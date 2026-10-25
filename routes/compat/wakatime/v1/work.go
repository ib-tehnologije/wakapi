package v1

import (
	"fmt"
	"math"
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

const maxBranchActivityDays = 366

// WorkHandler serves the data billing needs to bill work by branch: time per branch and day, and
// the pull requests that tell when a branch reached the default branch.
type WorkHandler struct {
	userSrvc     services.IUserService
	durationSrvc services.IDurationService
	pullSrvc     services.IPullRequestService
}

func NewWorkHandler(userService services.IUserService, durationService services.IDurationService, pullRequestService services.IPullRequestService) *WorkHandler {
	return &WorkHandler{userSrvc: userService, durationSrvc: durationService, pullSrvc: pullRequestService}
}

func (h *WorkHandler) RegisterRoutes(router chi.Router) {
	router.Group(func(r chi.Router) {
		r.Use(middlewares.NewAuthenticateMiddleware(h.userSrvc).Handler)
		r.Get("/compat/wakatime/v1/users/{user}/branch-activity", h.GetBranchActivity)
		r.Get("/compat/wakatime/v1/users/{user}/pull-requests", h.GetPullRequests)
	})
}

type branchActivityItem struct {
	Project string  `json:"project"`
	Date    string  `json:"date"`
	Branch  string  `json:"branch"`
	Seconds float64 `json:"seconds"`
	FirstAt string  `json:"first_at"`
	LastAt  string  `json:"last_at"`
}

type branchActivityResponse struct {
	Data     []*branchActivityItem `json:"data"`
	Start    string                `json:"start"`
	End      string                `json:"end"`
	Timezone string                `json:"timezone"`
}

// GetBranchActivity returns the time per project, local day and branch.
// Query: start and end (YYYY-MM-DD, both inclusive), optional timezone (IANA name, defaults to the
// user's) and project.
func (h *WorkHandler) GetBranchActivity(w http.ResponseWriter, r *http.Request) {
	user, err := routeutils.CheckEffectiveUser(w, r, h.userSrvc, "current")
	if err != nil {
		return
	}
	q := r.URL.Query()

	tz := user.TZ()
	if name := strings.TrimSpace(q.Get("timezone")); name != "" {
		loc, err := time.LoadLocation(name)
		if err != nil {
			respondBadRequest(w, "invalid timezone")
			return
		}
		tz = loc
	}

	start, err := time.ParseInLocation(conf.SimpleDateFormat, q.Get("start"), tz)
	if err != nil {
		respondBadRequest(w, "invalid start, expected YYYY-MM-DD")
		return
	}
	end, err := time.ParseInLocation(conf.SimpleDateFormat, q.Get("end"), tz)
	if err != nil {
		respondBadRequest(w, "invalid end, expected YYYY-MM-DD")
		return
	}
	if end.Before(start) {
		respondBadRequest(w, "start must be before or equal to end")
		return
	}
	if end.Sub(start) > maxBranchActivityDays*24*time.Hour {
		respondBadRequest(w, fmt.Sprintf("range must not exceed %d days", maxBranchActivityDays))
		return
	}
	end = datetime.EndOfDay(end)

	var filters *models.Filters
	if project := strings.TrimSpace(q.Get("project")); project != "" {
		filters = &models.Filters{Project: models.OrFilter{project}}
	}

	durations, err := h.durationSrvc.Get(start, end, user, filters, nil, false)
	if err != nil {
		conf.Log().Request(r).Error("failed to load durations for branch activity", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(conf.ErrInternalServerError))
		return
	}

	items := []*branchActivityItem{}
	for _, a := range services.AggregateBranchActivity(durations, start, end, tz) {
		items = append(items, &branchActivityItem{
			Project: a.Project,
			Date:    a.Date,
			Branch:  a.Branch,
			Seconds: math.Round(a.Seconds*100) / 100,
			FirstAt: a.FirstAt.UTC().Format(time.RFC3339),
			LastAt:  a.LastAt.UTC().Format(time.RFC3339),
		})
	}
	helpers.RespondJSON(w, r, http.StatusOK, &branchActivityResponse{
		Data:     items,
		Start:    start.Format(conf.SimpleDateFormat),
		End:      end.Format(conf.SimpleDateFormat),
		Timezone: tz.String(),
	})
}

type pullRequestItem struct {
	Project        string  `json:"project"`
	Repository     string  `json:"repository"`
	DefaultBranch  string  `json:"default_branch"`
	Number         int     `json:"number"`
	Title          string  `json:"title"`
	HTMLURL        string  `json:"html_url"`
	State          string  `json:"state"`
	HeadRef        string  `json:"head_ref"`
	HeadSHA        string  `json:"head_sha"`
	BaseRef        string  `json:"base_ref"`
	AuthorLogin    string  `json:"author_login"`
	OpenedAt       string  `json:"opened_at"`
	ClosedAt       *string `json:"closed_at"`
	MergedAt       *string `json:"merged_at"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
	UpdatedAt      string  `json:"updated_at"`
}

type pullRequestsResponse struct {
	Data []*pullRequestItem `json:"data"`
}

// GetPullRequests returns the pull requests of the user's linked repositories, oldest update first.
// Query: optional updated_since (RFC 3339 or YYYY-MM-DD) and project.
func (h *WorkHandler) GetPullRequests(w http.ResponseWriter, r *http.Request) {
	user, err := routeutils.CheckEffectiveUser(w, r, h.userSrvc, "current")
	if err != nil {
		return
	}
	q := r.URL.Query()

	var since time.Time
	if raw := strings.TrimSpace(q.Get("updated_since")); raw != "" {
		// restore '+' that may have been decoded to space
		parsed, err := helpers.ParseDateTimeTZ(strings.Replace(raw, " ", "+", 1), user.TZ())
		if err != nil {
			respondBadRequest(w, "invalid updated_since")
			return
		}
		since = parsed
	}

	prs, err := h.pullSrvc.GetPullRequests(user, strings.TrimSpace(q.Get("project")), since)
	if err != nil {
		conf.Log().Request(r).Error("failed to load pull requests", "error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(conf.ErrInternalServerError))
		return
	}

	formatOptional := func(t *models.CustomTime) *string {
		if t == nil || t.T().IsZero() {
			return nil
		}
		s := t.T().UTC().Format(time.RFC3339)
		return &s
	}
	items := make([]*pullRequestItem, 0, len(prs))
	for _, info := range prs {
		pr := info.PullRequest
		items = append(items, &pullRequestItem{
			Project:        info.Project,
			Repository:     info.Repo.FullName,
			DefaultBranch:  info.Repo.DefaultBranch,
			Number:         pr.Number,
			Title:          pr.Title,
			HTMLURL:        pr.HTMLURL,
			State:          pr.State,
			HeadRef:        pr.HeadRef,
			HeadSHA:        pr.HeadSHA,
			BaseRef:        pr.BaseRef,
			AuthorLogin:    pr.AuthorLogin,
			OpenedAt:       pr.OpenedAt.T().UTC().Format(time.RFC3339),
			ClosedAt:       formatOptional(pr.ClosedAt),
			MergedAt:       formatOptional(pr.MergedAt),
			MergeCommitSHA: pr.MergeCommitSHA,
			UpdatedAt:      pr.ProviderUpdatedAt.T().UTC().Format(time.RFC3339),
		})
	}
	helpers.RespondJSON(w, r, http.StatusOK, &pullRequestsResponse{Data: items})
}

func respondBadRequest(w http.ResponseWriter, message string) {
	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte(message))
}
