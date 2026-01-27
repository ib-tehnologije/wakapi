package repositories

import (
	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ScmRepositoryRepository struct {
	BaseRepository
}

func NewScmRepositoryRepository(db *gorm.DB) *ScmRepositoryRepository {
	return &ScmRepositoryRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *ScmRepositoryRepository) Upsert(repo *models.ScmRepository) error {
	return r.db.
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider"}, {Name: "external_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"full_name", "name", "owner", "html_url", "api_url", "description", "homepage", "default_branch", "is_private", "is_fork", "star_count", "fork_count", "watch_count", "updated_at"}),
		}).
		Create(repo).Error
}

func (r *ScmRepositoryRepository) GetByExternalID(provider, externalID string) (*models.ScmRepository, error) {
	var repo models.ScmRepository
	if err := r.db.
		Where(&models.ScmRepository{Provider: provider, ExternalID: externalID}).
		First(&repo).Error; err != nil {
		return nil, err
	}
	return &repo, nil
}

func (r *ScmRepositoryRepository) GetByProviderAndFullName(provider, fullName string) (*models.ScmRepository, error) {
	var repo models.ScmRepository
	if err := r.db.
		Where(&models.ScmRepository{Provider: provider, FullName: fullName}).
		First(&repo).Error; err != nil {
		return nil, err
	}
	return &repo, nil
}

func (r *ScmRepositoryRepository) GetByID(id string) (*models.ScmRepository, error) {
	var repo models.ScmRepository
	if err := r.db.Where("id = ?", id).First(&repo).Error; err != nil {
		return nil, err
	}
	return &repo, nil
}

func (r *ScmRepositoryRepository) Delete(repo *models.ScmRepository) error {
	return r.db.Delete(repo).Error
}
