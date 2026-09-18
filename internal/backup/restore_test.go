package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"pappice/internal/dblock"
	"pappice/internal/store"
)

func TestRestoreMovesRollBackEveryFailure(t *testing.T) {
	for failAt := 1; failAt <= 6; failAt++ {
		t.Run(fmt.Sprintf("move_%d", failAt), func(t *testing.T) {
			moves := restoreTestMoves(t)
			failure := errors.New("injected rename failure")
			calls := 0
			err := moveRestoreFiles(moves, func(from, to string) error {
				calls++
				if calls == failAt {
					return failure
				}
				return os.Rename(from, to)
			})
			if !errors.Is(err, failure) {
				t.Fatalf("restore error = %v, want injected failure", err)
			}
			for i, move := range moves {
				path := move.from
				if i == 3 || i == 5 {
					path = filepath.Join(path, "attachment.txt")
				}
				if got := readFile(t, path); got != fmt.Sprint(i) {
					t.Fatalf("%s changed after failed restore: %q", path, got)
				}
			}
		})
	}
}

func TestRestoreMovesRollBackWithoutOriginals(t *testing.T) {
	moves := restoreTestMoves(t)
	for _, move := range moves[:4] {
		if err := os.RemoveAll(move.from); err != nil {
			t.Fatal(err)
		}
	}
	failure := errors.New("cannot install uploads")
	err := moveRestoreFiles(moves, func(from, to string) error {
		if from == moves[5].from {
			return failure
		}
		return os.Rename(from, to)
	})
	if !errors.Is(err, failure) {
		t.Fatalf("restore error = %v, want injected failure", err)
	}
	for _, move := range moves[:4] {
		if _, err := os.Lstat(move.from); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed restore left %s behind: %v", move.from, err)
		}
	}
	if got := readFile(t, moves[4].from); got != "4" {
		t.Fatalf("staged database changed: %q", got)
	}
}

func TestRestoreMovesKeepOriginalWhenRollbackFails(t *testing.T) {
	moves := restoreTestMoves(t)
	installErr := errors.New("cannot install uploads")
	rollbackErr := errors.New("cannot restore original database")
	err := moveRestoreFiles(moves, func(from, to string) error {
		if from == moves[5].from {
			return installErr
		}
		if from == moves[0].to {
			return rollbackErr
		}
		return os.Rename(from, to)
	})
	if !errors.Is(err, installErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("restore error = %v, want both failures", err)
	}
	if !strings.Contains(err.Error(), moves[0].to) {
		t.Fatalf("error does not identify saved database: %v", err)
	}
	if got := readFile(t, moves[0].to); got != "0" {
		t.Fatalf("saved database changed: %q", got)
	}
	if got := readFile(t, filepath.Join(moves[3].from, "attachment.txt")); got != "3" {
		t.Fatalf("original uploads were not restored: %q", got)
	}
}

func restoreTestMoves(t *testing.T) []restoreMove {
	t.Helper()
	dir := t.TempDir()
	saved := filepath.Join(dir, "saved")
	if err := os.Mkdir(saved, 0o700); err != nil {
		t.Fatal(err)
	}
	moves := []restoreMove{
		{filepath.Join(dir, "pappice.db"), filepath.Join(saved, "pappice.db"), true},
		{filepath.Join(dir, "pappice.db-wal"), filepath.Join(saved, "pappice.db-wal"), true},
		{filepath.Join(dir, "pappice.db-shm"), filepath.Join(saved, "pappice.db-shm"), true},
		{filepath.Join(dir, "uploads"), filepath.Join(saved, "uploads"), true},
		{filepath.Join(dir, "new.db"), filepath.Join(dir, "pappice.db"), false},
		{filepath.Join(dir, "new-uploads"), filepath.Join(dir, "uploads"), false},
	}
	for i, move := range moves {
		path := move.from
		if i == 3 || i == 5 {
			path = filepath.Join(path, "attachment.txt")
		}
		writeFile(t, path, fmt.Sprint(i))
	}
	return moves
}

