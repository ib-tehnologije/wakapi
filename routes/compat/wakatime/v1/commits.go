package v1

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	conf "github.com/muety/wakapi/config"
	"github.com/muety/wakapi/helpers"
	"github.com/muety/wakapi/middlewares"
	"github.com/muety/wakapi/models"
	wv1 "github.com/muety/wakapi/models/compat/wakatime/v1"
	routeutils "github.com/muety/wakapi/routes/utils"
	"github.com/muety/wakapi/services"
	"gorm.io/gorm"
)

type CommitsHandler struct {
	userSrvc   services.IUserService
	commitSrvc services.ICommitService
}

const defaultCommitPageSize = 50

func NewCommitsHandler(userService services.IUserService, commitService services.ICommitService) *CommitsHandler {
	return &CommitsHandler{userSrvc: userService, commitSrvc: commitService}
}

func (h *CommitsHandler) RegisterRoutes(router chi.Router) {
	router.Group(func(r chi.Router) {
		r.Use(middlewares.NewAuthenticateMiddleware(h.userSrvc).Handler)
		r.Get("/compat/wakatime/v1/users/{user}/projects/{project}/commits", h.GetMany)
		r.Get("/compat/wakatime/v1/users/{user}/projects/{project}/commits/{hash}", h.GetOne)
		r.Post("/compat/wakatime/v1/users/{user}/projects/{project}/link", h.LinkProject)
	})
}

func (h *CommitsHandler) GetMany(w http.ResponseWriter, r *http.Request) {
	user, err := routeutils.CheckEffectiveUser(w, r, h.userSrvc, "current")
	if err != nil {
		return
	}

	project := chi.URLParam(r, "project")
	q := r.URL.Query()
	branch := q.Get("branch")
	author := q.Get("author")
	page, _ := strconv.Atoi(q.Get("page"))

	res, err := h.commitSrvc.GetCommits(user, project, branch, author, page, 0)
	if err != nil {
		status := http.StatusInternalServerError
		body := conf.ErrInternalServerError
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
			body = conf.ErrNotFound
		} else {
			conf.Log().Request(r).Error("error occurred", "error", err.Error())
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
		return
	}

	vm := toCommitsResponse(r, res, author)
	helpers.RespondJSON(w, r, http.StatusOK, vm)
}

func (h *CommitsHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	user, err := routeutils.CheckEffectiveUser(w, r, h.userSrvc, "current")
	if err != nil {
		return
	}

	project := chi.URLParam(r, "project")
	hash := chi.URLParam(r, "hash")
	branch := r.URL.Query().Get("branch")

	res, err := h.commitSrvc.GetCommit(user, project, branch, hash, "")
	if err != nil {
		status := http.StatusNotFound
		body := conf.ErrNotFound
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusInternalServerError
			body = conf.ErrInternalServerError
			conf.Log().Request(r).Error("error occurred", "error", err.Error())
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
		return
	}

	vm := toCommitResponse(res)
	helpers.RespondJSON(w, r, http.StatusOK, vm)
}

func toCommitsResponse(r *http.Request, res *services.CommitsResult, author string) *wv1.CommitsResponse {
	perPage := defaultCommitPageSize
	totalPages := int(math.Ceil(float64(res.Total) / float64(perPage)))
	if totalPages == 0 {
		totalPages = 1
	}
	nextPage := optionalPage(res.Page+1, totalPages)
	prevPage := optionalPage(res.Page-1, totalPages)

	var nextURL, prevURL *string
	if nextPage != nil {
		u := withPage(r.URL, *nextPage)
		s := u.String()
		nextURL = &s
	}
	if prevPage != nil {
		u := withPage(r.URL, *prevPage)
		s := u.String()
		prevURL = &s
	}

	commits := make([]*wv1.Commit, 0, len(res.Stats))
	for _, st := range res.Stats {
		commit := res.Commits[st.CommitHash]
		if commit == nil {
			continue
		}
		commits = append(commits, toCommitVM(commit, st, res.Branch))
	}

	var authorPtr *string
	if author != "" {
		authorPtr = &author
	}

	return &wv1.CommitsResponse{
		Commits:     commits,
		Author:      authorPtr,
		NextPage:    nextPage,
		NextPageURL: nextURL,
		Page:        res.Page,
		PrevPage:    prevPage,
		PrevPageURL: prevURL,
		Branch:      res.Branch,
		Project:     toProjectVM(res.Repo, res.Link),
		Status:      res.Link.SyncStatus,
		Total:       int(res.Total),
		TotalPages:  totalPages,
	}
}

type linkProjectRequest struct {
	Repository string `json:"repository"`
	Token      string `json:"token"`
	Branch     string `json:"branch"`
}

