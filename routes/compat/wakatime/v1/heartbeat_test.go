package v1

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/mocks"
	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/mock"
)

func TestHeartbeatHandler_Get_WithProjectFilter_UsesFilteredQuery(t *testing.T) {
	config.Set(config.Empty())

	user := &models.User{
		ID:     "filter-user",
		ApiKey: "filter-user-api-key",
	}

	router := chi.NewRouter()
	apiRouter := chi.NewRouter()
	apiRouter.Use(middlewares.NewSharedDataMiddleware())
	router.Mount("/api", apiRouter)

	userServiceMock := new(mocks.UserServiceMock)
	userServiceMock.On("GetUserById", user.ID).Return(user, nil)
	userServiceMock.On("GetUserByKey", user.ApiKey, false).Return(user, nil)

	heartbeatServiceMock := new(mocks.HeartbeatServiceMock)
	heartbeatServiceMock.
		On(
			"GetAllWithinByFilters",
			mock.Anything,
			mock.Anything,
			user,
			mock.MatchedBy(func(filters *models.Filters) bool {
				return filters != nil &&
					filters.Project.Exists() &&
					filters.Project.MatchAny("OnixServer")
			}),
		).
		Return([]*models.Heartbeat{
			{
				ID:        1,
				UserID:    user.ID,
				Entity:    "/Users/test/Projects/OnixServer/main.go",
				Project:   "OnixServer",
				Time:      models.CustomTime(time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)),
				CreatedAt: models.CustomTime(time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)),
			},
		}, nil).
		Once()

	handler := NewHeartbeatHandler(userServiceMock, heartbeatServiceMock)
	handler.RegisterRoutes(apiRouter)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/compat/wakatime/v1/users/{user}/heartbeats?date=2026-04-07&project=OnixServer",
		nil,
	)
	req = withUrlParam(req, "user", user.ID)
	req.Header.Add(
		"Authorization",
		fmt.Sprintf("Bearer %s", base64.StdEncoding.EncodeToString([]byte(user.ApiKey))),
	)

	router.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("unexpected error reading response body: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", res.StatusCode, string(body))
	}

	if !strings.Contains(string(body), "\"project\":\"OnixServer\"") {
		t.Fatalf("expected filtered heartbeat response, got: %s", string(body))
	}

	heartbeatServiceMock.AssertExpectations(t)
	heartbeatServiceMock.AssertNotCalled(t, "GetAllWithin", mock.Anything, mock.Anything, user)
}