func TestRestoreAcrossFilesystems(t *testing.T) {
	dir := t.TempDir()
	other, err := os.MkdirTemp("/dev/shm", "pappice-restore-test-")
	if err != nil {
		t.Skipf("second filesystem unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	probe := filepath.Join(dir, "probe")
	writeFile(t, probe, "probe")
	if err := os.Rename(probe, filepath.Join(other, "probe")); !errors.Is(err, syscall.EXDEV) {
		t.Skipf("test requires separate filesystems; rename returned %v", err)
	}

	for _, separate := range []string{"database", "uploads"} {
		t.Run(separate, func(t *testing.T) {
			base := filepath.Join(dir, separate)
			dbPath := filepath.Join(base, "pappice.db")
			uploadDir := filepath.Join(base, "uploads")
			backupDir := filepath.Join(base, "backups")
			if separate == "database" {
				dbPath = filepath.Join(other, "pappice.db")
			} else {
				uploadDir = filepath.Join(other, "uploads")
			}
			createTestDB(t, dbPath, "backup")
			writeFile(t, filepath.Join(uploadDir, "attachment.txt"), "backup attachment")
			backup, err := Create(Config{DBPath: dbPath, UploadDir: uploadDir, BackupDir: backupDir})
			if err != nil {
				t.Fatal(err)
			}
			createTestDB(t, dbPath, "current")
			writeFile(t, filepath.Join(uploadDir, "attachment.txt"), "current attachment")
			result, err := Restore(RestoreConfig{
				DBPath: dbPath, UploadDir: uploadDir + string(filepath.Separator), BackupDir: backupDir, BackupPath: backup.Path,
			})
			if err != nil {
				t.Fatalf("restore across filesystems: %v", err)
			}
			if got := queryTestDB(t, dbPath); got != "backup" {
				t.Fatalf("restored database = %q", got)
			}
			if got := readFile(t, filepath.Join(uploadDir, "attachment.txt")); got != "backup attachment" {
				t.Fatalf("restored attachment = %q", got)
			}
			if got := queryTestDB(t, filepath.Join(result.DatabaseSafetyDir, "pappice.db")); got != "current" {
				t.Fatalf("saved original database = %q", got)
			}
			if got := readFile(t, filepath.Join(result.UploadSafetyDir, "uploads", "attachment.txt")); got != "current attachment" {
				t.Fatalf("saved original attachment = %q", got)
			}
		})
	}
}

func TestRestoreExcludesDatabaseOperations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pappice.db")
	tracker, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DBPath: path, UploadDir: filepath.Join(dir, "uploads"), BackupDir: filepath.Join(dir, "backups")}
	snapshot, err := Create(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Pause restore after it has acquired exclusivity, before installing the DB.
	restoreCfg := RestoreConfig{
		DBPath: path, UploadDir: cfg.UploadDir, BackupDir: cfg.BackupDir, BackupPath: snapshot.Path,
		Now: func() time.Time {
			checks := map[string]func() error{
				"open": func() error {
					opened, err := store.Open(path)
					if opened != nil {
						_ = opened.Close()
					}
					return err
				},
				"status":  func() error { _, err := store.InspectMigration(path); return err },
				"migrate": func() error { _, err := store.Migrate(path, store.MigrationOptions{}); return err },
				"backup":  func() error { _, err := Create(cfg); return err },
				"restore": func() error {
					_, err := Restore(RestoreConfig{DBPath: path, UploadDir: cfg.UploadDir, BackupDir: cfg.BackupDir, BackupPath: snapshot.Path})
					return err
				},
			}
			for name, check := range checks {
				if err := check(); !errors.Is(err, dblock.ErrInUse) {
					t.Errorf("%s during restore: %v", name, err)
				}
			}
			return time.Now()
		},
	}
	if _, err := Restore(restoreCfg); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreThroughDatabaseSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pappice.db")
	alias := filepath.Join(dir, "alias.db")
	createTestDB(t, path, "before")
	if err := os.Symlink(path, alias); err != nil {
		t.Skip(err)
	}
	cfg := Config{DBPath: path, UploadDir: filepath.Join(dir, "uploads"), BackupDir: filepath.Join(dir, "backups")}
	snapshot, err := Create(cfg)
	if err != nil {
		t.Fatal(err)
	}
	createTestDB(t, path, "after")
	if _, err := Restore(RestoreConfig{
		DBPath: alias, UploadDir: cfg.UploadDir, BackupDir: cfg.BackupDir, BackupPath: snapshot.Path,
	}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(alias); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("restore replaced the database symlink: %v", err)
	}
	if got := queryTestDB(t, path); got != "before" {
		t.Fatalf("restore did not replace the database target: %q", got)
	}
}
