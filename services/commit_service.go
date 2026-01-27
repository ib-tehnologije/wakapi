package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/muety/artifex/v2"
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
	queue       *artifex.Dispatcher
	repoCache   map[string]repoCacheEntry
	repoCacheMu sync.RWMutex
}

// ProjectLinkInfo bundles a link with its repository metadata for UI consumption.
type ProjectLinkInfo struct {
	Link *models.ProjectRepositoryLink
	Repo *models.ScmRepository
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
		queue:       config.GetQueue(config.QueueDefault),
		repoCache:   make(map[string]repoCacheEntry),
	}
}

// LinkProject links a Wakapi project to a GitHub repo using an explicit token (PAT) or the user's stored SCM account token.
// fullName must be in the form owner/repo.
func (s *CommitService) LinkProject(user *models.User, project, fullName, token, branchOverride string) (*models.ProjectRepositoryLink, error) {
	if fullName == "" {
		return nil, errors.New("repository full name is required")
	}

	account, plainToken, err := s.resolveAccountToken(user, token)
	if err != nil {
		return nil, err
	}

	client := NewGitHubClient(plainToken)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo, err := client.GetRepo(ctx, fullName)
	if err != nil {
		return nil, err
	}

	repoModel := mapGitHubRepo(repo)
	if existing, err := s.repos.GetByExternalID(models.ScmProviderGithub, repoModel.ExternalID); err == nil {
		repoModel.ID = existing.ID
	}
	if err := s.repos.Upsert(repoModel); err != nil {
		return nil, err
	}

	account.ProviderLogin = repo.Owner.Login
	_ = s.accounts.Upsert(account)

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

	// trigger initial sync in background to avoid blocking UI
	go func() {
		if err := s.Sync(link, account, repoModel); err != nil {
			slog.Warn("initial sync failed", "error", err)
		}
	}()

	return link, nil
}

