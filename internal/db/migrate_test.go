package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMigrateSnapshotsBeforePending(t *testing.T) {
	dir := t.TempDir()
	sqlDB, err := Open(filepath.Join(dir, "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	backups := filepath.Join(dir, "backups")

	fsys := fstest.MapFS{
		"001_init.sql": {Data: []byte(`CREATE TABLE things (id INTEGER PRIMARY KEY, name TEXT);`)},
	}
	if err := migrateFS(sqlDB, fsys, backups); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(backups); len(entries) != 0 {
		t.Fatalf("fresh database should not be snapshotted: %v", entries)
	}
	if _, err := sqlDB.Exec(`INSERT INTO things(name) VALUES ('before')`); err != nil {
		t.Fatal(err)
	}

	if err := migrateFS(sqlDB, fsys, backups); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(backups); len(entries) != 0 {
		t.Fatalf("no pending migrations should not snapshot: %v", entries)
	}

	fsys["002_more.sql"] = &fstest.MapFile{Data: []byte(`ALTER TABLE things ADD COLUMN extra TEXT;`)}
	if err := migrateFS(sqlDB, fsys, backups); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "pre-migrate-002-") {
		t.Fatalf("snapshot entries %v", entries)
	}

	snap, err := Open(filepath.Join(backups, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	var name string
	if err := snap.QueryRow(`SELECT name FROM things`).Scan(&name); err != nil || name != "before" {
		t.Fatalf("snapshot row %q err %v", name, err)
	}
	if _, err := snap.Exec(`SELECT extra FROM things`); err == nil {
		t.Fatal("snapshot should predate migration 002")
	}
}

func TestMigrateStopsWhenSnapshotFails(t *testing.T) {
	dir := t.TempDir()
	sqlDB, err := Open(filepath.Join(dir, "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	fsys := fstest.MapFS{
		"001_init.sql": {Data: []byte(`CREATE TABLE things (id INTEGER PRIMARY KEY);`)},
	}
	if err := migrateFS(sqlDB, fsys, ""); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys["002_more.sql"] = &fstest.MapFile{Data: []byte(`ALTER TABLE things ADD COLUMN extra TEXT;`)}
	if err := migrateFS(sqlDB, fsys, filepath.Join(blocker, "backups")); err == nil {
		t.Fatal("expected snapshot failure")
	}
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=2`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("migration 002 applied despite failed snapshot (n=%d err=%v)", n, err)
	}
}
