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
	assert.Contains(t, created[0].SummaryHR, "Rad na projektu OnixServer")
	assert.Contains(t, created[0].SummaryHR, "Codex task worklogs")
	assert.Contains(t, created[0].TechnicalNote, "routes/api/codex_tasks.go")
	assert.Equal(t, `{"tool_count":4}`, created[0].EvidenceJSON)
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
	assert.Equal(t, "codex:local:thread-1:turn-1", worklogs[0].ExternalKey)
	assert.Equal(t, "Implementirana je sinkronizacija Codex zadataka.", worklogs[0].Summary)
	assert.Equal(t, 600.0, worklogs[0].DurationSeconds)
}