// LinkProjectWithRepo links using a stored SCM account and GitHub repository external id.
func (s *CommitService) LinkProjectWithRepo(user *models.User, project, repoExternalID, branchOverride string) (*models.ProjectRepositoryLink, error) {
	if repoExternalID == "" {
		return nil, errors.New("repository id is required")
	}

	account, plainToken, err := s.resolveAccountToken(user, "")
	if err != nil {
		return nil, err
	}

	client := NewGitHubClient(plainToken)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo, err := client.GetRepoByID(ctx, repoExternalID)
	if err != nil {
		return nil, err
	}

	repoModel := mapGitHubRepo(repo)
	if existing, err := s.repos.GetByExternalID(models.ScmProviderGithub, repoModel.ExternalID); err == nil {
		repoModel.ID = existing.ID
	}
	if err := s.repos.Upsert(repoModel); err != nil {
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

	go func() {
		if err := s.Sync(link, account, repoModel); err != nil {
			slog.Warn("initial sync failed", "error", err)
		}
	}()
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

// ListLinks returns the user's project↔repo links with repository metadata.
func (s *CommitService) ListLinks(user *models.User) ([]*ProjectLinkInfo, error) {
	links, err := s.links.ListByUser(user.ID)
	if err != nil {
		return nil, err
	}
	results := make([]*ProjectLinkInfo, 0, len(links))
	for _, l := range links {
		repo, err := s.repos.GetByID(l.RepositoryID)
		if err != nil {
			continue
		}
		results = append(results, &ProjectLinkInfo{Link: l, Repo: repo})
	}
	return results, nil
}

// ListRepos lists repositories accessible to the stored SCM account token.
func (s *CommitService) ListRepos(user *models.User, search string, page, perPage int) ([]*models.ScmRepository, error) {
	_, plainToken, err := s.resolveAccountToken(user, "")
	if err != nil {
		return nil, err
	}

	all := perPage == 0 && page == 0 // signal to fetch all pages when handler requests all=true

	// cache first
	if all {
		if cached := s.getCachedRepos(user.ID); cached != nil {
			return filterRepos(cached, search), nil
		}
	}

	client := NewGitHubClient(plainToken)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var repos []*githubRepo
	if all {
		r, err := s.fetchAllRepos(ctx, client)
		if err != nil {
			return nil, err
		}
		repos = r
		s.setCachedRepos(user.ID, repos)
	} else {
		if perPage <= 0 || perPage > 100 {
			perPage = 30
		}
		if page <= 0 {
			page = 1
		}
		r, err := client.ListRepos(ctx, page, perPage)
		if err != nil {
			return nil, err
		}
		repos = r
	}

	return filterRepos(repos, search), nil
}

// UpdateLink updates the branch override and/or token (if provided) for a linked project.
func (s *CommitService) UpdateLink(user *models.User, project, branchOverride, token string) error {
	link, err := s.links.GetByUserAndProject(user.ID, project)
	if err != nil {
		return err
	}

	if branchOverride != "" {
		link.BranchOverride = branchOverride
	}
	if token != "" {
		if err := s.UpdateToken(user, token); err != nil {
			return err
		}
	}

	// mark for resync
	link.LastSyncedAt = nil
	link.SyncStatus = "pending"
	link.SyncError = ""
	return s.links.Upsert(link)
}

// UpdateLinkByID updates link by id (for API routes).
func (s *CommitService) UpdateLinkByID(user *models.User, linkID, branchOverride, repoExternalID string) error {
	link, err := s.links.GetByID(linkID)
	if err != nil {
		return err
	}
	if link.UserID != user.ID {
		return errors.New("forbidden")
	}

	if branchOverride != "" {
		link.BranchOverride = branchOverride
	}

	if repoExternalID != "" {
		repo, err := s.repos.GetByExternalID(models.ScmProviderGithub, repoExternalID)
		if err != nil {
			return err
		}
		link.RepositoryID = repo.ID
	}

	link.LastSyncedAt = nil
	link.SyncStatus = "pending"
	link.SyncError = ""
	return s.links.Upsert(link)
}

// UpdateToken rotates the stored PAT for the user.
func (s *CommitService) UpdateToken(user *models.User, token string) error {
	if token == "" {
		return errors.New("token required")
	}
	account, err := s.accounts.GetByUserAndProvider(user.ID, models.ScmProviderGithub)
	if err != nil {
		account = &models.ScmAccount{
			ID:       uuid.Must(uuid.NewV4()).String(),
			UserID:   user.ID,
			Provider: models.ScmProviderGithub,
			AuthType: models.ScmAuthTypePat,
		}
	}
	enc, err := newTokenCipher().Encrypt(token)
	if err != nil {
		return err
	}
	account.AccessTokenEnc = enc
	account.AuthType = models.ScmAuthTypePat
	if err := s.accounts.Upsert(account); err != nil {
		return err
	}
	s.invalidateRepoCache(user.ID)
	return nil
}

// UnlinkProject removes the link between a project and repository.
func (s *CommitService) UnlinkProject(user *models.User, project string, purge bool) error {
	link, err := s.links.GetByUserAndProject(user.ID, project)
	if err != nil {
		return err
	}
	repo, err := s.repos.GetByID(link.RepositoryID)
	if err != nil {
		return err
	}

	if purge {
		if err := s.stats.DeleteByRepo(repo.ID); err != nil {
			return err
		}
		if err := s.commits.DeleteByRepo(repo.ID); err != nil {
			return err
		}
		if err := s.repos.Delete(repo); err != nil {
			return err
		}
	}

	return s.links.DeleteByUserAndProject(user.ID, project)
}

// UnlinkByID removes a link by id.
func (s *CommitService) UnlinkByID(user *models.User, linkID string, purge bool) error {
	link, err := s.links.GetByID(linkID)
	if err != nil {
		return err
	}
	if link.UserID != user.ID {
		return errors.New("forbidden")
	}
	repo, err := s.repos.GetByID(link.RepositoryID)
	if err != nil {
		return err
	}

	if purge {
		if err := s.stats.DeleteByRepo(repo.ID); err != nil {
			return err
		}
		if err := s.commits.DeleteByRepo(repo.ID); err != nil {
			return err
		}
		if err := s.repos.Delete(repo); err != nil {
			return err
		}
	}
	return s.links.DeleteByUserAndProject(user.ID, link.Project)
}

func (s *CommitService) effectiveBranch(link *models.ProjectRepositoryLink, repo *models.ScmRepository) string {
	if link.BranchOverride != "" {
		return link.BranchOverride
	}
	return repo.DefaultBranch
}

// SyncNow triggers an immediate sync for a given project link.
func (s *CommitService) SyncNow(user *models.User, project string) error {
	link, repo, account, err := s.ensureLink(user, project)
	if err != nil {
		return err
	}
	return s.Sync(link, account, repo)
}

// SyncByID triggers sync for link by id.
func (s *CommitService) SyncByID(user *models.User, linkID string) error {
	link, err := s.links.GetByID(linkID)
	if err != nil {
		return err
	}
	if link.UserID != user.ID {
		return errors.New("forbidden")
	}
	repo, err := s.repos.GetByID(link.RepositoryID)
	if err != nil {
		return err
	}
	account, err := s.accounts.GetByUserAndProvider(user.ID, models.ScmProviderGithub)
	if err != nil {
		return err
	}
	return s.Sync(link, account, repo)
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

// resolveAccountToken returns the SCM account and plain token.
// If explicitToken is provided, the user's account is created/updated with that token.
// Otherwise it loads the stored account and decrypts the token.
func (s *CommitService) resolveAccountToken(user *models.User, explicitToken string) (*models.ScmAccount, string, error) {
	if explicitToken != "" {
		if err := s.UpdateToken(user, explicitToken); err != nil {
			return nil, "", err
		}
		account, err := s.accounts.GetByUserAndProvider(user.ID, models.ScmProviderGithub)
		return account, explicitToken, err
	}

	account, err := s.accounts.GetByUserAndProvider(user.ID, models.ScmProviderGithub)
	if err != nil {
		return nil, "", err
	}
	token, err := newTokenCipher().Decrypt(account.AccessTokenEnc)
	if err != nil {
		return nil, "", err
	}
	return account, token, nil
}

func mapGitHubRepo(repo *githubRepo) *models.ScmRepository {
	id := uuid.Must(uuid.NewV4()).String()
	externalID := fmt.Sprintf("%d", repo.ID)

	return &models.ScmRepository{
		ID:            id,
		Provider:      models.ScmProviderGithub,
		ExternalID:    externalID,
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
}

type repoCacheEntry struct {
	repos     []*githubRepo
	fetchedAt time.Time
}

const repoCacheTTL = 5 * time.Minute

func (s *CommitService) getCachedRepos(userID string) []*githubRepo {
	s.repoCacheMu.RLock()
	defer s.repoCacheMu.RUnlock()
	entry, ok := s.repoCache[userID]
	if !ok || time.Since(entry.fetchedAt) > repoCacheTTL {
		return nil
	}
	return entry.repos
}

func (s *CommitService) setCachedRepos(userID string, repos []*githubRepo) {
	s.repoCacheMu.Lock()
	defer s.repoCacheMu.Unlock()
	s.repoCache[userID] = repoCacheEntry{repos: repos, fetchedAt: time.Now()}
}

func (s *CommitService) invalidateRepoCache(userID string) {
	s.repoCacheMu.Lock()
	defer s.repoCacheMu.Unlock()
	delete(s.repoCache, userID)
}

func (s *CommitService) fetchAllRepos(ctx context.Context, client *GitHubClient) ([]*githubRepo, error) {
	all := []*githubRepo{}
	page := 1
	perPage := 100

	for {
		repos, err := client.ListRepos(ctx, page, perPage)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}
		all = append(all, repos...)
		if len(repos) < perPage {
			break
		}
		page++
	}
	return all, nil
}

func filterRepos(repos []*githubRepo, search string) []*models.ScmRepository {
	result := make([]*models.ScmRepository, 0, len(repos))
	for _, r := range repos {
		if search != "" && !strings.Contains(strings.ToLower(r.FullName), strings.ToLower(search)) {
			continue
		}
		result = append(result, mapGitHubRepo(r))
	}
	return result
}

// Schedule periodic sync for stale project↔repo links.
func (s *CommitService) Schedule() {
	cronExpr := s.config.App.CommitSyncCron
	if cronExpr == "" {
		cronExpr = "0 */10 * * * *"
	}
	slog.Info("scheduling commit sync", "cron", cronExpr)

	if _, err := s.queue.DispatchCron(func() {
		s.syncStaleLinks()
	}, cronExpr); err != nil {
		config.Log().Error("failed to schedule commit sync", "error", err)
	}
}

func (s *CommitService) syncStaleLinks() {
	staleBefore := time.Now().Add(-linkSyncStaleAfter)

	links, err := s.links.ListStale(staleBefore, 50)
	if err != nil {
		slog.Warn("failed to list stale commit links", "error", err)
		return
	}

	for _, link := range links {
		account, err := s.accounts.GetByUserAndProvider(link.UserID, models.ScmProviderGithub)
		if err != nil {
			slog.Warn("no scm account for link", "link", link.ID, "error", err)
			continue
		}
		repo, err := s.repos.GetByID(link.RepositoryID)
		if err != nil {
			slog.Warn("missing repo for link", "link", link.ID, "error", err)
			continue
		}

		link.SyncStatus = "syncing"
		link.SyncError = ""
		_ = s.links.Upsert(link)

		if err := s.Sync(link, account, repo); err != nil {
			link.SyncStatus = "error"
			link.SyncError = err.Error()
			_ = s.links.Upsert(link)
			slog.Warn("sync stale link failed", "link", link.ID, "error", err)
		}
	}
}
