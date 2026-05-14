package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gofrs/uuid/v5"
	"github.com/muety/wakapi/models"
)

type codexTaskSessionRepository interface {
	Upsert(*models.CodexTaskSession) error
	GetByUserWithin(string, *time.Time, *time.Time, string) ([]*models.CodexTaskSession, error)
}

type ICodexTaskService interface {
	UpsertMany(*models.User, []*CodexTaskSessionInput) ([]*models.CodexTaskSession, error)
	GetWorklogs(*models.User, *time.Time, *time.Time, string) ([]*CodexTaskWorklog, error)
}

type CodexTaskSessionInput struct {
	ExternalKey           string
	Project               string
	WorkspaceRoot         string
	Repository            string
	Branch                string
	StartedAt             time.Time
	EndedAt               *time.Time
	DurationSeconds       float64
	Status                string
	SummaryHR             string
	Prompt                string
	LastAssistantMessage  string
	Evidence              []string
	TechnicalEvidenceJSON string
}

type CodexTaskWorklog struct {
	ID              string
	ExternalKey     string
	Project         string
	Source          string
	StartedAt       time.Time
	EndedAt         time.Time
	DurationSeconds float64
	Summary         string
	TechnicalNote   string
	WorkspaceRoot   string
	Repository      string
	Branch          string
	Status          string
}

type CodexTaskService struct {
	repository codexTaskSessionRepository
}

func NewCodexTaskService(repository codexTaskSessionRepository) *CodexTaskService {
	return &CodexTaskService{repository: repository}
}

func (s *CodexTaskService) UpsertMany(user *models.User, inputs []*CodexTaskSessionInput) ([]*models.CodexTaskSession, error) {
	if user == nil || user.ID == "" {
		return nil, errors.New("user is required")
	}

	result := make([]*models.CodexTaskSession, 0, len(inputs))
	for _, input := range inputs {
		session, err := s.buildSession(user, input)
		if err != nil {
			return nil, err
		}
		if err := s.repository.Upsert(session); err != nil {
			return nil, err
		}
		result = append(result, session)
	}

	return result, nil
}

func (s *CodexTaskService) GetWorklogs(user *models.User, from, to *time.Time, project string) ([]*CodexTaskWorklog, error) {
	sessions, err := s.repository.GetByUserWithin(user.ID, from, to, project)
	if err != nil {
		return nil, err
	}

	worklogs := make([]*CodexTaskWorklog, 0, len(sessions))
	for _, session := range sessions {
		if session.EndedAt == nil {
			continue
		}
		worklogs = append(worklogs, &CodexTaskWorklog{
			ID:              session.ID,
			ExternalKey:     session.ExternalKey,
			Project:         session.Project,
			Source:          models.CodexTaskWorklogSource,
			StartedAt:       session.StartedAt.T(),
			EndedAt:         session.EndedAt.T(),
			DurationSeconds: session.DurationSeconds,
			Summary:         session.SummaryHR,
			TechnicalNote:   session.TechnicalNote,
			WorkspaceRoot:   session.WorkspaceRoot,
			Repository:      session.Repository,
			Branch:          session.Branch,
			Status:          session.Status,
		})
	}
	return worklogs, nil
}

