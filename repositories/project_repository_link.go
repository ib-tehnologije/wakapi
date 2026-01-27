package repositories

import (
	"time"

	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
)

type ProjectRepositoryLinkRepository struct {
	BaseRepository
}

func NewProjectRepositoryLinkRepository(db *gorm.DB) *ProjectRepositoryLinkRepository {
	return &ProjectRepositoryLinkRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *ProjectRepositoryLinkRepository) Upsert(link *models.ProjectRepositoryLink) error {
	return r.db.
		Clauses(clauseOnConflictDoUpdateAll()).
		Create(link).Error
}

func (r *ProjectRepositoryLinkRepository) GetByUserAndProject(userID, project string) (*models.ProjectRepositoryLink, error) {
	var link models.ProjectRepositoryLink
	if err := r.db.
		Where(&models.ProjectRepositoryLink{UserID: userID, Project: project}).
		First(&link).Error; err != nil {
		return nil, err
	}
	return &link, nil
}

func (r *ProjectRepositoryLinkRepository) GetByID(id string) (*models.ProjectRepositoryLink, error) {
	var link models.ProjectRepositoryLink
	if err := r.db.Where("id = ?", id).First(&link).Error; err != nil {
		return nil, err
	}
	return &link, nil
}

func (r *ProjectRepositoryLinkRepository) DeleteByUserAndProject(userID, project string) error {
	return r.db.
		Where(&models.ProjectRepositoryLink{UserID: userID, Project: project}).
		Delete(&models.ProjectRepositoryLink{}).Error
}

func (r *ProjectRepositoryLinkRepository) ListByUser(userID string) ([]*models.ProjectRepositoryLink, error) {
	var links []*models.ProjectRepositoryLink
	if err := r.db.
		Where(&models.ProjectRepositoryLink{UserID: userID}).
		Order("updated_at DESC").
		Find(&links).Error; err != nil {
		return nil, err
	}
	return links, nil
}

func (r *ProjectRepositoryLinkRepository) ListStale(staleBefore time.Time, limit int) ([]*models.ProjectRepositoryLink, error) {
	var links []*models.ProjectRepositoryLink
	q := r.db.
		Where("last_synced_at IS NULL OR last_synced_at < ?", staleBefore).
		Order("last_synced_at NULLS FIRST, updated_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&links).Error; err != nil {
		return nil, err
	}
	return links, nil
}
