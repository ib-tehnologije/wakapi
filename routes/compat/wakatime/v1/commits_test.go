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
	wv1 "github.com/muety/wakapi/models/compat/wakatime/v1"
	"github.com/muety/wakapi/services"
	"gorm.io/gorm"
)

type stubCommitService struct {
	list *services.CommitsResult
	one  *services.CommitResult
	err  error
}

func (s *stubCommitService) LinkProject(*models.User, string, string, string, string) (*models.ProjectRepositoryLink, error) {
	return nil, nil
}
func (s *stubCommitService) GetCommits(*models.User, string, string, string, int, int) (*services.CommitsResult, error) {
	return s.list, s.err
}
func (s *stubCommitService) GetCommit(*models.User, string, string, string, string) (*services.CommitResult, error) {
	return s.one, s.err
}
func (s *stubCommitService) ListLinks(*models.User) ([]*services.ProjectLinkInfo, error) {
	return nil, nil
}
func (s *stubCommitService) Schedule()                                             {}
func (s *stubCommitService) UpdateLink(*models.User, string, string, string) error { return nil }
func (s *stubCommitService) UnlinkProject(*models.User, string, bool) error        { return nil }
func (s *stubCommitService) UpdateToken(*models.User, string) error                { return nil }
func (s *stubCommitService) SyncNow(*models.User, string) error                    { return nil }

func TestCommitsHandler_GetMany(t *testing.T) {
	config.Set(config.Empty())

	now := models.CustomTime(time.Now())
	commit := &models.ScmCommit{
		ID:             "cid",
		RepositoryID:   "repo1",
		Hash:           "abcdef123456",
		TruncatedHash:  "abcdef1",
		Message:        "feat: test",
		HTMLURL:        "http://example.com/c",
		URL:            "http://api.example.com/c",
		AuthorName:     "Alice",
		AuthorEmail:    "a@example.com",
		AuthorDate:     now,
		CommitterName:  "Alice",
		CommitterEmail: "a@example.com",
		CommitterDate:  now,
		Ref:            "refs/heads/main",
		Branch:         "main",
		CreatedAt:      now,
	}
	stat := &models.CommitStat{
		CommitHash:                   commit.Hash,
		TotalSeconds:                 120,
		HumanReadableTotal:           "0 hrs 2 mins",
		HumanReadableTotalWithSecond: "2 mins 0 secs",
	}
	repo := &models.ScmRepository{ID: "repo1", Name: "repo1", FullName: "alice/repo1", Owner: "alice", HTMLURL: "http://example.com/r", APIURL: "http://api.example.com/r", DefaultBranch: "main"}
	link := &models.ProjectRepositoryLink{Project: "proj", RepositoryID: "repo1", SyncStatus: "ok"}

	user := &models.User{ID: "user", ApiKey: "apikey"}

	handler := NewCommitsHandler(
		&mockUserService{user: user},
		&stubCommitService{
			list: &services.CommitsResult{
				Link:    link,
				Repo:    repo,
				Stats:   []*models.CommitStat{stat},
				Commits: map[string]*models.ScmCommit{commit.Hash: commit},
				Branch:  "main",
				Total:   1,
				Page:    1,
			},
		},
	)

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	req := httptest.NewRequest(http.MethodGet, "/api/compat/wakatime/v1/users/current/projects/proj/commits", nil)
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp wv1.CommitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(resp.Commits))
	}
	if resp.Commits[0].Hash != commit.Hash {
		t.Fatalf("unexpected hash %s", resp.Commits[0].Hash)
	}
	if resp.Total != 1 || resp.TotalPages != 1 {
		t.Fatalf("unexpected totals %d pages %d", resp.Total, resp.TotalPages)
	}
}

func TestCommitsHandler_GetMany_NotLinked(t *testing.T) {
	config.Set(config.Empty())

	user := &models.User{ID: "user", ApiKey: "apikey"}

	handler := NewCommitsHandler(
		&mockUserService{user: user},
		&stubCommitService{err: gorm.ErrRecordNotFound},
	)

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	req := httptest.NewRequest(http.MethodGet, "/api/compat/wakatime/v1/users/current/projects/unknown/commits", nil)
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// minimal user service mock implementing needed methods
type mockUserService struct {
	user *models.User
}

func (m *mockUserService) GetUserById(id string) (*models.User, error) { return m.user, nil }
func (m *mockUserService) GetUserByKey(key string, b bool) (*models.User, error) {
	if key == m.user.ApiKey {
		return m.user, nil
	}
	return nil, nil
}

// unused interface methods
func (m *mockUserService) GetUserByEmail(string) (*models.User, error)             { return nil, nil }
func (m *mockUserService) GetUserByResetToken(string) (*models.User, error)        { return nil, nil }
func (m *mockUserService) GetUserByUnsubscribeToken(string) (*models.User, error)  { return nil, nil }
func (m *mockUserService) GetUserByStripeCustomerId(string) (*models.User, error)  { return nil, nil }
func (m *mockUserService) GetUserByOidc(string, string) (*models.User, error)      { return nil, nil }
func (m *mockUserService) GetAll() ([]*models.User, error)                         { return nil, nil }
func (m *mockUserService) GetAllMapped() (map[string]*models.User, error)          { return nil, nil }
func (m *mockUserService) GetMany([]string) ([]*models.User, error)                { return nil, nil }
func (m *mockUserService) GetManyMapped([]string) (map[string]*models.User, error) { return nil, nil }
func (m *mockUserService) GetAllByReports(bool) ([]*models.User, error)            { return nil, nil }
func (m *mockUserService) GetAllByLeaderboard(bool) ([]*models.User, error)        { return nil, nil }
func (m *mockUserService) GetActive(bool) ([]*models.User, error)                  { return nil, nil }
func (m *mockUserService) Count() (int64, error)                                   { return 0, nil }
func (m *mockUserService) CountCurrentlyOnline() (int, error)                      { return 0, nil }
func (m *mockUserService) CreateOrGet(*models.Signup, bool) (*models.User, bool, error) {
	return nil, false, nil
}
func (m *mockUserService) Update(*models.User) (*models.User, error)               { return nil, nil }
func (m *mockUserService) Delete(*models.User) error                               { return nil }
func (m *mockUserService) ChangeUserId(*models.User, string) (*models.User, error) { return nil, nil }
func (m *mockUserService) ResetApiKey(*models.User) (*models.User, error)          { return nil, nil }
func (m *mockUserService) SetWakatimeApiCredentials(*models.User, string, string) (*models.User, error) {
	return nil, nil
}
func (m *mockUserService) GenerateResetToken(*models.User) (*models.User, error) { return nil, nil }
func (m *mockUserService) GenerateUnsubscribeToken(*models.User) (*models.User, error) {
	return nil, nil
}
func (m *mockUserService) FlushCache()           {}
func (m *mockUserService) FlushUserCache(string) {}
