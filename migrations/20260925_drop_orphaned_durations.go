package migrations

import (
	"log/slog"

	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
	"gorm.io/gorm"
)

// We didn't have a foreign key constraint on durations.user_id (because no *models.User field on the struct),
// which resulted in user deletion not cascading to durations, leaving many orphaned rows. This migrations aims to drop them.
// Note that for existing SQLite instances, the foreign key constraint still won't be created (as part of AutoMigrate), because SQLite doesn't allow so for existing tables.
// We'd have to recreate the table (create new one, copy rows, drop old one), as already being done by other migrations.
// However, the benefit from having this foreign key constraint (i.e. avoiding orphaned rows) is arguably quite limited, so perhaps not really worth that effort.

func init() {
	const name = "20260925-drop_orphaned_durations"
	f := migrationFunc{
		name:       name,
		background: false,
		f: func(db *gorm.DB, cfg *config.Config) error {
			if hasRun(name, db) {
				return nil
			}

			if !db.Migrator().HasTable(&models.Duration{}) {
				return nil
			}

			// Just for the record: some useful queries in this context:
			// Count orphaned rows:
			// 	select count(*) from durations d left join users u on d.user_id = u.id where u.id is null;
			// Get deleted users:
			//	select distinct d.user_id from durations d left join users u on d.user_id = u.id where u.id is null;

			slog.Info("deleting orphaned durations, this may take a while")
			if err := db.
				Where("user_id NOT IN (?)", db.Model(&models.User{}).Select("id")).
				Delete(&models.Duration{}).
				Error; err != nil {
				return err
			}
			slog.Info("done deleting orphaned durations, creating foreign key constraint now, which might take even longer")

			setHasRun(name, db)
			return nil
		},
	}

	registerPreMigration(f)
}
