package repositories

import (
	"time"

	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ScmPullRequestRepository struct {
	BaseRepository
}

func NewScmPullRequestRepository(db *gorm.DB) *ScmPullRequestRepository {
	return &ScmPullRequestRepository{BaseRepository: NewBaseRepository(db)}
}

// UpsertMany inserts pull requests or updates the mutable fields of known ones (by repository and number).
func (r *ScmPullRequestRepository) UpsertMany(prs []*models.ScmPullRequest) error {
	if len(prs) == 0 {
		return nil
	}
	return r.db.
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "repository_id"}, {Name: "number"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"title", "html_url", "state", "head_ref", "head_sha", "base_ref", "author_login",
				"closed_at", "merged_at", "merge_commit_sha", "provider_updated_at", "updated_at",
			}),
		}).
		CreateInBatches(prs, 100).Error
}

func (r *ScmPullRequestRepository) DeleteByRepo(repoID string) error {
	return r.db.Where(&models.ScmPullRequest{RepositoryID: repoID}).Delete(&models.ScmPullRequest{}).Error
}

// GetLatestUpdatedByRepo returns the repository's most recently updated pull request (the sync watermark).
func (r *ScmPullRequestRepository) GetLatestUpdatedByRepo(repoID string) (*models.ScmPullRequest, error) {
	var pr models.ScmPullRequest
	if err := r.db.
		Where("repository_id = ?", repoID).
		Order("provider_updated_at DESC").
		First(&pr).Error; err != nil {
		return nil, err
	}
	return &pr, nil
}

// GetByRepoUpdatedSince lists the repository's pull requests updated on GitHub at or after the given time, oldest first.
func (r *ScmPullRequestRepository) GetByRepoUpdatedSince(repoID string, since time.Time) ([]*models.ScmPullRequest, error) {
	var prs []*models.ScmPullRequest
	q := r.db.Where("repository_id = ?", repoID)
	if !since.IsZero() {
		q = q.Where("provider_updated_at >= ?", models.CustomTime(since))
	}
	if err := q.Order("provider_updated_at ASC, number ASC").Find(&prs).Error; err != nil {
		return nil, err
	}
	return prs, nil
}
