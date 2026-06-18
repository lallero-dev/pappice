package backup

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateAndRestoreBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pappice.db")
	uploadDir := filepath.Join(dir, "uploads")
	backupDir := filepath.Join(dir, "backups")
	createTestDB(t, dbPath, "before")
	writeFile(t, filepath.Join(uploadDir, "tickets", "one.txt"), "attachment before")

	now := fixedTime(2026, 1, 2, 3, 4, 5)
	result, err := Create(Config{
		DBPath:    dbPath,
		UploadDir: uploadDir,
		BackupDir: backupDir,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if result.Path != filepath.Join(backupDir, "20260102T030405Z") {
		t.Fatalf("backup path = %q", result.Path)
	}
	if !result.HasUploads {
		t.Fatal("backup should include uploads")
	}
	if got := queryTestDB(t, result.DatabasePath); got != "before" {
		t.Fatalf("backup database value = %q", got)
	}
	if _, err := os.Stat(result.UploadArchivePath); err != nil {
		t.Fatalf("uploads archive missing: %v", err)
	}

	createTestDB(t, dbPath, "after")
	writeFile(t, filepath.Join(uploadDir, "stale.txt"), "stale upload")
	restore, err := Restore(RestoreConfig{
		DBPath:     dbPath,
		UploadDir:  uploadDir,
		BackupDir:  backupDir,
		BackupPath: "latest",
		Now:        fixedTime(2026, 1, 2, 4, 0, 0),
	})
	if err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	if restore.BackupPath != result.Path {
		t.Fatalf("restore backup path = %q, want %q", restore.BackupPath, result.Path)
	}
	if got := queryTestDB(t, dbPath); got != "before" {
		t.Fatalf("restored database value = %q", got)
	}
	if got := readFile(t, filepath.Join(uploadDir, "tickets", "one.txt")); got != "attachment before" {
		t.Fatalf("restored upload = %q", got)
	}
	if _, err := os.Stat(filepath.Join(uploadDir, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale upload remained in restored upload dir: %v", err)
	}
	if got := readFile(t, filepath.Join(restore.SafetyDir, "uploads", "stale.txt")); got != "stale upload" {
		t.Fatalf("safety upload = %q", got)
	}
	latest, err := ResolvePath(backupDir, "latest")
	if err != nil {
		t.Fatalf("resolve latest after restore: %v", err)
	}
	if latest != result.Path {
		t.Fatalf("latest after restore = %q, want %q", latest, result.Path)
	}
}

func TestRestoreBackupWithoutUploadsReplacesStaleUploads(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pappice.db")
	uploadDir := filepath.Join(dir, "uploads")
	backupDir := filepath.Join(dir, "backups")
	createTestDB(t, dbPath, "db-only")

	result, err := Create(Config{
		DBPath:    dbPath,
		UploadDir: uploadDir,
		BackupDir: backupDir,
		Now:       fixedTime(2026, 2, 3, 4, 5, 6),
	})
	if err != nil {
		t.Fatalf("create db-only backup: %v", err)
	}
	if result.HasUploads || result.UploadArchivePath != "" {
		t.Fatalf("db-only backup reported uploads: %#v", result)
	}

	createTestDB(t, dbPath, "changed")
	writeFile(t, filepath.Join(uploadDir, "stale.txt"), "stale")
	restore, err := Restore(RestoreConfig{
		DBPath:     dbPath,
		UploadDir:  uploadDir,
		BackupDir:  backupDir,
		BackupPath: result.Path,
		Now:        fixedTime(2026, 2, 3, 5, 0, 0),
	})
	if err != nil {
		t.Fatalf("restore db-only backup: %v", err)
	}
	if got := queryTestDB(t, dbPath); got != "db-only" {
		t.Fatalf("restored database value = %q", got)
	}
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		t.Fatalf("read restored upload dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("db-only restore left uploads behind: %#v", entries)
	}
	if got := readFile(t, filepath.Join(restore.SafetyDir, "uploads", "stale.txt")); got != "stale" {
		t.Fatalf("safety stale upload = %q", got)
	}
}

func TestRestoreWithInvalidUploadsArchiveDoesNotReplaceCurrentFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pappice.db")
	uploadDir := filepath.Join(dir, "uploads")
	backupDir := filepath.Join(dir, "backups")
	createTestDB(t, dbPath, "backup")
	result, err := Create(Config{
		DBPath:    dbPath,
		UploadDir: uploadDir,
		BackupDir: backupDir,
		Now:       fixedTime(2026, 3, 4, 5, 6, 7),
	})
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	writeFile(t, filepath.Join(result.Path, "uploads.tar"), "not a tar archive")

	createTestDB(t, dbPath, "current")
	writeFile(t, filepath.Join(uploadDir, "current.txt"), "current upload")
	if _, err := Restore(RestoreConfig{
		DBPath:     dbPath,
		UploadDir:  uploadDir,
		BackupDir:  backupDir,
		BackupPath: result.Path,
		Now:        fixedTime(2026, 3, 4, 6, 0, 0),
	}); err == nil {
		t.Fatal("restore should fail for invalid uploads archive")
	}
	if got := queryTestDB(t, dbPath); got != "current" {
		t.Fatalf("database changed after failed restore: %q", got)
	}
	if got := readFile(t, filepath.Join(uploadDir, "current.txt")); got != "current upload" {
		t.Fatalf("upload changed after failed restore: %q", got)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "restore-pre-") {
			t.Fatalf("failed restore created safety dir %q", entry.Name())
		}
	}
}

func TestCleanArchivePath(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		wantSkip  bool
		wantError bool
	}{
		{name: "regular path", input: "tickets/one.txt", want: "tickets/one.txt"},
		{name: "leading dot", input: "./tickets/one.txt", want: "tickets/one.txt"},
		{name: "root", input: ".", wantSkip: true},
		{name: "parent root", input: "..", wantError: true},
		{name: "parent", input: "../outside.txt", wantError: true},
		{name: "absolute", input: "/outside.txt", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, skip, err := cleanArchivePath(test.input)
			if test.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("clean archive path: %v", err)
			}
			if skip != test.wantSkip {
				t.Fatalf("skip = %t, want %t", skip, test.wantSkip)
			}
			if got != test.want {
				t.Fatalf("path = %q, want %q", got, test.want)
			}
		})
	}
}

func createTestDB(t *testing.T, path, value string) {
	t.Helper()
	for _, item := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(item); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove test db file: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create db dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records (value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create records table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO records (value) VALUES (?)`, value); err != nil {
		t.Fatalf("insert record: %v", err)
	}
}

func queryTestDB(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open query db: %v", err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM records`).Scan(&value); err != nil {
		t.Fatalf("query record: %v", err)
	}
	return value
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create file dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func fixedTime(year int, month time.Month, day, hour, minute, second int) func() time.Time {
	return func() time.Time {
		return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
	}
}
