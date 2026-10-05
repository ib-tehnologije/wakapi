package services

import (
	"errors"
	"testing"
	"time"

	"github.com/muety/wakapi/models"
	"github.com/muety/wakapi/repositories"
	"github.com/stretchr/testify/assert"
)

type fakeCommitStatRepo struct {
	repositories.ICommitStatRepository // only the methods used by the tests are implemented
	marked                             []string
	err                                error
	stats                              map[string]*models.CommitStat
	upserted                           []string
}

func (f *fakeCommitStatRepo) MarkDirtyByUserCommittedSince(userID string, since time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.marked = append(f.marked, userID+"@"+since.Format("15:04"))
	return nil
}

func (f *fakeCommitStatRepo) GetByUserProjectBranch(userID, project, branch string, limit, offset int) ([]*models.CommitStat, int64, error) {
	out := make([]*models.CommitStat, 0, len(f.stats))
	for _, s := range f.stats {
		out = append(out, s)
	}
	return out, int64(len(out)), nil
}

func (f *fakeCommitStatRepo) Upsert(stat *models.CommitStat) error {
	if f.stats == nil {
		f.stats = map[string]*models.CommitStat{}
	}
	f.stats[stat.CommitHash] = stat
	f.upserted = append(f.upserted, stat.CommitHash)
	return nil
}

type fakeScmCommitRepo struct {
	repositories.IScmCommitRepository
	commits []*models.ScmCommit
}

func (f *fakeScmCommitRepo) GetByRepoAndBranchAfter(repoID, branch string, after time.Time, limit, offset int) ([]*models.ScmCommit, error) {
	return f.commits, nil
}

type fakeDurationService struct {
	IDurationService
	durations models.Durations
	calls     int
}

func (f *fakeDurationService) Get(from, to time.Time, user *models.User, filters *models.Filters, customTimeout *time.Duration, skipCache bool) (models.Durations, error) {
	f.calls++
	return f.durations, nil
}

func TestCommitService_NoteHeartbeat_MarksCommitsSinceHeartbeat(t *testing.T) {
	repo := &fakeCommitStatRepo{}
	sut := &CommitService{stats: repo, lastDirty: map[string]dirtyMark{}}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	hb := time.Date(2026, 10, 5, 11, 0, 0, 0, time.UTC)

	assert.True(t, sut.noteHeartbeat("u1", hb, now))
	// debounced within the interval when not older than what was already marked
	assert.False(t, sut.noteHeartbeat("u1", hb.Add(10*time.Minute), now.Add(30*time.Second)))
	// an older heartbeat reaches further back and must not be lost
	assert.True(t, sut.noteHeartbeat("u1", hb.Add(-30*time.Minute), now.Add(40*time.Second)))
	assert.False(t, sut.noteHeartbeat("u1", hb, now.Add(50*time.Second)))
	// per user
	assert.True(t, sut.noteHeartbeat("u2", hb, now.Add(30*time.Second)))
	// marked again once the interval has passed
	assert.True(t, sut.noteHeartbeat("u1", hb, now.Add(40*time.Second+commitStatsDirtyDebounce)))
	assert.Equal(t, []string{"u1@11:00", "u1@10:30", "u2@11:00", "u1@11:00"}, repo.marked)

	// nothing to mark without a user or a time
	assert.False(t, sut.noteHeartbeat("", hb, now))
	assert.False(t, sut.noteHeartbeat("u1", time.Time{}, now))
}

func TestCommitService_NoteHeartbeat_RetriesAfterRepoError(t *testing.T) {
	repo := &fakeCommitStatRepo{err: errors.New("db down")}
	sut := &CommitService{stats: repo, lastDirty: map[string]dirtyMark{}}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	assert.False(t, sut.noteHeartbeat("u1", now, now))
	repo.err = nil
	// not debounced by the failed attempt
	assert.True(t, sut.noteHeartbeat("u1", now, now.Add(time.Second)))
}

func TestCommitService_ComputeStats_OnlyRecomputesStaleCommits(t *testing.T) {
	base := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	commit := func(hash string, hours int) *models.ScmCommit {
		return &models.ScmCommit{Hash: hash, CommitterDate: models.CustomTime(base.Add(time.Duration(hours) * time.Hour))}
	}
	stat := func(hash string, dirty bool, version int) *models.CommitStat {
		return &models.CommitStat{CommitHash: hash, Dirty: dirty, AlgoVersion: version, TotalSeconds: 1}
	}
	commits := &fakeScmCommitRepo{commits: []*models.ScmCommit{commit("a", 1), commit("b", 2), commit("c", 3), commit("d", 4), commit("e", 5), commit("f", 6)}}
	stats := &fakeCommitStatRepo{stats: map[string]*models.CommitStat{
		"a": stat("a", false, models.CommitAlgoVersion),
		"b": stat("b", true, models.CommitAlgoVersion),    // marked stale by a late heartbeat
		"c": stat("c", false, models.CommitAlgoVersion-1), // older algorithm
		// "d" is new, so the interval of "e" changed as well
		"e": stat("e", false, models.CommitAlgoVersion),
		"f": stat("f", false, models.CommitAlgoVersion),
	}}
	// 20 minutes of work that ended right before commit "b"
	durations := &fakeDurationService{durations: models.Durations{{Time: models.CustomTime(base.Add(100 * time.Minute)), Duration: 20 * time.Minute}}}
	sut := &CommitService{stats: stats, commits: commits, durations: durations}
	repo := &models.ScmRepository{ID: "r1"}

	assert.NoError(t, sut.computeStats("u1", "proj", repo, "main"))
	assert.Equal(t, []string{"b", "c", "d", "e"}, stats.upserted)
	assert.Equal(t, 4, durations.calls)
	assert.Equal(t, float64(20*60), stats.stats["b"].TotalSeconds)
	assert.False(t, stats.stats["b"].Dirty)
	assert.Equal(t, models.CommitAlgoVersion, stats.stats["c"].AlgoVersion)
	assert.Equal(t, float64(1), stats.stats["a"].TotalSeconds)

	// everything is current now: a second pass computes nothing
	assert.NoError(t, sut.computeStats("u1", "proj", repo, "main"))
	assert.Equal(t, 4, durations.calls)
}
