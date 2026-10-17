package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CreateBackupFile runs VACUUM INTO a timestamped file under destDir and returns the path.
func (s *Store) CreateBackupFile(destDir string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("finance-backup-%s.db", time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(destDir, name)
	// VACUUM INTO needs a non-existent path
	_ = os.Remove(path)
	if _, err := s.DB.Exec(`VACUUM INTO ?`, path); err != nil {
		return "", fmt.Errorf("VACUUM INTO: %w", err)
	}
	return path, nil
}
