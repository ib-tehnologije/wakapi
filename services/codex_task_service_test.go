package services

import (
	"testing"
	"time"

	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type inMemoryCodexTaskRepository struct {
	records map[string]*models.CodexTaskSession
}

func newInMemoryCodexTaskRepository() *inMemoryCodexTaskRepository {
	return &inMemoryCodexTaskRepository{records: map[string]*models.CodexTaskSession{}}
}

func (r *inMemoryCodexTaskRepository) Upsert(session *models.CodexTaskSession) error {
	r.records[session.UserID+":"+session.ExternalKey] = session
	return nil
}

func (r *inMemoryCodexTaskRepository) GetByUserWithin(userID string, from, to *time.Time, project string) ([]*models.CodexTaskSession, error) {
	result := make([]*models.CodexTaskSession, 0)
	for _, session := range r.records {
		if session.UserID != userID {
			continue
		}
		if project != "" && session.Project != project {
			continue
		}
		start := session.StartedAt.T()
		if from != nil && start.Before(*from) {
			continue
		}
		if to != nil && start.After(*to) {
			continue
		}
		result = append(result, session)
	}
	return result, nil
}

func TestCodexTaskService_UpsertManyBuildsFallbackSummaryAndDuration(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(12*time.Minute + 30*time.Second)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:           "codex:local:thread-1:turn-1",
		Project:               "OnixServer",
		WorkspaceRoot:         "/Users/igbenic/Projects/OnixServer",
		Repository:            "OnixServer",
		Branch:                "codex/codex-task-worklogs",
		StartedAt:             started,
		EndedAt:               &ended,
		Prompt:                "please implement codex task worklogs and make grunf summaries useful",
		LastAssistantMessage:  "Implemented a native Codex task endpoint and Onix sync.",
		Evidence:              []string{"routes/api/codex_tasks.go", "OnixWeb.Function/Services/WakaTimeSyncService.cs"},
		TechnicalEvidenceJSON: `{"tool_count":4}`,
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, 750.0, created[0].DurationSeconds)
	assert.Equal(t, models.CodexTaskSessionStatusClosed, created[0].Status)
	assert.Equal(t, "Rad na Codex worklog integraciji u Wakapiju.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "please implement codex task worklogs")
	assert.Contains(t, created[0].TechnicalNote, "routes/api/codex_tasks.go")
	assert.Equal(t, `{"tool_count":4}`, created[0].EvidenceJSON)
}

func TestCodexTaskService_UpsertManyPrefersEvidenceOverVagueProvidedSummary(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:          "codex:local:thread-1:turn-1",
		Project:              "wakapi",
		StartedAt:            started,
		EndedAt:              &ended,
		SummaryHR:            "Checked and patched it.",
		LastAssistantMessage: "Checked and patched it.",
		Evidence:             []string{"routes/api/codex_tasks.go"},
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Rad na Codex worklog integraciji u Wakapiju.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "Checked and patched it")
}

func TestCodexTaskService_UpsertManyPrefersEvidenceOverAssistantReply(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:          "codex:local:thread-1:turn-1",
		Project:              "IBTechK3SFleetRepo",
		StartedAt:            started,
		EndedAt:              &ended,
		LastAssistantMessage: "You use it as a Kubernetes TCP gateway, not as a ZeroTier interface inside Grunf/onix-api.",
		TechnicalEvidenceJSON: EncodeCodexEvidence(map[string]any{
			"events": []map[string]any{
				{
					"hook_event_name": "PostToolUse",
					"tool_name":       "Bash",
					"command":         "sed -n '1,140p' 02-fleet/05-apps/zerotier-client-gateway/zerotier-client-gateway-configmap.yaml",
				},
				{
					"hook_event_name": "PostToolUse",
					"tool_name":       "Bash",
					"command":         "sed -n '1,120p' 02-fleet/05-apps/zerotier-client-gateway/zerotier-client-gateway-service.yaml",
				},
			},
		}),
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Rad na deployu i Kubernetes konfiguraciji projekta IBTechK3SFleetRepo.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "You use it as")
}

func TestCodexTaskService_UpsertManyUsesCommandCategoryWhenNoFilesWereCaptured(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey: "codex:local:thread-1:turn-1",
		Project:     "IBTechK3SFleetRepo",
		StartedAt:   started,
		EndedAt:     &ended,
		SummaryHR:   "Patch applied successfully.",
		TechnicalEvidenceJSON: EncodeCodexEvidence(map[string]any{
			"events": []map[string]any{
				{
					"hook_event_name": "PostToolUse",
					"tool_name":       "Bash",
					"command":         "kubectl -n wakapi-system get deploy wakapi-backend-deployment",
				},
			},
		}),
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Rad na deployu i Kubernetes konfiguraciji projekta IBTechK3SFleetRepo.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "Patch applied")
}

