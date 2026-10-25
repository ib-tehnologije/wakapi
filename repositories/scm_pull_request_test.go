package repositories

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestScmPullRequestRepository_UpsertAndWatermark(t *testing.T) {
	cfg := config.Empty()
	cfg.Db.Dialect = config.SQLDialectSqlite
	config.Set(cfg)

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wakapi_test.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.ScmPullRequest{}))
	sut := NewScmPullRequestRepository(db)

	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pr := func(id string, repo string, number int, state string, updated time.Time) *models.ScmPullRequest {
		return &models.ScmPullRequest{ID: id, RepositoryID: repo, Number: number, State: state, HeadRef: "feature/x", ProviderUpdatedAt: models.CustomTime(updated)}
	}

	require.NoError(t, sut.UpsertMany([]*models.ScmPullRequest{
		pr("a", "r1", 1, "open", base.Add(-2*time.Hour)),
		pr("b", "r1", 2, "open", base.Add(-time.Hour)),
		pr("c", "r2", 1, "open", base),
	}))

	// the same PR synced again (new id from the mapper) updates the stored row
	merged := models.CustomTime(base.Add(time.Hour))
	again := pr("a2", "r1", 1, "closed", base.Add(time.Hour))
	again.MergedAt = &merged
	require.NoError(t, sut.UpsertMany([]*models.ScmPullRequest{again}))

	var count int64
	require.NoError(t, db.Model(&models.ScmPullRequest{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)

	latest, err := sut.GetLatestUpdatedByRepo("r1")
	require.NoError(t, err)
	assert.Equal(t, 1, latest.Number)
	assert.Equal(t, "a", latest.ID)
	assert.Equal(t, "closed", latest.State)
	require.NotNil(t, latest.MergedAt)
	assert.True(t, latest.MergedAt.T().Equal(merged.T()))

	since, err := sut.GetByRepoUpdatedSince("r1", base.Add(-90*time.Minute))
	require.NoError(t, err)
	require.Len(t, since, 2)
	assert.Equal(t, 2, since[0].Number)
	assert.Equal(t, 1, since[1].Number)

	all, err := sut.GetByRepoUpdatedSince("r1", time.Time{})
	require.NoError(t, err)
	assert.Len(t, all, 2)

	_, err = sut.GetLatestUpdatedByRepo("r3")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	require.NoError(t, sut.DeleteByRepo("r1"))
	require.NoError(t, db.Model(&models.ScmPullRequest{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
