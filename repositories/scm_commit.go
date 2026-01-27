package repositories

import (
	"time"

	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
)

type ScmCommitRepository struct {
	BaseRepository
}

func NewScmCommitRepository(db *gorm.DB) *ScmCommitRepository {
	return &ScmCommitRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *ScmCommitRepository) UpsertMany(commits []*models.ScmCommit) error {
	return InsertBatchChunked(commits, &models.ScmCommit{}, r.db)
}

func (r *ScmCommitRepository) DeleteByRepo(repoID string) error {
	return r.db.Where(&models.ScmCommit{RepositoryID: repoID}).Delete(&models.ScmCommit{}).Error
}

func (r *ScmCommitRepository) GetLatestByRepoAndBranch(repoID, branch string) (*models.ScmCommit, error) {
	var commit models.ScmCommit
	if err := r.db.
		Where("repository_id = ? AND branch = ?", repoID, branch).
		Order("committer_date DESC").
		First(&commit).Error; err != nil {
		return nil, err
	}
	return &commit, nil
}

func (r *ScmCommitRepository) GetByRepoAndBranchAfter(repoID, branch string, after time.Time, limit, offset int) ([]*models.ScmCommit, error) {
	var commits []*models.ScmCommit
	q := r.db.
		Where("repository_id = ? AND branch = ? AND committer_date > ?", repoID, branch, after).
		Order("committer_date ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&commits).Error; err != nil {
		return nil, err
	}
	return commits, nil
}

func (r *ScmCommitRepository) GetByRepoAndHash(repoID, hash string) (*models.ScmCommit, error) {
	var commit models.ScmCommit
	if err := r.db.
		Where("repository_id = ? AND hash = ?", repoID, hash).
		First(&commit).Error; err != nil {
		return nil, err
	}
	return &commit, nil
}