func TestCodexTaskService_UpsertManyUsesToolCategoryWhenCommandTextIsAbsent(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey: "codex:local:thread-1:turn-1",
		Project:     "URA",
		StartedAt:   started,
		EndedAt:     &ended,
		SummaryHR:   "Good.",
		TechnicalEvidenceJSON: EncodeCodexEvidence(map[string]any{
			"events": []map[string]any{
				{
					"hook_event_name": "PostToolUse",
					"tool_name":       "mcp__onix_support_ticketing__onix_support_company_db_query",
				},
			},
		}),
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Analiza podataka u bazi za projekt URA.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "Good")
}

func TestCodexTaskService_UpsertManySkipsEnglishAssistantTitleJSON(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:          "codex:local:thread-1:turn-1",
		Project:              "URA",
		StartedAt:            started,
		EndedAt:              &ended,
		Prompt:               "raw user message should never become the visible summary",
		LastAssistantMessage: `{"title":"Review URA migration flow"}`,
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Codex sesija bez zabilježenog konteksta.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "raw user message")
	assert.NotContains(t, created[0].SummaryHR, "title")
}

func TestCodexTaskService_UpsertManySkipsEnglishAssistantMessageJSON(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:          "codex:local:thread-1:turn-1",
		Project:              "OnixServer",
		StartedAt:            started,
		EndedAt:              &ended,
		Prompt:               "raw user message should never become the visible summary",
		LastAssistantMessage: `{"message":"Add hide action for TeamViewer sessions"}`,
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Codex sesija bez zabilježenog konteksta.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "raw user message")
	assert.NotContains(t, created[0].SummaryHR, "message")
}

func TestCodexTaskService_UpsertManySkipsEnglishProvidedSummary(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey: "codex:local:thread-1:turn-1",
		Project:     "wakapi",
		StartedAt:   started,
		EndedAt:     &ended,
		Prompt:      "raw user message should never become the visible summary",
		SummaryHR:   "Generated via Codex summary.",
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Codex sesija bez zabilježenog konteksta.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "Generated via")
}

func TestCodexTaskService_UpsertManySkipsUselessAssistantFallbackText(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:          "codex:local:thread-1:turn-1",
		Project:              "URA",
		StartedAt:            started,
		EndedAt:              &ended,
		Prompt:               "raw user message should never become the visible summary",
		LastAssistantMessage: "...",
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Codex sesija bez zabilježenog konteksta.", created[0].SummaryHR)
	assert.NotEqual(t, "...", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "raw user message")
}

func TestCodexTaskService_UpsertManySkipsFillerAssistantAcknowledgements(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	user := &models.User{ID: "user"}

	created, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:          "codex:local:thread-1:turn-1",
		Project:              "OnixServer",
		StartedAt:            started,
		EndedAt:              &ended,
		Prompt:               "raw user message should never become the visible summary",
		LastAssistantMessage: "You're right.",
	}})

	require.NoError(t, err)
	require.Len(t, created, 1)
	assert.Equal(t, "Codex sesija bez zabilježenog konteksta.", created[0].SummaryHR)
	assert.NotEqual(t, "You're right.", created[0].SummaryHR)
	assert.NotContains(t, created[0].SummaryHR, "raw user message")
}

func TestCodexTaskService_GetWorklogsReturnsClosedTaskShape(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(10 * time.Minute)
	user := &models.User{ID: "user"}

	_, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:     "codex:local:thread-1:turn-1",
		Project:         "OnixServer",
		StartedAt:       started,
		EndedAt:         &ended,
		SummaryHR:       "Implementirana je sinkronizacija Codex zadataka.",
		DurationSeconds: 600,
	}})
	require.NoError(t, err)

	worklogs, err := sut.GetWorklogs(user, &started, &ended, "OnixServer")

	require.NoError(t, err)
	require.Len(t, worklogs, 1)
	assert.Equal(t, "CodexTask", worklogs[0].Source)
	assert.Equal(t, "codex:chat:local:thread-1:20260514:OnixServer", worklogs[0].ExternalKey)
	assert.Equal(t, "Implementirana je sinkronizacija Codex zadataka.", worklogs[0].Summary)
	assert.Equal(t, 600.0, worklogs[0].DurationSeconds)
}