type linkProjectResponse struct {
	Project    string `json:"project"`
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
	Status     string `json:"status"`
}

// LinkProject lets a user attach a Wakapi project to a GitHub repository using a PAT (read-only contents).
// This is not part of the WakaTime API, but provides a minimal way to enable commit syncing in Wakapi.
func (h *CommitsHandler) LinkProject(w http.ResponseWriter, r *http.Request) {
	user, err := routeutils.CheckEffectiveUser(w, r, h.userSrvc, "current")
	if err != nil {
		return
	}

	project := chi.URLParam(r, "project")

	var body linkProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid request body"))
		return
	}

	body.Repository = strings.TrimSpace(body.Repository)
	body.Token = strings.TrimSpace(body.Token)
	body.Branch = strings.TrimSpace(body.Branch)

	if body.Repository == "" || body.Token == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("repository and token are required"))
		return
	}

	link, err := h.commitSrvc.LinkProject(user, project, body.Repository, body.Token, body.Branch)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("failed to link project to repository"))
		conf.Log().Request(r).Warn("failed to link project", "error", err, "project", project, "repo", body.Repository)
		return
	}

	resp := &linkProjectResponse{
		Project:    project,
		Repository: body.Repository,
		Branch:     link.BranchOverride,
		Status:     link.SyncStatus,
	}

	helpers.RespondJSON(w, r, http.StatusCreated, resp)
}

func toCommitResponse(res *services.CommitResult) *wv1.CommitResponse {
	return &wv1.CommitResponse{
		Commit:  toCommitVM(res.Commit, res.Stat, res.Branch),
		Branch:  res.Branch,
		Project: toProjectVM(res.Repo, res.Link),
		Status:  res.Link.SyncStatus,
	}
}

func toCommitVM(commit *models.ScmCommit, stat *models.CommitStat, branch string) *wv1.Commit {
	return &wv1.Commit{
		AuthorAvatarURL:              commit.AuthorAvatarURL,
		AuthorDate:                   commit.AuthorDate.T().Format(time.RFC3339),
		AuthorEmail:                  commit.AuthorEmail,
		AuthorHTMLURL:                commit.AuthorHTMLURL,
		AuthorName:                   commit.AuthorName,
		AuthorURL:                    commit.AuthorURL,
		AuthorUsername:               commit.AuthorUsername,
		CommitterAvatarURL:           commit.CommitterAvatarURL,
		CommitterDate:                commit.CommitterDate.T().Format(time.RFC3339),
		CommitterEmail:               commit.CommitterEmail,
		CommitterHTMLURL:             commit.CommitterHTMLURL,
		CommitterName:                commit.CommitterName,
		CommitterURL:                 commit.CommitterURL,
		CommitterUsername:            commit.CommitterUsername,
		CreatedAt:                    commit.CreatedAt.T().Format(time.RFC3339),
		Hash:                         commit.Hash,
		TruncatedHash:                commit.TruncatedHash,
		HTMLURL:                      commit.HTMLURL,
		HumanReadableTotal:           stat.HumanReadableTotal,
		HumanReadableTotalWithSecond: stat.HumanReadableTotalWithSecond,
		ID:                           commit.ID,
		Message:                      commit.Message,
		Ref:                          commit.Ref,
		TotalSeconds:                 stat.TotalSeconds,
		URL:                          commit.URL,
		Branch:                       branch,
	}
}

func toProjectVM(repo *models.ScmRepository, link *models.ProjectRepositoryLink) *wv1.CommitProject {
	privacy := "public"
	if repo.IsPrivate {
		privacy = "private"
	}
	return &wv1.CommitProject{
		ID:      link.Project,
		Name:    link.Project,
		Privacy: privacy,
		Repository: &wv1.Repository{
			ID:            repo.ID,
			Name:          repo.Name,
			FullName:      repo.FullName,
			Owner:         repo.Owner,
			HtmlURL:       repo.HTMLURL,
			URL:           repo.APIURL,
			Description:   repo.Description,
			Homepage:      repo.Homepage,
			DefaultBranch: repo.DefaultBranch,
			IsPrivate:     repo.IsPrivate,
			IsFork:        repo.IsFork,
			StarCount:     repo.StarCount,
			ForkCount:     repo.ForkCount,
			WatchCount:    repo.WatchCount,
		},
	}
}

func optionalPage(p, totalPages int) *int {
	if p < 1 || p > totalPages {
		return nil
	}
	return &p
}

func withPage(u *url.URL, page int) *url.URL {
	clone := *u
	q := clone.Query()
	q.Set("page", strconv.Itoa(page))
	clone.RawQuery = q.Encode()
	return &clone
}
