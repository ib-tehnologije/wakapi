package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/helpers"
	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/repositories"
)

const (
	defaultCommitPageSize = 50
	linkSyncStaleAfter    = 15 * time.Minute
	lookbackWindow        = 30 * 24 * time.Hour
)

type CommitsResult struct {
	Link    *models.ProjectRepositoryLink
	Repo    *models.ScmRepository
	Stats   []*models.CommitStat
	Commits map[string]*models.ScmCommit
	Branch  string
	Total   int64
	Page    int
}

type CommitResult struct {
	Link   *models.ProjectRepositoryLink
	Repo   *models.ScmRepository
	Stat   *models.CommitStat
	Commit *models.ScmCommit
	Branch string
}

type CommitService struct {
	config      *config.Config
	accounts    repositories.IScmAccountRepository
	repos       repositories.IScmRepositoryRepository
	links       repositories.IProjectRepositoryLinkRepository
	commits     repositories.IScmCommitRepository
	stats       repositories.ICommitStatRepository
	userService IUserService
	heartbeats  IHeartbeatService
	durations   IDurationService
}

func NewCommitService(
	accountRepo repositories.IScmAccountRepository,
	repoRepo repositories.IScmRepositoryRepository,
	linkRepo repositories.IProjectRepositoryLinkRepository,
	commitRepo repositories.IScmCommitRepository,
	statRepo repositories.ICommitStatRepository,
	userService IUserService,
	heartbeatService IHeartbeatService,
	durationService IDurationService,
) *CommitService {
	return &CommitService{
		config:      config.Get(),
		accounts:    accountRepo,
		repos:       repoRepo,
		links:       linkRepo,
		commits:     commitRepo,
		stats:       statRepo,
		userService: userService,
		heartbeats:  heartbeatService,
		durations:   durationService,
	}
}

// LinkProject links a Wakapi project to a GitHub repo using a PAT (fine-grained, contents read scope).
func (s *CommitService) LinkProject(user *models.User, project, fullName, pat, branchOverride string) (*models.ProjectRepositoryLink, error) {
	if fullName == "" || pat == "" {
		return nil, errors.New("repository full name and token are required")
	}

	cipher := newTokenCipher()
	encToken, err := cipher.Encrypt(pat)
	if err != nil {
		return nil, err
	}

	client := NewGitHubClient(pat)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo, err := client.GetRepo(ctx, fullName)
	if err != nil {
		return nil, err
	}

	repoModel := &models.ScmRepository{
		ID:            uuid.Must(uuid.NewV4()).String(),
		Provider:      models.ScmProviderGithub,
		ExternalID:    fmt.Sprintf("%d", repo.ID),
		FullName:      repo.FullName,
		Name:          repo.Name,
		Owner:         repo.Owner.Login,
		HTMLURL:       repo.HTMLURL,
		APIURL:        repo.URL,
		Description:   repo.Description,
		Homepage:      repo.Homepage,
		DefaultBranch: repo.DefaultBranch,
		IsPrivate:     repo.Private,
		IsFork:        repo.Fork,
		StarCount:     repo.Stargazers,
		ForkCount:     repo.Forks,
		WatchCount:    repo.Watchers,
	}
	if err := s.repos.Upsert(repoModel); err != nil {
		return nil, err
	}

	account := &models.ScmAccount{
		ID:             uuid.Must(uuid.NewV4()).String(),
		UserID:         user.ID,
		Provider:       models.ScmProviderGithub,
		AuthType:       models.ScmAuthTypePat,
		AccessTokenEnc: encToken,
		ProviderLogin:  repo.Owner.Login,
	}
	if err := s.accounts.Upsert(account); err != nil {
		return nil, err
	}

	link := &models.ProjectRepositoryLink{
		ID:             uuid.Must(uuid.NewV4()).String(),
		UserID:         user.ID,
		Project:        project,
		RepositoryID:   repoModel.ID,
		BranchOverride: branchOverride,
		SyncStatus:     "pending",
	}
	if err := s.links.Upsert(link); err != nil {
		return nil, err
	}

	// trigger initial sync
	if err := s.Sync(link, account, repoModel); err != nil {
		slog.Warn("initial sync failed", "error", err)
	}

	return link, nil
}