func TestCodexTaskService_GetWorklogsGroupsClosedTurnsByChatProjectAndDay(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	dayStart := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	dayEnd := time.Date(2026, 5, 14, 23, 59, 59, 0, time.UTC)
	user := &models.User{ID: "user"}

	firstStart := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	firstEnd := firstStart.Add(10 * time.Minute)
	secondStart := time.Date(2026, 5, 14, 9, 15, 0, 0, time.UTC)
	secondEnd := secondStart.Add(5 * time.Minute)
	otherDayStart := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	otherDayEnd := otherDayStart.Add(3 * time.Minute)

	_, err := sut.UpsertMany(user, []*CodexTaskSessionInput{
		{
			ExternalKey:     "codex:local:thread-1:turn-1",
			Project:         "OnixServer",
			StartedAt:       firstStart,
			EndedAt:         &firstEnd,
			SummaryHR:       "Implementirana je sinkronizacija Codex zadataka.",
			DurationSeconds: 600,
			TechnicalEvidenceJSON: EncodeCodexEvidence(map[string]any{
				"events": []map[string]any{{"tool_name": "Bash", "command": "go test ./services"}},
			}),
		},
		{
			ExternalKey:     "codex:local:thread-1:turn-2",
			Project:         "OnixServer",
			StartedAt:       secondStart,
			EndedAt:         &secondEnd,
			SummaryHR:       "Provjereno stanje baze podataka.",
			DurationSeconds: 300,
		},
		{
			ExternalKey:     "codex:local:thread-1:turn-3",
			Project:         "wakapi",
			StartedAt:       secondStart,
			EndedAt:         &secondEnd,
			SummaryHR:       "Ažurirano services/codex_task_service.go.",
			DurationSeconds: 300,
		},
		{
			ExternalKey:     "codex:local:thread-1:turn-4",
			Project:         "OnixServer",
			StartedAt:       otherDayStart,
			EndedAt:         &otherDayEnd,
			SummaryHR:       "Ažurirano other-day.go.",
			DurationSeconds: 180,
		},
		{
			ExternalKey: "codex:local:thread-1:turn-open",
			Project:     "OnixServer",
			StartedAt:   secondStart.Add(30 * time.Minute),
		},
	})
	require.NoError(t, err)

	worklogs, err := sut.GetWorklogs(user, &dayStart, &dayEnd, "OnixServer")

	require.NoError(t, err)
	require.Len(t, worklogs, 1)
	assert.Equal(t, "codex:chat:local:thread-1:20260514:OnixServer", worklogs[0].ExternalKey)
	assert.Equal(t, firstStart, worklogs[0].StartedAt)
	assert.Equal(t, secondEnd, worklogs[0].EndedAt)
	assert.Equal(t, 900.0, worklogs[0].DurationSeconds)
	assert.Equal(t, "Implementirana je sinkronizacija Codex zadataka.", worklogs[0].Summary)
	assert.Contains(t, worklogs[0].TechnicalNote, "Grupirano 2 Codex turna iz istog chata.")
	assert.Contains(t, worklogs[0].TechnicalNote, "codex:local:thread-1:turn-1")
	assert.Contains(t, worklogs[0].TechnicalNote, "codex:local:thread-1:turn-2")
}

func TestCodexTaskService_GetWorklogsSkipsTinyTurnsWithoutEvidence(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	dayStart := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	dayEnd := time.Date(2026, 5, 14, 23, 59, 59, 0, time.UTC)
	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	tinyEnd := started.Add(5 * time.Second)
	realStart := started.Add(1 * time.Minute)
	realEnd := realStart.Add(10 * time.Minute)
	user := &models.User{ID: "user"}

	_, err := sut.UpsertMany(user, []*CodexTaskSessionInput{
		{
			ExternalKey:           "codex:local:thread-tiny:turn-1",
			Project:               "URA",
			WorkspaceRoot:         "/Users/igbenic/Projects/URA",
			StartedAt:             started,
			EndedAt:               &tinyEnd,
			DurationSeconds:       5,
			TechnicalEvidenceJSON: `{"events":[],"session_id":"thread-tiny","turn_id":"turn-1"}`,
		},
		{
			ExternalKey:     "codex:local:thread-real:turn-1",
			Project:         "URA",
			WorkspaceRoot:   "/Users/igbenic/Projects/URA",
			StartedAt:       realStart,
			EndedAt:         &realEnd,
			DurationSeconds: 600,
			SummaryHR:       "Ažurirano URA_TST/steps/15.sql.",
		},
	})
	require.NoError(t, err)

	worklogs, err := sut.GetWorklogs(user, &dayStart, &dayEnd, "URA")

	require.NoError(t, err)
	require.Len(t, worklogs, 1)
	assert.Equal(t, "codex:chat:local:thread-real:20260514:URA", worklogs[0].ExternalKey)
	assert.Equal(t, 600.0, worklogs[0].DurationSeconds)
}

