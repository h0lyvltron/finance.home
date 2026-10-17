package db

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var MigrationFS embed.FS

// Migrate applies versioned SQL files embedded in this package.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`); err != nil {
		return err
	}

	entries, err := MigrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		ver, err := parseVersion(name)
		if err != nil {
			return err
		}
		var exists int
		err = db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, ver).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := MigrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, ver); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func parseVersion(name string) (int, error) {
	parts := strings.SplitN(name, "_", 2)
	if len(parts) < 1 {
		return 0, fmt.Errorf("bad migration name %q", name)
	}
	return strconv.Atoi(parts[0])
}

// NeedsSetup returns true when no household row exists.
func NeedsSetup(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='households'`).Scan(&n)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return true, nil
	}
	err = db.QueryRow(`SELECT COUNT(1) FROM households`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}
