// Package migrations embeds the SQL migration files and applies them with
// golang-migrate at startup. Files follow the golang-migrate convention:
// NNNNNN_name.up.sql / NNNNNN_name.down.sql.
package migrations

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed *.sql
var FS embed.FS

// Up applies all pending migrations. DATABASE_URL keeps its postgres://
// scheme; golang-migrate's postgres driver handles it. ErrNoChange is not
// an error — the schema is already current.
func Up(dbURL string) error {
	src, err := iofs.New(FS, ".")
	if err != nil {
		return fmt.Errorf("migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
