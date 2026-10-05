package services

import (
	"errors"
	"testing"
	"time"

	"github.com/muety/wakapi/repositories"
	"github.com/stretchr/testify/assert"
)

type fakeCommitStatRepo struct {
	repositories.ICommitStatRepository // only the methods used by noteHeartbeat are implemented
	marked                             []string
	err                                error
}

func (f *fakeCommitStatRepo) MarkDirtyByUserProjectAfter(userID, project string, after time.Time) error {
	if f.err != nil {
		return f.err
	}
	if !after.IsZero() {
		return errors.New("expected the zero time (all stats of the project)")
	}
	f.marked = append(f.marked, userID+"/"+project)
	return nil
}

func TestCommitService_NoteHeartbeat_MarksProjectStatsStale(t *testing.T) {
	repo := &fakeCommitStatRepo{}
	sut := &CommitService{stats: repo, lastDirty: map[string]time.Time{}}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	assert.True(t, sut.noteHeartbeat("u1", "proj-a", now))
	// debounced within the interval, per user and project
	assert.False(t, sut.noteHeartbeat("u1", "proj-a", now.Add(30*time.Second)))
	assert.True(t, sut.noteHeartbeat("u1", "proj-b", now.Add(30*time.Second)))
	assert.True(t, sut.noteHeartbeat("u2", "proj-a", now.Add(30*time.Second)))
	// marked again once the interval has passed (a later heartbeat must not be lost)
	assert.True(t, sut.noteHeartbeat("u1", "proj-a", now.Add(commitStatsDirtyDebounce)))
	assert.Equal(t, []string{"u1/proj-a", "u1/proj-b", "u2/proj-a", "u1/proj-a"}, repo.marked)

	// nothing to mark without a user or a project
	assert.False(t, sut.noteHeartbeat("", "proj-a", now))
	assert.False(t, sut.noteHeartbeat("u1", "", now))
}

func TestCommitService_NoteHeartbeat_RetriesAfterRepoError(t *testing.T) {
	repo := &fakeCommitStatRepo{err: errors.New("db down")}
	sut := &CommitService{stats: repo, lastDirty: map[string]time.Time{}}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	assert.False(t, sut.noteHeartbeat("u1", "proj-a", now))
	repo.err = nil
	// not debounced by the failed attempt
	assert.True(t, sut.noteHeartbeat("u1", "proj-a", now.Add(time.Second)))
}
