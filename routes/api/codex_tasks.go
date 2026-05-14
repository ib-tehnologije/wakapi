package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/duke-git/lancet/v2/datetime"
	"github.com/go-chi/chi/v5"
	conf "github.com/muety/wakapi/config"
	"github.com/muety/wakapi/helpers"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/models"
	routeutils "github.com/muety/wakapi/routes/utils"
	"github.com/muety/wakapi/services"
)

type CodexTaskApiHandler struct {
	userSrvc      services.IUserService
	codexTaskSrvc services.ICodexTaskService
}

func NewCodexTaskApiHandler(userService services.IUserService, codexTaskService services.ICodexTaskService) *CodexTaskApiHandler {
	return &CodexTaskApiHandler{userSrvc: userService, codexTaskSrvc: codexTaskService}
}

func (h *CodexTaskApiHandler) RegisterRoutes(router chi.Router) {
	router.Group(func(r chi.Router) {
		r.Use(middlewares.NewAuthenticateMiddleware(h.userSrvc).WithFullAccessOnly(true).Handler)
		r.Post("/integrations/codex/task-sessions", h.PostTaskSessions)
		r.Post("/integrations/codex/task-sessions.bulk", h.PostTaskSessions)
		r.Get("/compat/onix/v1/users/{user}/worklogs", h.GetOnixWorklogs)
	})
}

type codexTaskSessionRequest struct {
	ExternalKey          string          `json:"external_key"`
	Project              string          `json:"project"`
	WorkspaceRoot        string          `json:"workspace_root"`
	Repository           string          `json:"repository"`
	Branch               string          `json:"branch"`
	StartedAt            time.Time       `json:"started_at"`
	EndedAt              *time.Time      `json:"ended_at"`
	DurationSeconds      float64         `json:"duration_seconds"`
	Status               string          `json:"status"`
	SummaryHR            string          `json:"summary_hr"`
	Prompt               string          `json:"prompt"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	Evidence             []string        `json:"evidence"`
	TechnicalEvidence    json.RawMessage `json:"technical_evidence"`
	TechnicalEvidenceRaw string          `json:"technical_evidence_json"`
}

type codexTaskSessionsRequest struct {
	Sessions []*codexTaskSessionRequest `json:"sessions"`
}

type codexTaskSessionResponse struct {
	ID              string     `json:"id"`
	ExternalKey     string     `json:"external_key"`
	Project         string     `json:"project"`
	Source          string     `json:"source"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	DurationSeconds float64    `json:"duration_seconds"`
	Status          string     `json:"status"`
	Summary         string     `json:"summary"`
	TechnicalNote   string     `json:"technical_note,omitempty"`
	WorkspaceRoot   string     `json:"workspace_root,omitempty"`
	Repository      string     `json:"repository,omitempty"`
	Branch          string     `json:"branch,omitempty"`
}

type codexTaskSessionsResponse struct {
	Data []*codexTaskSessionResponse `json:"data"`
}

type codexTaskWorklogsResponse struct {
	Data []*codexTaskSessionResponse `json:"data"`
}

func (h *CodexTaskApiHandler) PostTaskSessions(w http.ResponseWriter, r *http.Request) {
	user, err := routeutils.CheckEffectiveUser(w, r, h.userSrvc, "current")
	if err != nil {
		return
	}

	sessions, err := decodeCodexTaskSessions(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	inputs := make([]*services.CodexTaskSessionInput, 0, len(sessions))
	for _, session := range sessions {
		inputs = append(inputs, session.toServiceInput())
	}

	result, err := h.codexTaskSrvc.UpsertMany(user, inputs)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	helpers.RespondJSON(w, r, http.StatusCreated, &codexTaskSessionsResponse{Data: codexTaskSessionsToResponse(result)})
}

func (h *CodexTaskApiHandler) GetOnixWorklogs(w http.ResponseWriter, r *http.Request) {
	user, err := routeutils.CheckEffectiveUser(w, r, h.userSrvc, "current")
	if err != nil {
		return
	}

	q := r.URL.Query()
	source := strings.ToLower(strings.TrimSpace(q.Get("source")))
	if source != "" && source != "codex" && source != strings.ToLower(models.CodexTaskWorklogSource) {
		helpers.RespondJSON(w, r, http.StatusOK, &codexTaskWorklogsResponse{Data: []*codexTaskSessionResponse{}})
		return
	}

	from, to, err := parseCodexWorklogDateRange(q.Get("start"), q.Get("end"), user.TZ())
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	worklogs, err := h.codexTaskSrvc.GetWorklogs(user, from, to, strings.TrimSpace(q.Get("project")))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(conf.ErrInternalServerError))
		conf.Log().Request(r).Error("failed to get codex worklogs", "error", err)
		return
	}

	helpers.RespondJSON(w, r, http.StatusOK, &codexTaskWorklogsResponse{Data: codexWorklogsToResponse(worklogs)})
}

