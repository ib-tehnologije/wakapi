package repositories

import (
	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ScmAccountRepository struct {
	BaseRepository
}

func NewScmAccountRepository(db *gorm.DB) *ScmAccountRepository {
	return &ScmAccountRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *ScmAccountRepository) Upsert(acc *models.ScmAccount) error {
	return r.db.
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "provider"}},
			DoUpdates: clause.AssignmentColumns([]string{"auth_type", "access_token_enc", "refresh_token_enc", "installation_id", "expires_at", "provider_user_id", "provider_login", "avatar_url", "updated_at", "revoked_at"}),
		}).
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
