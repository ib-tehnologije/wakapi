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

func TestCommitStatRepository_MarkDirtyByUserCommittedSince(t *testing.T) {
	cfg := config.Empty()
	cfg.Db.Dialect = config.SQLDialectSqlite
	config.Set(cfg)

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wakapi_test.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.ScmCommit{}, &models.CommitStat{}))

	base := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	add := func(user, repo, hash string, committed time.Time) {
		require.NoError(t, db.Create(&models.ScmCommit{ID: repo + hash, RepositoryID: repo, Hash: hash, Branch: "main", CommitterDate: models.CustomTime(committed)}).Error)
		require.NoError(t, db.Create(&models.CommitStat{ID: user + repo + hash, UserID: user, Project: repo, RepositoryID: repo, Branch: "main", CommitHash: hash}).Error)
	}
	add("u1", "r1", "old", base.Add(-time.Hour))
	add("u1", "r1", "new", base.Add(time.Hour))
	add("u1", "r2", "new", base.Add(2*time.Hour)) // other project of the same user
	add("u2", "r3", "new", base.Add(time.Hour))   // other user

	sut := NewCommitStatRepository(db)
	require.NoError(t, sut.MarkDirtyByUserCommittedSince("u1", base))

	var dirty []string
	require.NoError(t, db.Model(&models.CommitStat{}).Where("dirty = ?", true).Order("id").Pluck("id", &dirty).Error)
	assert.Equal(t, []string{"u1r1new", "u1r2new"}, dirty)

	// a heartbeat newer than every commit marks nothing
	require.NoError(t, db.Model(&models.CommitStat{}).Where("1 = 1").Update("dirty", false).Error)
	require.NoError(t, sut.MarkDirtyByUserCommittedSince("u1", base.Add(3*time.Hour)))
	var count int64
	require.NoError(t, db.Model(&models.CommitStat{}).Where("dirty = ?", true).Count(&count).Error)
	assert.Zero(t, count)
}