// Sync fetches commits for the link's branch and stores them.
func (s *CommitService) Sync(link *models.ProjectRepositoryLink, account *models.ScmAccount, repo *models.ScmRepository) error {
	token, err := newTokenCipher().Decrypt(account.AccessTokenEnc)
	if err != nil {
		return err
	}

	branch := link.BranchOverride
	if branch == "" {
		branch = repo.DefaultBranch
	}

	client := NewGitHubClient(token)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// determine cutoff hash to stop early
	var cutoffHash string
	if latest, err := s.commits.GetLatestByRepoAndBranch(repo.ID, branch); err == nil {
		cutoffHash = latest.Hash
	}

	page := 1
	perPage := 100
	collected := []*models.ScmCommit{}

	for {
		commits, err := client.ListCommits(ctx, repo.FullName, branch, "", page, perPage)
		if err != nil {
			return err
		}
		if len(commits) == 0 {
			break
		}
		for _, c := range commits {
			if cutoffHash != "" && c.SHA == cutoffHash {
				goto STORE
			}
			collected = append(collected, mapCommit(c, repo.ID, branch))
		}
		if len(commits) < perPage {
			break
		}
		page++
	}

STORE:
	if len(collected) > 0 {
		if err := s.commits.UpsertMany(collected); err != nil {
			return err
		}
		// compute stats for new commits (ascending by date)
		if err := s.computeStats(link.UserID, link.Project, repo, branch); err != nil {
			slog.Warn("failed to compute commit stats", "error", err)
		}
	}

	now := models.CustomTime(time.Now())
	link.LastSyncedAt = &now
	link.SyncStatus = "ok"
	link.SyncError = ""
	return s.links.Upsert(link)
}

func (s *CommitService) GetCommits(user *models.User, project, branch, author string, page, perPage int) (*CommitsResult, error) {
	link, repo, account, err := s.ensureLink(user, project)
	if err != nil {
		return nil, err
	}

	if branch == "" {
		branch = s.effectiveBranch(link, repo)
	}

	if link.LastSyncedAt == nil || time.Since(link.LastSyncedAt.T()) > linkSyncStaleAfter {
		if err := s.Sync(link, account, repo); err != nil {
			link.SyncStatus = "error"
			link.SyncError = err.Error()
			_ = s.links.Upsert(link)
			slog.Warn("sync before list failed", "error", err)
		}
	}

	stats, _, err := s.stats.GetByUserProjectBranch(user.ID, project, branch, 0, 0)
	if err != nil {
		return nil, err
	}

	filtered := make([]*models.CommitStat, 0, len(stats))
	for _, st := range stats {
		cmt, err := s.commits.GetByRepoAndHash(repo.ID, st.CommitHash)
		if err != nil {
			continue
		}
		if author != "" {
			if cmt.AuthorName != author && cmt.CommitterName != author && cmt.AuthorUsername != author && cmt.CommitterUsername != author {
				continue
			}
		}
		filtered = append(filtered, st)
	}

	total := int64(len(filtered))
	if perPage <= 0 {
		perPage = defaultCommitPageSize
	}
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * perPage
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + perPage
	if end > len(filtered) {
		end = len(filtered)
	}
	paged := filtered[start:end]

	commitMap := make(map[string]*models.ScmCommit, len(paged))
	for _, st := range paged {
		if c, err := s.commits.GetByRepoAndHash(repo.ID, st.CommitHash); err == nil {
			commitMap[st.CommitHash] = c
		}
	}

	return &CommitsResult{
		Link:    link,
		Repo:    repo,
		Stats:   paged,
		Commits: commitMap,
		Branch:  branch,
		Total:   total,
		Page:    page,
	}, nil
}

func (s *CommitService) GetCommit(user *models.User, project, branch, hash, author string) (*CommitResult, error) {
	link, repo, account, err := s.ensureLink(user, project)
	if err != nil {
		return nil, err
	}
	if branch == "" {
		branch = s.effectiveBranch(link, repo)
	}

	if link.LastSyncedAt == nil || time.Since(link.LastSyncedAt.T()) > linkSyncStaleAfter {
		_ = s.Sync(link, account, repo)
	}

	stat, err := s.stats.GetByUserProjectBranchAndHash(user.ID, project, branch, hash)
	if err != nil {
		return nil, err
	}
	commit, err := s.commits.GetByRepoAndHash(repo.ID, hash)
	if err != nil {
		return nil, err
	}
	return &CommitResult{
		Link:   link,
		Repo:   repo,
		Stat:   stat,
		Commit: commit,
		Branch: branch,
	}, nil
}

