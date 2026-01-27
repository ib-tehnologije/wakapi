package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	conf "github.com/muety/wakapi/config"
	"github.com/muety/wakapi/helpers"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/services"
)

type GitHubIntegrationHandler struct {
	userSrvc   services.IUserService
	commitSrvc services.ICommitService
}

func NewGitHubIntegrationHandler(userService services.IUserService, commitService services.ICommitService) *GitHubIntegrationHandler {
	return &GitHubIntegrationHandler{
		userSrvc:   userService,
		commitSrvc: commitService,
	}
}

func (h *GitHubIntegrationHandler) RegisterRoutes(router chi.Router) {
	r := chi.NewRouter()
	r.Use(middlewares.NewAuthenticateMiddleware(h.userSrvc).Handler)

	r.Post("/pat", h.PostPAT)
	r.Get("/repos", h.GetRepos)
	r.Post("/links", h.PostLink)
	r.Put("/links/{id}", h.PutLink)
	r.Delete("/links/{id}", h.DeleteLink)
	r.Post("/links/{id}/sync", h.PostSync)

	router.Mount("/integrations/github", r)
}

type patRequest struct {
	Token string `json:"token"`
}

func (h *GitHubIntegrationHandler) PostPAT(w http.ResponseWriter, r *http.Request) {
	user := middlewares.GetPrincipal(r)

	var body patRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid request body"))
		return
	}
	if body.Token == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("token is required"))
		return
	}

	if err := h.commitSrvc.UpdateToken(user, body.Token); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("failed to store token"))
		conf.Log().Request(r).Warn("failed to store github token", "user", user.ID, "error", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *GitHubIntegrationHandler) GetRepos(w http.ResponseWriter, r *http.Request) {
	user := middlewares.GetPrincipal(r)

	q := r.URL.Query()
	search := q.Get("search")
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("per_page"))

	repos, err := h.commitSrvc.ListRepos(user, search, page, perPage)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("failed to list repositories"))
		conf.Log().Request(r).Warn("failed to list github repos", "user", user.ID, "error", err)
		return
	}

	type repoResponse struct {
		ID            string `json:"id"`
		FullName      string `json:"full_name"`
		HTMLURL       string `json:"html_url"`
		DefaultBranch string `json:"default_branch"`
		Visibility    string `json:"visibility"`
		Owner         string `json:"owner"`
	}

	resp := make([]*repoResponse, 0, len(repos))
	for _, repo := range repos {
		vis := "public"
		if repo.IsPrivate {
			vis = "private"
		}
		resp = append(resp, &repoResponse{
			ID:            repo.ExternalID,
			FullName:      repo.FullName,
			HTMLURL:       repo.HTMLURL,
			DefaultBranch: repo.DefaultBranch,
			Visibility:    vis,
			Owner:         repo.Owner,
		})
	}

	helpers.RespondJSON(w, r, http.StatusOK, resp)
}

type linkRequest struct {
	Project      string `json:"project"`
	RepositoryID string `json:"repository_id"`
	Branch       string `json:"branch_override"`
}

func (h *GitHubIntegrationHandler) PostLink(w http.ResponseWriter, r *http.Request) {
	user := middlewares.GetPrincipal(r)

	var body linkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid request body"))
		return
	}
	if body.Project == "" || body.RepositoryID == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("project and repository_id are required"))
		return
	}

	link, err := h.commitSrvc.LinkProjectWithRepo(user, body.Project, body.RepositoryID, body.Branch)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("failed to link repository"))
		conf.Log().Request(r).Warn("failed to link github repo", "user", user.ID, "project", body.Project, "repo", body.RepositoryID, "error", err)
		return
	}

	helpers.RespondJSON(w, r, http.StatusCreated, link)
}

type updateLinkRequest struct {
	Branch       string `json:"branch_override"`
	RepositoryID string `json:"repository_id"`
}

func (h *GitHubIntegrationHandler) PutLink(w http.ResponseWriter, r *http.Request) {
	user := middlewares.GetPrincipal(r)
	id := chi.URLParam(r, "id")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("id is required"))
		return
	}

	var body updateLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid request body"))
		return
	}

	if err := h.commitSrvc.UpdateLinkByID(user, id, body.Branch, body.RepositoryID); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("failed to update link"))
		conf.Log().Request(r).Warn("failed to update github link", "user", user.ID, "link", id, "error", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *GitHubIntegrationHandler) DeleteLink(w http.ResponseWriter, r *http.Request) {
	user := middlewares.GetPrincipal(r)
	id := chi.URLParam(r, "id")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("id is required"))
		return
	}
	purge := r.URL.Query().Get("purge") == "true"

	if err := h.commitSrvc.UnlinkByID(user, id, purge); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("failed to delete link"))
		conf.Log().Request(r).Warn("failed to delete github link", "user", user.ID, "link", id, "error", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *GitHubIntegrationHandler) PostSync(w http.ResponseWriter, r *http.Request) {
	user := middlewares.GetPrincipal(r)
	id := chi.URLParam(r, "id")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("id is required"))
		return
	}

	go func() {
		if err := h.commitSrvc.SyncByID(user, id); err != nil {
			conf.Log().Request(r).Warn("manual sync failed", "user", user.ID, "link", id, "error", err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
}