func TestCodexTaskService_GetWorklogsKeepsTinyTurnsWithEvidence(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Second)
	user := &models.User{ID: "user"}

	_, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:     "codex:local:thread-short:turn-1",
		Project:         "URA",
		WorkspaceRoot:   "/Users/igbenic/Projects/URA",
		StartedAt:       started,
		EndedAt:         &ended,
		DurationSeconds: 5,
		Evidence:        []string{"command: SELECT TOP 1 * FROM Documents"},
		SummaryHR:       "Provjereno stanje baze podataka.",
	}})
	require.NoError(t, err)

	worklogs, err := sut.GetWorklogs(user, &started, &ended, "URA")

	require.NoError(t, err)
	require.Len(t, worklogs, 1)
	assert.Equal(t, "codex:chat:local:thread-short:20260514:URA", worklogs[0].ExternalKey)
	assert.Equal(t, 5.0, worklogs[0].DurationSeconds)
}

func TestCodexTaskService_GetWorklogsBuildsIntentSummaryInsteadOfFileBreadcrumbs(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 21, 0, 1, 0, 0, time.UTC)
	ended := started.Add(45 * time.Minute)
	user := &models.User{ID: "user"}

	_, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:          "codex:local:thread-delphi:turn-1",
		Project:              "Delphi-decompiler-IDR",
		WorkspaceRoot:        "/Users/igbenic/Projects/Delphi-decompiler-IDR",
		StartedAt:            started,
		EndedAt:              &ended,
		DurationSeconds:      2700,
		SummaryHR:            "Ažurirano cli/check.sh.",
		Prompt:               "make the Delphi decompiler CLI check script more useful",
		LastAssistantMessage: "Updated cli/check.sh and reran the decompiler sample validation.",
		Evidence:             []string{"cli/check.sh"},
	}})
	require.NoError(t, err)

	worklogs, err := sut.GetWorklogs(user, &started, &ended, "Delphi-decompiler-IDR")

	require.NoError(t, err)
	require.Len(t, worklogs, 1)
	assert.Equal(t, "Rad na CLI provjerama i validaciji Delphi decompilera.", worklogs[0].Summary)
	assert.NotContains(t, worklogs[0].Summary, "Ažurirano")
	assert.NotContains(t, worklogs[0].Summary, "cli/check.sh")
}

func TestCodexTaskService_GetWorklogsBuildsWakapiIntentFromTouchedRepo(t *testing.T) {
	repo := newInMemoryCodexTaskRepository()
	sut := NewCodexTaskService(repo)

	started := time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC)
	ended := started.Add(30 * time.Minute)
	user := &models.User{ID: "user"}

	_, err := sut.UpsertMany(user, []*CodexTaskSessionInput{{
		ExternalKey:          "codex:local:thread-wakapi:turn-1",
		Project:              "OnixServer",
		WorkspaceRoot:        "/Users/igbenic/Projects/OnixServer",
		StartedAt:            started,
		EndedAt:              &ended,
		DurationSeconds:      1800,
		SummaryHR:            "Ažurirano /Users/igbenic/Projects/wakapi/services/codex_task_service.go.",
		Prompt:               "make Wakapi Codex worklog messages smarter",
		LastAssistantMessage: "Added grouped Codex worklog summary logic in Wakapi.",
		Evidence:             []string{"/Users/igbenic/Projects/wakapi/services/codex_task_service.go"},
	}})
	require.NoError(t, err)

	worklogs, err := sut.GetWorklogs(user, &started, &ended, "OnixServer")

	require.NoError(t, err)
	require.Len(t, worklogs, 1)
	assert.Equal(t, "Rad na Codex worklog integraciji u Wakapiju.", worklogs[0].Summary)
	assert.NotContains(t, worklogs[0].Summary, "Ažurirano")
	assert.NotContains(t, worklogs[0].Summary, "codex_task_service.go")
}