func (s *CommitService) ensureLink(user *models.User, project string) (*models.ProjectRepositoryLink, *models.ScmRepository, *models.ScmAccount, error) {
	link, err := s.links.GetByUserAndProject(user.ID, project)
	if err != nil {
		return nil, nil, nil, err
	}
	repo, err := s.repos.GetByID(link.RepositoryID)
	if err != nil {
		return nil, nil, nil, err
	}
	account, err := s.accounts.GetByUserAndProvider(user.ID, models.ScmProviderGithub)
	if err != nil {
		return nil, nil, nil, err
	}
	return link, repo, account, nil
}

func (s *CommitService) effectiveBranch(link *models.ProjectRepositoryLink, repo *models.ScmRepository) string {
	if link.BranchOverride != "" {
		return link.BranchOverride
	}
	return repo.DefaultBranch
}

func (s *CommitService) computeStats(userID, project string, repo *models.ScmRepository, branch string) error {
	commits, err := s.commits.GetByRepoAndBranchAfter(repo.ID, branch, time.Time{}, 0, 0)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		return nil
	}

	// sort ascending by committer date (just in case)
	slices.SortFunc(commits, func(a, b *models.ScmCommit) int {
		return a.CommitterDate.T().Compare(b.CommitterDate.T())
	})

	// Build filters
	filters := &models.Filters{Project: models.OrFilter{project}, Branch: models.OrFilter{branch}}

	var prev time.Time
	for i, c := range commits {
		end := c.CommitterDate.T()
		start := prev
		if i == 0 || start.IsZero() {
			start = end.Add(-lookbackWindow)
		}

		durs, err := s.durations.Get(start, end, &models.User{ID: userID}, filters, nil, true)
		if err != nil {
			return err
		}

		total := accumulateOverlap(durs, start, end)

		stat := &models.CommitStat{
			ID:                           uuid.Must(uuid.NewV4()).String(),
			UserID:                       userID,
			Project:                      project,
			RepositoryID:                 repo.ID,
			Branch:                       branch,
			CommitHash:                   c.Hash,
			TotalSeconds:                 total.Seconds(),
			HumanReadableTotal:           helpers.FmtWakatimeDuration(total),
			HumanReadableTotalWithSecond: helpers.FmtWakatimeDurationWithSeconds(total),
			AlgoVersion:                  models.CommitAlgoVersion,
			Dirty:                        false,
			CalculatedAt:                 models.CustomTime(time.Now()),
		}
		if err := s.stats.Upsert(stat); err != nil {
			return err
		}
		prev = end
	}
	return nil
}

func accumulateOverlap(durs models.Durations, start, end time.Time) time.Duration {
	var total time.Duration
	for _, d := range durs {
		ds := d.Time.T()
		de := d.TimeEnd()
		if de.Before(start) || ds.After(end) {
			continue
		}
		s := ds
		if s.Before(start) {
			s = start
		}
		e := de
		if e.After(end) {
			e = end
		}
		if e.After(s) {
			total += e.Sub(s)
		}
	}
	return total
}

func mapCommit(c *githubCommit, repoID, branch string) *models.ScmCommit {
	now := models.CustomTime(time.Now())
	return &models.ScmCommit{
		ID:                 uuid.Must(uuid.NewV4()).String(),
		RepositoryID:       repoID,
		Hash:               c.SHA,
		TruncatedHash:      truncateHash(c.SHA),
		Message:            c.Commit.Message,
		HTMLURL:            c.HTML,
		URL:                c.Commit.URL,
		AuthorName:         c.Commit.Author.Name,
		AuthorEmail:        c.Commit.Author.Email,
		AuthorDate:         models.CustomTime(c.Commit.Author.Date),
		AuthorUsername:     c.Author.Login,
		AuthorAvatarURL:    c.Author.AvatarURL,
		AuthorHTMLURL:      c.Author.HTMLURL,
		AuthorURL:          c.Author.URL,
		CommitterName:      c.Commit.Committer.Name,
		CommitterEmail:     c.Commit.Committer.Email,
		CommitterDate:      models.CustomTime(c.Commit.Committer.Date),
		CommitterUsername:  c.Committer.Login,
		CommitterAvatarURL: c.Committer.AvatarURL,
		CommitterHTMLURL:   c.Committer.HTMLURL,
		CommitterURL:       c.Committer.URL,
		Ref:                fmt.Sprintf("refs/heads/%s", branch),
		Branch:             branch,
		CreatedAt:          now,
	}
}

func truncateHash(hash string) string {
	if len(hash) <= 7 {
		return hash
	}
	return hash[:7]
}
