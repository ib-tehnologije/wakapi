package v1

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/services"
	"github.com/stretchr/testify/assert"
)

type stubWorkDurationService struct {
	services.IDurationService
	durations models.Durations
	from, to  time.Time
	filters   *models.Filters
}

func (s *stubWorkDurationService) Get(from, to time.Time, user *models.User, filters *models.Filters, customTimeout *time.Duration, skipCache bool) (models.Durations, error) {
	s.from, s.to, s.filters = from, to, filters
	return s.durations, nil
}

type stubPullRequestService struct {
	prs     []*services.PullRequestInfo
	project string
	since   time.Time
}

func (s *stubPullRequestService) GetPullRequests(user *models.User, project string, since time.Time) ([]*services.PullRequestInfo, error) {
	s.project, s.since = project, since
	return s.prs, nil
}

func serveWork(t *testing.T, handler *WorkHandler, user *models.User, url string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestWorkHandler_GetBranchActivity(t *testing.T) {
	config.Set(config.Empty())
	user := &models.User{ID: "user", ApiKey: "apikey", Location: "UTC"}
	tz, _ := time.LoadLocation("Europe/Zagreb")
	durations := &stubWorkDurationService{durations: models.Durations{
		{Project: "app", Branch: "feature/x", Time: models.CustomTime(time.Date(2026, 10, 7, 10, 0, 0, 0, tz)), Duration: 90 * time.Minute},
		{Project: "app", Branch: "master", Time: models.CustomTime(time.Date(2026, 10, 7, 12, 0, 0, 0, tz)), Duration: 30 * time.Minute},
	}}
	handler := NewWorkHandler(&mockUserService{user: user}, durations, &stubPullRequestService{})

	rec := serveWork(t, handler, user, "/api/compat/wakatime/v1/users/current/branch-activity?start=2026-10-07&end=2026-10-07&timezone=Europe/Zagreb&project=app")

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp branchActivityResponse
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "Europe/Zagreb", resp.Timezone)
	assert.Len(t, resp.Data, 2)
	assert.Equal(t, "feature/x", resp.Data[0].Branch)
	assert.Equal(t, float64(5400), resp.Data[0].Seconds)
	assert.Equal(t, "2026-10-07T08:00:00Z", resp.Data[0].FirstAt)
	assert.Equal(t, "master", resp.Data[1].Branch)
	assert.Equal(t, float64(1800), resp.Data[1].Seconds)
	// the whole local day was requested, for the given project only
	assert.Equal(t, time.Date(2026, 10, 7, 0, 0, 0, 0, tz), durations.from)
	assert.Equal(t, "2026-10-07 23:59:59", durations.to.In(tz).Format("2006-01-02 15:04:05"))
	assert.True(t, durations.filters.IsProjectDetails())
}

func TestWorkHandler_GetBranchActivity_RejectsBadInput(t *testing.T) {
	config.Set(config.Empty())
	user := &models.User{ID: "user", ApiKey: "apikey"}
	handler := NewWorkHandler(&mockUserService{user: user}, &stubWorkDurationService{}, &stubPullRequestService{})

	for _, query := range []string{
		"start=2026-10-07",
		"start=2026-10-08&end=2026-10-07",
		"start=2025-01-01&end=2026-10-07",
		"start=2026-10-07&end=2026-10-07&timezone=Mars/Base",
	} {
		rec := serveWork(t, handler, user, "/api/compat/wakatime/v1/users/current/branch-activity?"+query)
		assert.Equal(t, http.StatusBadRequest, rec.Code, query)
	}
}

func TestWorkHandler_GetPullRequests(t *testing.T) {
	config.Set(config.Empty())
	user := &models.User{ID: "user", ApiKey: "apikey", Location: "UTC"}
	merged := models.CustomTime(time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC))
	pulls := &stubPullRequestService{prs: []*services.PullRequestInfo{{
		Project: "app",
		Repo:    &models.ScmRepository{FullName: "acme/app", DefaultBranch: "master"},
		PullRequest: &models.ScmPullRequest{
			Number: 12, Title: "Uvoz IRA", HTMLURL: "https://github.com/acme/app/pull/12", State: "closed",
			HeadRef: "claude/ira-import", BaseRef: "master", MergedAt: &merged, MergeCommitSHA: "abc",
			OpenedAt:          models.CustomTime(time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)),
			ProviderUpdatedAt: merged,
		},
	}}}
	handler := NewWorkHandler(&mockUserService{user: user}, &stubWorkDurationService{}, pulls)

	rec := serveWork(t, handler, user, "/api/compat/wakatime/v1/users/current/pull-requests?updated_since=2026-10-01T00:00:00Z&project=app")

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp pullRequestsResponse
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	pr := resp.Data[0]
	assert.Equal(t, "acme/app", pr.Repository)
	assert.Equal(t, "master", pr.DefaultBranch)
	assert.Equal(t, "claude/ira-import", pr.HeadRef)
	assert.Equal(t, "2026-10-20T14:00:00Z", *pr.MergedAt)
	assert.Nil(t, pr.ClosedAt)
	assert.Equal(t, "app", pulls.project)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), pulls.since.UTC())
}