func (s *CodexTaskService) buildSession(user *models.User, input *CodexTaskSessionInput) (*models.CodexTaskSession, error) {
	if input == nil {
		return nil, errors.New("session is required")
	}

	input.ExternalKey = strings.TrimSpace(input.ExternalKey)
	input.Project = strings.TrimSpace(input.Project)
	if input.ExternalKey == "" {
		return nil, errors.New("external_key is required")
	}
	if input.Project == "" {
		return nil, errors.New("project is required")
	}
	if input.StartedAt.IsZero() {
		return nil, errors.New("started_at is required")
	}
	if input.EndedAt != nil && input.EndedAt.Before(input.StartedAt) {
		return nil, errors.New("ended_at must be after started_at")
	}

	duration := input.DurationSeconds
	if duration <= 0 && input.EndedAt != nil {
		duration = input.EndedAt.Sub(input.StartedAt).Seconds()
	}
	if duration < 0 {
		duration = 0
	}

	status := strings.TrimSpace(input.Status)
	if status == "" {
		if input.EndedAt == nil {
			status = models.CodexTaskSessionStatusOpen
		} else {
			status = models.CodexTaskSessionStatusClosed
		}
	}

	summary := strings.TrimSpace(input.SummaryHR)
	if summary == "" {
		summary = buildCodexSummary(input)
	}

	technicalNote := buildCodexTechnicalNote(input)

	var endedAt *models.CustomTime
	if input.EndedAt != nil {
		custom := models.CustomTime(*input.EndedAt)
		endedAt = &custom
	}

	return &models.CodexTaskSession{
		ID:                   uuid.Must(uuid.NewV4()).String(),
		User:                 user,
		UserID:               user.ID,
		ExternalKey:          input.ExternalKey,
		Project:              input.Project,
		WorkspaceRoot:        strings.TrimSpace(input.WorkspaceRoot),
		Repository:           strings.TrimSpace(input.Repository),
		Branch:               strings.TrimSpace(input.Branch),
		StartedAt:            models.CustomTime(input.StartedAt),
		EndedAt:              endedAt,
		DurationSeconds:      duration,
		Status:               status,
		SummaryHR:            summary,
		Prompt:               strings.TrimSpace(input.Prompt),
		LastAssistantMessage: strings.TrimSpace(input.LastAssistantMessage),
		EvidenceJSON:         strings.TrimSpace(input.TechnicalEvidenceJSON),
		TechnicalNote:        technicalNote,
	}, nil
}

func buildCodexSummary(input *CodexTaskSessionInput) string {
	project := strings.TrimSpace(input.Project)
	if project == "" {
		project = "projektu"
	}

	work := normalizeSentence(input.Prompt)
	if work == "" {
		work = normalizeSentence(input.LastAssistantMessage)
	}
	if work == "" {
		work = "obraden je Codex zadatak"
	}

	summary := fmt.Sprintf("Rad na projektu %s: %s.", project, strings.TrimSuffix(work, "."))
	if len(input.Evidence) > 0 {
		summary += fmt.Sprintf(" Zabiljezene su aktivnosti na: %s.", strings.Join(limitStrings(input.Evidence, 3), ", "))
	}
	return summary
}

func buildCodexTechnicalNote(input *CodexTaskSessionInput) string {
	parts := []string{}
	if len(input.Evidence) > 0 {
		parts = append(parts, fmt.Sprintf("Codex evidence: %d captured items (%s).", len(input.Evidence), strings.Join(limitStrings(input.Evidence, 8), ", ")))
	}
	if strings.TrimSpace(input.WorkspaceRoot) != "" {
		parts = append(parts, fmt.Sprintf("Workspace: %s.", strings.TrimSpace(input.WorkspaceRoot)))
	}
	if strings.TrimSpace(input.Branch) != "" {
		parts = append(parts, fmt.Sprintf("Branch: %s.", strings.TrimSpace(input.Branch)))
	}
	if len(parts) == 0 {
		parts = append(parts, "Codex evidence: no captured tool evidence.")
	}
	return strings.Join(parts, " ")
}

func normalizeSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimPrefix(strings.ToLower(s), "please ")
	replacements := map[string]string{
		"codex":    "Codex",
		"grunf":    "Grunf",
		"wakapi":   "Wakapi",
		"wakatime": "WakaTime",
		"onix":     "Onix",
	}
	for old, replacement := range replacements {
		s = strings.ReplaceAll(s, old, replacement)
	}
	return capitalizeFirst(s)
}

func capitalizeFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

func limitStrings(values []string, max int) []string {
	seen := map[string]bool{}
	result := make([]string, 0, max)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == max {
			break
		}
	}
	return result
}

func EncodeCodexEvidence(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
