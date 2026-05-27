package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/mocks"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type stubCodexTaskService struct {
	upserted           []*services.CodexTaskSessionInput
	list               []*services.CodexTaskWorklog
	from               *time.Time
	to                 *time.Time
	project            string
	reviewQueue        []*models.CodexTaskSession
	reviewQueueStatus  string
	reviewQueueLimit   int
	lastReviewInput    *services.CodexTaskReviewInput
	lastReviewExternal string
}

func (s *stubCodexTaskService) UpsertMany(user *models.User, sessions []*services.CodexTaskSessionInput) ([]*models.CodexTaskSession, error) {
	s.upserted = sessions
	result := make([]*models.CodexTaskSession, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, &models.CodexTaskSession{
			ID:              "task-1",
			UserID:          user.ID,
			ExternalKey:     session.ExternalKey,
			Project:         session.Project,
			StartedAt:       models.CustomTime(session.StartedAt),
			EndedAt:         customTimePtr(session.EndedAt),
			DurationSeconds: session.DurationSeconds,
			Status:          models.CodexTaskSessionStatusClosed,
			SummaryHR:       "Implementirana je sinkronizacija Codex zadataka u Wakapi. Dodan je cisti sazetak za Grunf bez oslanjanja na commitove.",
		})
	}
	return result, nil
}

func (s *stubCodexTaskService) GetWorklogs(user *models.User, from, to *time.Time, project string) ([]*services.CodexTaskWorklog, error) {
	s.from = from
	s.to = to
	s.project = project
	return s.list, nil
}

func (s *stubCodexTaskService) ListReviewQueue(user *models.User, from, to *time.Time, project string, status string, limit int) ([]*models.CodexTaskSession, error) {
	s.from = from
	s.to = to
	s.project = project
	s.reviewQueueStatus = status
	s.reviewQueueLimit = limit
	return s.reviewQueue, nil
}

func (s *stubCodexTaskService) ReviewSession(user *models.User, input *services.CodexTaskReviewInput) (*models.CodexTaskSession, error) {
	s.lastReviewInput = input
	if input != nil {
		s.lastReviewExternal = input.ExternalKey
	}
	return &models.CodexTaskSession{
		ID:                  "reviewed-1",
		UserID:              user.ID,
		ExternalKey:         input.ExternalKey,
		Project:             "OnixServer",
		SummaryHR:           "Ručno uređen sažetak za klijenta.",
		SummaryHROriginal:   "Planiranje implementacije za projekt OnixServer.",
		SummaryHRNormalized: "Ručno uređen sažetak za klijenta.",
		SummarySource:       "human_review",
		SummaryConfidence:   0.90,
		ClientMessageHR:     "Ručno uređen sažetak za klijenta.",
		InternalMessageHR:   "Ručno odobreno nakon pregleda.",
		ReviewStatus:        "approved",
		Status:              models.CodexTaskSessionStatusClosed,
		StartedAt:           models.CustomTime(time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)),
		EndedAt:             customTimePtr(timePtr(time.Date(2026, 5, 14, 9, 20, 0, 0, time.UTC))),
		DurationSeconds:     1200,
	}, nil
}

