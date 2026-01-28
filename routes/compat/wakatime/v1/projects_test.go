package v1

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/mock"

	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/mocks"
	"github.com/muety/wakapi/models"
	wv1 "github.com/muety/wakapi/models/compat/wakatime/v1"
	"github.com/muety/wakapi/services"
	"github.com/muety/wakapi/utils"
)

type listLinksCommitService struct {
	*stubCommitService
	links []*services.ProjectLinkInfo
}

func (s *listLinksCommitService) ListLinks(*models.User) ([]*services.ProjectLinkInfo, error) {
	return s.links, nil
}

func TestProjectsHandler_HasRepoFlag(t *testing.T) {
	config.Set(config.Empty())

	user := &models.User{ID: "user", ApiKey: "apikey"}

	now := models.CustomTime(time.Now())
	stats := []*models.ProjectStats{
		{Project: "proj-linked", First: now, Last: now},
		{Project: "proj-unlinked", First: now, Last: now},
	}

	hb := &mocks.HeartbeatServiceMock{}
	hb.On("GetUserProjectStats", user, mock.Anything, mock.Anything, (*utils.PageParams)(nil), false).Return(stats, nil)

	cs := &listLinksCommitService{
		stubCommitService: &stubCommitService{},
		links: []*services.ProjectLinkInfo{
			{Link: &models.ProjectRepositoryLink{Project: "proj-linked"}},
		},
	}

	handler := NewProjectsHandler(
		&mockUserService{user: user},
		hb,
		cs,
	)

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	handler.RegisterRoutes(apiRouter)
	router.Mount("/api", apiRouter)

	req := httptest.NewRequest(http.MethodGet, "/api/compat/wakatime/v1/users/current/projects", nil)
	req.Header.Set("Authorization", "Bearer "+base64.StdEncoding.EncodeToString([]byte(user.ApiKey)))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp wv1.ProjectsViewModel
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(resp.Data))
	}

	for _, p := range resp.Data {
		if p.ID == "proj-linked" && !p.HasRepo {
			t.Fatalf("expected proj-linked to have has_repo=true")
		}
		if p.ID == "proj-unlinked" && p.HasRepo {
			t.Fatalf("expected proj-unlinked to have has_repo=false")
		}
	}
}
