package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/repositories"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

type fakePullRepo struct {
	repositories.IScmPullRequestRepository
	stored   map[int]*models.ScmPullRequest
	upserted [][]int
}

func (f *fakePullRepo) GetLatestUpdatedByRepo(repoID string) (*models.ScmPullRequest, error) {
	var latest *models.ScmPullRequest
	for _, pr := range f.stored {
		if latest == nil || pr.ProviderUpdatedAt.T().After(latest.ProviderUpdatedAt.T()) {
			latest = pr
		}
	}
	if latest == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return latest, nil
}

func (f *fakePullRepo) UpsertMany(prs []*models.ScmPullRequest) error {
	numbers := []int{}
	for _, pr := range prs {
		f.stored[pr.Number] = pr
		numbers = append(numbers, pr.Number)
	}
	f.upserted = append(f.upserted, numbers)
	return nil
}

func (f *fakePullRepo) GetByRepoUpdatedSince(repoID string, since time.Time) ([]*models.ScmPullRequest, error) {
	out := []*models.ScmPullRequest{}
	for _, pr := range f.stored {
		if pr.RepositoryID == repoID && !pr.ProviderUpdatedAt.T().Before(since) {
			out = append(out, pr)
		}
	}
	return out, nil
}

// githubPullsServer serves the given pull requests sorted as GitHub does (most recently updated first).
func githubPullsServer(t *testing.T, pulls []map[string]any, pages *[]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/acme/app/pulls", r.URL.Path)
		assert.Equal(t, "all", r.URL.Query().Get("state"))
		assert.Equal(t, "updated", r.URL.Query().Get("sort"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		*pages = append(*pages, page)
		start := min((page-1)*perPage, len(pulls))
		end := min(start+perPage, len(pulls))
		_ = json.NewEncoder(w).Encode(pulls[start:end])
	}))
}

func ghPull(number int, updated time.Time, merged *time.Time) map[string]any {
	pr := map[string]any{
		"number": number, "title": fmt.Sprintf("PR %d", number), "state": "open",
		"html_url":   fmt.Sprintf("https://github.com/acme/app/pull/%d", number),
		"created_at": updated.Add(-time.Hour), "updated_at": updated,
		"head": map[string]any{"ref": fmt.Sprintf("feature/%d", number), "sha": "h"},
		"base": map[string]any{"ref": "master"},
		"user": map[string]any{"login": "igor"},
	}
	if merged != nil {
		pr["state"] = "closed"
		pr["merged_at"] = *merged
		pr["closed_at"] = *merged
		pr["merge_commit_sha"] = "m"
	}
	return pr
}

func TestCommitService_SyncPulls_StopsAtWatermark(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	merged := base.Add(-time.Hour)
	pulls := []map[string]any{
		ghPull(5, base, &merged),
		ghPull(4, base.Add(-2*time.Hour), nil),
		ghPull(3, base.Add(-3*time.Hour), nil), // same second as the stored watermark
		ghPull(2, base.Add(-4*time.Hour), nil), // older than the watermark: not fetched again
		ghPull(1, base.Add(-5*time.Hour), nil),
	}
	var pages []int
	server := githubPullsServer(t, pulls, &pages)
	defer server.Close()

	repo := &fakePullRepo{stored: map[int]*models.ScmPullRequest{
		3: {Number: 3, RepositoryID: "r1", ProviderUpdatedAt: models.CustomTime(base.Add(-3 * time.Hour))},
	}}
	sut := &CommitService{pulls: repo}
	client := NewGitHubClient("token")
	client.baseURL = server.URL

	assert.NoError(t, sut.syncPulls(client, &models.ScmRepository{ID: "r1", FullName: "acme/app"}))

	assert.Equal(t, [][]int{{5, 4, 3}}, repo.upserted)
	assert.Equal(t, []int{1}, pages)
	assert.Equal(t, "feature/5", repo.stored[5].HeadRef)
	assert.Equal(t, "master", repo.stored[5].BaseRef)
	assert.Equal(t, merged, repo.stored[5].MergedAt.T().UTC())
	assert.Nil(t, repo.stored[4].MergedAt)
}

func TestCommitService_SyncPulls_FirstSyncPagesThroughAll(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pulls := []map[string]any{}
	for i := 150; i >= 1; i-- {
		pulls = append(pulls, ghPull(i, base.Add(-time.Duration(150-i)*time.Minute), nil))
	}
	var pages []int
	server := githubPullsServer(t, pulls, &pages)
	defer server.Close()

	repo := &fakePullRepo{stored: map[int]*models.ScmPullRequest{}}
	sut := &CommitService{pulls: repo}
	client := NewGitHubClient("token")
	client.baseURL = server.URL

	assert.NoError(t, sut.syncPulls(client, &models.ScmRepository{ID: "r1", FullName: "acme/app"}))

	assert.Equal(t, []int{1, 2}, pages)
	assert.Len(t, repo.stored, 150)
}

type fakeLinkRepo struct {
	repositories.IProjectRepositoryLinkRepository
	links []*models.ProjectRepositoryLink
}

func (f *fakeLinkRepo) ListByUser(userID string) ([]*models.ProjectRepositoryLink, error) {
	return f.links, nil
}

type fakeRepoRepo struct {
	repositories.IScmRepositoryRepository
	repos map[string]*models.ScmRepository
}

func (f *fakeRepoRepo) GetByID(id string) (*models.ScmRepository, error) {
	if repo, ok := f.repos[id]; ok {
		return repo, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func TestCommitService_GetPullRequests_ByProjectAndUpdate(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pr := func(number int, repoID string, updated time.Time) *models.ScmPullRequest {
		return &models.ScmPullRequest{Number: number, RepositoryID: repoID, ProviderUpdatedAt: models.CustomTime(updated)}
	}
	sut := &CommitService{
		links: &fakeLinkRepo{links: []*models.ProjectRepositoryLink{
			{Project: "app", RepositoryID: "r1"},
			{Project: "web", RepositoryID: "r2"},
			{Project: "gone", RepositoryID: "missing"},
		}},
		repos: &fakeRepoRepo{repos: map[string]*models.ScmRepository{
			"r1": {ID: "r1", FullName: "acme/app"},
			"r2": {ID: "r2", FullName: "acme/web"},
		}},
		pulls: &fakePullRepo{stored: map[int]*models.ScmPullRequest{
			1: pr(1, "r1", base.Add(-48*time.Hour)),
			2: pr(2, "r1", base),
			3: pr(3, "r2", base.Add(-time.Hour)),
		}},
	}
	user := &models.User{ID: "u1"}

	all, err := sut.GetPullRequests(user, "", base.Add(-24*time.Hour))
	assert.NoError(t, err)
	assert.Len(t, all, 2)
	// oldest update first, each with its project
	assert.Equal(t, 3, all[0].PullRequest.Number)
	assert.Equal(t, "web", all[0].Project)
	assert.Equal(t, 2, all[1].PullRequest.Number)
	assert.Equal(t, "acme/app", all[1].Repo.FullName)

	app, err := sut.GetPullRequests(user, "app", time.Time{})
	assert.NoError(t, err)
	assert.Len(t, app, 2)
	assert.Equal(t, 1, app[0].PullRequest.Number)
}
