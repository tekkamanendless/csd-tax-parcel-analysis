package database

import (
	"context"
	"fmt"
	"log/slog"

	gormextraclauseplugin "github.com/WinterYukky/gorm-extra-clause-plugin"
	"github.com/ncruces/go-sqlite3/gormlite"
	"github.com/tekkamanendless/gormslog"
	"gorm.io/gorm"
)

// New creates a new database connection.
func New(ctx context.Context, driverName string, connectionString string) (*gorm.DB, error) {
	var db *gorm.DB
	var err error

	config := &gorm.Config{
		TranslateError: true,           // Ensure that errors are properly translated into the Gorm built-in ones.
		Logger:         gormslog.New(), // Use slog for logging.
	}

	switch driverName {
	case "sqlite3":
		db, err = gorm.Open(gormlite.Open(connectionString), config)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid database driver: %s", driverName)
	}

	err = db.Use(gormextraclauseplugin.New())
	if err != nil {
		return nil, err
	}

	// Only turn on database debugging if we're in debug mode.
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		db = db.Debug()
	}

	return db, nil
}
