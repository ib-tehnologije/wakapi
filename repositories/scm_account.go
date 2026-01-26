package repositories

import (
	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
)

type ScmAccountRepository struct {
	BaseRepository
}

func NewScmAccountRepository(db *gorm.DB) *ScmAccountRepository {
	return &ScmAccountRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *ScmAccountRepository) Upsert(acc *models.ScmAccount) error {
	return r.db.
		Clauses(clauseOnConflictDoUpdateAll()).
		Create(acc).Error
}

func (r *ScmAccountRepository) GetByUserAndProvider(userID, provider string) (*models.ScmAccount, error) {
	var acc models.ScmAccount
	if err := r.db.
		Where(&models.ScmAccount{UserID: userID, Provider: provider}).
		First(&acc).Error; err != nil {
		return nil, err
	}
	return &acc, nil
}

func (r *ScmAccountRepository) DeleteByUserAndProvider(userID, provider string) error {
	return r.db.
		Where(&models.ScmAccount{UserID: userID, Provider: provider}).
		Delete(&models.ScmAccount{}).Error
}
