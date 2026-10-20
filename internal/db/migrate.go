package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var MigrationFS embed.FS

// Migrate applies versioned SQL files embedded in this package.
func Migrate(db *sql.DB) error {
	return MigrateWithBackup(db, "")
}

// MigrateWithBackup applies pending migrations, first snapshotting an existing
// database into backupDir. A failed snapshot stops before anything changes.
func MigrateWithBackup(db *sql.DB, backupDir string) error {
	sub, err := fs.Sub(MigrationFS, "migrations")
	if err != nil {
		return err
	}
	return migrateFS(db, sub, backupDir)
}

type migration struct {
	version int
	name    string
}

func migrateFS(db *sql.DB, fsys fs.FS, backupDir string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`); err != nil {
		return err
	}

	entries, err := fs.ReadDir(fsys, ".")
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

	var pending []migration
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
		if exists == 0 {
			pending = append(pending, migration{version: ver, name: name})
		}
	}
	if len(pending) == 0 {
		return nil
	}

	var applied int
	if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&applied); err != nil {
		return err
	}
	if backupDir != "" && applied > 0 {
		path, err := snapshot(db, backupDir, pending[0].version)
		if err != nil {
			return fmt.Errorf("pre-migration backup: %w", err)
		}
		log.Printf("pre-migration backup written to %s", path)
	}

	for _, m := range pending {
		body, err := fs.ReadFile(fsys, m.name)
		if err != nil {
			return err
		}
		name, ver := m.name, m.version
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
		log.Printf("migration %s applied", name)
	}
	return nil
}

func snapshot(db *sql.DB, dir string, version int) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("pre-migrate-%03d-%s.db", version, time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	if _, err := db.Exec(`VACUUM INTO ?`, path); err != nil {
		return "", err
	}
	return path, nil
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
