package migrations

import (
	"github.com/muety/wakapi/config"
	"gorm.io/gorm"
)

func init() {
	const name = "20260127_add_installation_id_to_scm_accounts"
	f := migrationFunc{
		name:       name,
		background: false,
		f: func(db *gorm.DB, cfg *config.Config) error {
			if hasRun(name, db) {
				return nil
			}

			type ScmAccount struct {
				InstallationID string `gorm:"size:128"`
			}

			if err := db.AutoMigrate(&ScmAccount{}); err != nil {
				return err
			}

			setHasRun(name, db)
			return nil
		},
	}
	registerPostMigration(f)
}
