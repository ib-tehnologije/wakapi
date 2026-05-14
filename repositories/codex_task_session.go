package repositories

import (
	"errors"
	"time"

	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
)

type CodexTaskSessionRepository struct {
	BaseRepository
}

func NewCodexTaskSessionRepository(db *gorm.DB) *CodexTaskSessionRepository {
	return &CodexTaskSessionRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *CodexTaskSessionRepository) Upsert(session *models.CodexTaskSession) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing models.CodexTaskSession
		err := tx.
			Where("user_id = ? AND external_key = ?", session.UserID, session.ExternalKey).
			First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(session).Error
		}
		if err != nil {
			return err
		}

		session.ID = existing.ID
		session.CreatedAt = existing.CreatedAt
		return tx.Save(session).Error
	})
}

func (r *CodexTaskSessionRepository) GetByUserWithin(userID string, from, to *time.Time, project string) ([]*models.CodexTaskSession, error) {
	var sessions []*models.CodexTaskSession
	q := r.db.
		Model(&models.CodexTaskSession{}).
		Where("user_id = ?", userID).
		Order("started_at ASC")

	if from != nil {
		q = q.Where("started_at >= ?", from.Local())
	}
	if to != nil {
		q = q.Where("started_at <= ?", to.Local())
	}
	if project != "" {
		q = q.Where("project = ?", project)
	}

	if err := q.Find(&sessions).Error; err != nil {
		return nil, err
	}
	return sessions, nil
}