func customTimePtr(t *time.Time) *models.CustomTime {
	if t == nil {
		return nil
	}
	custom := models.CustomTime(*t)
	return &custom
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func TestCodexTaskApiHandler_PostTaskSessions(t *testing.T) {
	config.Set(config.Empty())

	user := &models.User{ID: "user", ApiKey: "apikey"}
	userService := new(mocks.UserServiceMock)
	userService.On("GetUserByKey", user.ApiKey, true).Return(user, nil)

	codexService := &stubCodexTaskService{}
	handler := NewCodexTaskApiHandler(userService, codexService)

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	body := []byte(`{
		"sessions": [{
			"external_key": "codex:local:thread-1:turn-1",
			"project": "OnixServer",
			"workspace_root": "/Users/igbenic/Projects/OnixServer",
			"repository": "OnixServer",
			"branch": "codex/codex-task-worklogs",
			"started_at": "2026-05-14T09:00:00+02:00",
			"ended_at": "2026-05-14T09:21:30+02:00",
			"duration_seconds": 1290,
			"prompt": "implement codex task worklogs",
			"last_assistant_message": "Implemented the sync path.",
			"evidence": ["OnixWeb.Function/Services/WakaTimeSyncService.cs", "routes/api/codex_tasks.go"]
		}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/codex/task-sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	if assert.Len(t, codexService.upserted, 1) {
		assert.Equal(t, "codex:local:thread-1:turn-1", codexService.upserted[0].ExternalKey)
		assert.Equal(t, "OnixServer", codexService.upserted[0].Project)
		assert.Equal(t, 1290.0, codexService.upserted[0].DurationSeconds)
		assert.Contains(t, codexService.upserted[0].Evidence, "routes/api/codex_tasks.go")
	}

	var response codexTaskSessionsResponse
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Len(t, response.Data, 1)
	assert.Equal(t, "CodexTask", response.Data[0].Source)
	assert.Contains(t, response.Data[0].Summary, "Codex zadataka")

	userService.AssertExpectations(t)
}

func TestCodexTaskApiHandler_GetOnixWorklogs(t *testing.T) {
	config.Set(config.Empty())

	tz, err := time.LoadLocation("Europe/Zagreb")
	assert.NoError(t, err)
	user := &models.User{ID: "user", ApiKey: "apikey", Location: tz.String()}
	userService := new(mocks.UserServiceMock)
	userService.On("GetUserByKey", user.ApiKey, false).Return(user, nil)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, tz)
	ended := started.Add(21*time.Minute + 30*time.Second)
	codexService := &stubCodexTaskService{
		list: []*services.CodexTaskWorklog{{
			ID:              "task-1",
			ExternalKey:     "codex:local:thread-1:turn-1",
			Project:         "OnixServer",
			Source:          "CodexTask",
			StartedAt:       started,
			EndedAt:         ended,
			DurationSeconds: 1290,
			Summary:         "Implementirana je sinkronizacija Codex zadataka u Wakapi.",
			TechnicalNote:   "Codex evidence: 2 captured items.",
			WorkspaceRoot:   "/Users/igbenic/Projects/OnixServer",
			Repository:      "OnixServer",
			Branch:          "codex/codex-task-worklogs",
		}},
	}
	handler := NewCodexTaskApiHandler(userService, codexService)

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	req := httptest.NewRequest(http.MethodGet, "/api/compat/onix/v1/users/current/worklogs?start=2026-05-14&end=2026-05-14&source=codex&project=OnixServer", nil)
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, codexService.from)
	assert.NotNil(t, codexService.to)
	assert.Equal(t, "OnixServer", codexService.project)
	assert.Equal(t, 0, codexService.from.In(tz).Hour())
	assert.Equal(t, 23, codexService.to.In(tz).Hour())

	var response codexTaskWorklogsResponse
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	if assert.Len(t, response.Data, 1) {
		assert.Equal(t, "CodexTask", response.Data[0].Source)
		assert.Equal(t, "Implementirana je sinkronizacija Codex zadataka u Wakapi.", response.Data[0].Summary)
		assert.Equal(t, 1290.0, response.Data[0].DurationSeconds)
	}

	userService.AssertExpectations(t)
	userService.AssertNotCalled(t, "GetUserById", mock.Anything)
}

func TestCodexTaskApiHandler_GetOnixWorklogs_AllowsReadOnlyApiKey(t *testing.T) {
	config.Set(config.Empty())

	tz, err := time.LoadLocation("Europe/Zagreb")
	assert.NoError(t, err)
	user := &models.User{ID: "user", ApiKey: "readonly-key", Location: tz.String()}
	userService := new(mocks.UserServiceMock)
	userService.On("GetUserByKey", user.ApiKey, false).Return(user, nil)

	codexService := &stubCodexTaskService{}
	handler := NewCodexTaskApiHandler(userService, codexService)

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	req := httptest.NewRequest(http.MethodGet, "/api/compat/onix/v1/users/current/worklogs?start=2026-05-14&end=2026-05-14&source=codex&project=OnixServer", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OnixServer", codexService.project)
	userService.AssertExpectations(t)
}

func TestCodexTaskApiHandler_GetReviewQueue(t *testing.T) {
	config.Set(config.Empty())

	user := &models.User{ID: "user", ApiKey: "apikey"}
	userService := new(mocks.UserServiceMock)
	userService.On("GetUserByKey", user.ApiKey, true).Return(user, nil)

	started := time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC)
	ended := started.Add(20 * time.Minute)
	codexService := &stubCodexTaskService{
		reviewQueue: []*models.CodexTaskSession{{
			ID:                  "task-1",
			UserID:              user.ID,
			ExternalKey:         "codex:local:thread-review:turn-1",
			Project:             "OnixServer",
			StartedAt:           models.CustomTime(started),
			EndedAt:             customTimePtr(&ended),
			DurationSeconds:     1200,
			Status:              models.CodexTaskSessionStatusClosed,
			SummaryHR:           "Codex aktivnost zahtijeva ručni pregled.",
			SummaryHROriginal:   "Rad na testovima i provjerama projekta OnixServer.",
			SummaryHRNormalized: "Rad na testovima i provjerama projekta OnixServer.",
			SummarySource:       "evidence",
			SummaryConfidence:   0.44,
			ClientMessageHR:     "",
			InternalMessageHR:   "Potreban ručni pregled prije klijentske sinkronizacije.",
			ReviewStatus:        "needs_review",
		}},
	}
	handler := NewCodexTaskApiHandler(userService, codexService)

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations/codex/review-queue?project=OnixServer&status=pending&limit=25", nil)
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OnixServer", codexService.project)
	assert.Equal(t, "pending", codexService.reviewQueueStatus)
	assert.Equal(t, 25, codexService.reviewQueueLimit)

	var response codexTaskSessionsResponse
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	if assert.Len(t, response.Data, 1) {
		assert.Equal(t, "codex:local:thread-review:turn-1", response.Data[0].ExternalKey)
		assert.Equal(t, "needs_review", response.Data[0].ReviewStatus)
		assert.Equal(t, "Codex aktivnost zahtijeva ručni pregled.", response.Data[0].Summary)
	}

	userService.AssertExpectations(t)
}

func TestCodexTaskApiHandler_PatchReviewQueue(t *testing.T) {
	config.Set(config.Empty())

	user := &models.User{ID: "user", ApiKey: "apikey"}
	userService := new(mocks.UserServiceMock)
	userService.On("GetUserByKey", user.ApiKey, true).Return(user, nil)

	codexService := &stubCodexTaskService{}
	handler := NewCodexTaskApiHandler(userService, codexService)

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	body := []byte(`{
		"action": "edit",
		"client_message_hr": "Ručno uređen sažetak za klijenta.",
		"internal_message_hr": "Ručno odobreno nakon pregleda."
	}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/integrations/codex/review-queue/codex:local:thread-review:turn-1", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, codexService.lastReviewInput)
	assert.Equal(t, "edit", codexService.lastReviewInput.Action)
	assert.Equal(t, "codex:local:thread-review:turn-1", codexService.lastReviewExternal)
	assert.Equal(t, "Ručno uređen sažetak za klijenta.", codexService.lastReviewInput.ClientMessageHR)

	var response codexTaskSessionResponse
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, "approved", response.ReviewStatus)
	assert.Equal(t, "Ručno uređen sažetak za klijenta.", response.ClientMessageHR)
	assert.Equal(t, "Ručno uređen sažetak za klijenta.", response.Summary)

	userService.AssertExpectations(t)
}
