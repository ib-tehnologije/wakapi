package repositories

import (
	"time"

	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
)

type CommitStatRepository struct {
	BaseRepository
}

func NewCommitStatRepository(db *gorm.DB) *CommitStatRepository {
	return &CommitStatRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *CommitStatRepository) Upsert(stat *models.CommitStat) error {
	return r.db.
		Clauses(clauseOnConflictDoUpdateAll()).
		Create(stat).Error
}

func (r *CommitStatRepository) DeleteByRepo(repoID string) error {
	return r.db.Where(&models.CommitStat{RepositoryID: repoID}).Delete(&models.CommitStat{}).Error
}

func (r *CommitStatRepository) MarkDirtyByUserProjectAfter(userID, project string, after time.Time) error {
	return r.db.
		Model(&models.CommitStat{}).
		Where("user_id = ? AND project = ? AND calculated_at >= ?", userID, project, after).
		Update("dirty", true).Error
}

func (r *CommitStatRepository) GetByUserProjectBranch(userID, project, branch string, limit, offset int) ([]*models.CommitStat, int64, error) {
	var stats []*models.CommitStat
	q := r.db.
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
