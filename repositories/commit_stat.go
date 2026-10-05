package repositories

import (
	"time"

	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CommitStatRepository struct {
	BaseRepository
}

func NewCommitStatRepository(db *gorm.DB) *CommitStatRepository {
	return &CommitStatRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *CommitStatRepository) Upsert(stat *models.CommitStat) error {
	return r.db.
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "project"}, {Name: "branch"}, {Name: "commit_hash"}},
			DoUpdates: clause.AssignmentColumns([]string{"total_seconds", "human_readable_total", "human_readable_total_with_second", "calculated_at", "algo_version", "dirty", "updated_at"}),
		}).
		Create(stat).Error
}

func (r *CommitStatRepository) DeleteByRepo(repoID string) error {
	return r.db.Where(&models.CommitStat{RepositoryID: repoID}).Delete(&models.CommitStat{}).Error
}

// MarkDirtyByUserCommittedSince marks the stats of all the user's commits made at or after the given time stale.
func (r *CommitStatRepository) MarkDirtyByUserCommittedSince(userID string, since time.Time) error {
	return r.db.
		Model(&models.CommitStat{}).
		Where("user_id = ? AND dirty = ?", userID, false).
		Where("EXISTS (SELECT 1 FROM scm_commits WHERE scm_commits.repository_id = commit_stats.repository_id AND scm_commits.hash = commit_stats.commit_hash AND scm_commits.committer_date >= ?)", models.CustomTime(since)).
		Update("dirty", true).Error
}

func (r *CommitStatRepository) GetByUserProjectBranch(userID, project, branch string, limit, offset int) ([]*models.CommitStat, int64, error) {
	var stats []*models.CommitStat
	q := r.db.
		Model(&models.CommitStat{}).
		Where("user_id = ? AND project = ? AND branch = ?", userID, project, branch).
		Order("calculated_at DESC")
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&stats).Error; err != nil {
		return nil, 0, err
	}
	return stats, total, nil
}

func (r *CommitStatRepository) GetByUserProjectBranchAndHash(userID, project, branch, hash string) (*models.CommitStat, error) {
	var stat models.CommitStat
	if err := r.db.
		Where("user_id = ? AND project = ? AND branch = ? AND commit_hash = ?", userID, project, branch, hash).
		First(&stat).Error; err != nil {
		return nil, err
	}
	return &stat, nil
}