func decodeCodexTaskSessions(r *http.Request) ([]*codexTaskSessionRequest, error) {
	var wrapper codexTaskSessionsRequest
	if err := json.NewDecoder(r.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("invalid request body")
	}
	if len(wrapper.Sessions) == 0 {
		return nil, fmt.Errorf("sessions are required")
	}
	return wrapper.Sessions, nil
}

func (s *codexTaskSessionRequest) toServiceInput() *services.CodexTaskSessionInput {
	technicalEvidence := strings.TrimSpace(s.TechnicalEvidenceRaw)
	if technicalEvidence == "" && len(s.TechnicalEvidence) > 0 {
		technicalEvidence = string(s.TechnicalEvidence)
	}
	return &services.CodexTaskSessionInput{
		ExternalKey:           s.ExternalKey,
		Project:               s.Project,
		WorkspaceRoot:         s.WorkspaceRoot,
		Repository:            s.Repository,
		Branch:                s.Branch,
		StartedAt:             s.StartedAt,
		EndedAt:               s.EndedAt,
		DurationSeconds:       s.DurationSeconds,
		Status:                s.Status,
		SummaryHR:             s.SummaryHR,
		Prompt:                s.Prompt,
		LastAssistantMessage:  s.LastAssistantMessage,
		Evidence:              s.Evidence,
		TechnicalEvidenceJSON: technicalEvidence,
	}
}

func codexTaskSessionsToResponse(sessions []*models.CodexTaskSession) []*codexTaskSessionResponse {
	result := make([]*codexTaskSessionResponse, 0, len(sessions))
	for _, session := range sessions {
		var endedAt *time.Time
		if session.EndedAt != nil {
			t := session.EndedAt.T()
			endedAt = &t
		}
		result = append(result, &codexTaskSessionResponse{
			ID:              session.ID,
			ExternalKey:     session.ExternalKey,
			Project:         session.Project,
			Source:          models.CodexTaskWorklogSource,
			StartedAt:       session.StartedAt.T(),
			EndedAt:         endedAt,
			DurationSeconds: session.DurationSeconds,
			Status:          session.Status,
			Summary:         session.SummaryHR,
			TechnicalNote:   session.TechnicalNote,
			WorkspaceRoot:   session.WorkspaceRoot,
			Repository:      session.Repository,
			Branch:          session.Branch,
		})
	}
	return result
}

func codexWorklogsToResponse(worklogs []*services.CodexTaskWorklog) []*codexTaskSessionResponse {
	result := make([]*codexTaskSessionResponse, 0, len(worklogs))
	for _, worklog := range worklogs {
		endedAt := worklog.EndedAt
		result = append(result, &codexTaskSessionResponse{
			ID:              worklog.ID,
			ExternalKey:     worklog.ExternalKey,
			Project:         worklog.Project,
			Source:          worklog.Source,
			StartedAt:       worklog.StartedAt,
			EndedAt:         &endedAt,
			DurationSeconds: worklog.DurationSeconds,
			Status:          worklog.Status,
			Summary:         worklog.Summary,
			TechnicalNote:   worklog.TechnicalNote,
			WorkspaceRoot:   worklog.WorkspaceRoot,
			Repository:      worklog.Repository,
			Branch:          worklog.Branch,
		})
	}
	return result
}

func parseCodexWorklogDateRange(startRaw, endRaw string, tz *time.Location) (*time.Time, *time.Time, error) {
	var start, end *time.Time
	if strings.TrimSpace(startRaw) != "" {
		parsed, err := parseCodexDate(startRaw, tz)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid start")
		}
		if isCodexDateOnly(startRaw) {
			parsed = datetime.BeginOfDay(parsed)
		}
		start = &parsed
	}
	if strings.TrimSpace(endRaw) != "" {
		parsed, err := parseCodexDate(endRaw, tz)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid end")
		}
		if isCodexDateOnly(endRaw) {
			parsed = datetime.EndOfDay(parsed)
		}
		end = &parsed
	}
	if start != nil && end != nil && start.After(*end) {
		return nil, nil, fmt.Errorf("start must be before or equal to end")
	}
	return start, end, nil
}

func parseCodexDate(raw string, tz *time.Location) (time.Time, error) {
	return helpers.ParseDateTimeTZ(strings.Replace(strings.TrimSpace(raw), " ", "+", 1), tz)
}

func isCodexDateOnly(v string) bool {
	v = strings.TrimSpace(v)
	if len(v) != len(conf.SimpleDateFormat) {
		return false
	}
	_, err := time.Parse(conf.SimpleDateFormat, v)
	return err == nil
}
