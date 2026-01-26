package repositories

import (
	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
)

type ScmRepositoryRepository struct {
	BaseRepository
}

func NewScmRepositoryRepository(db *gorm.DB) *ScmRepositoryRepository {
	return &ScmRepositoryRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *ScmRepositoryRepository) Upsert(repo *models.ScmRepository) error {
	return r.db.
		Clauses(clauseOnConflictDoUpdateAll()).
		Create(repo).Error
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
