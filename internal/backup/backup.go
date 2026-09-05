package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const timestampFormat = "20060102T150405Z"

type Config struct {
	DBPath    string
	UploadDir string
	BackupDir string
	Now       func() time.Time
}

type Result struct {
	Path              string
	DatabasePath      string
	UploadArchivePath string
	HasUploads        bool
}

type RestoreConfig struct {
	DBPath     string
	UploadDir  string
	BackupDir  string
	BackupPath string
	Now        func() time.Time
}

type RestoreResult struct {
	BackupPath        string
	DatabaseSafetyDir string
	UploadSafetyDir   string
}

func Create(cfg Config) (Result, error) {
	cfg = normalizeConfig(cfg)
	if err := validateBackupConfig(cfg); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(cfg.BackupDir, 0o750); err != nil {
		return Result{}, err
	}

	timestamp := cfg.now().Format(timestampFormat)
	finalPath, err := availablePath(cfg.BackupDir, timestamp)
	if err != nil {
		return Result{}, err
	}
	tempPath, err := createUniqueDir(cfg.BackupDir, "."+filepath.Base(finalPath)+".tmp")
	if err != nil {
		return Result{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tempPath)
		}
	}()

	result := Result{
		Path:         finalPath,
		DatabasePath: filepath.Join(finalPath, "pappice.db"),
	}
	tempDBPath := filepath.Join(tempPath, "pappice.db")
	if err := copySQLiteDatabase(cfg.DBPath, tempDBPath); err != nil {
		return Result{}, err
	}

	hasUploads, err := uploadDirExists(cfg.UploadDir)
	if err != nil {
		return Result{}, err
	}
	if hasUploads {
		tempUploadsPath := filepath.Join(tempPath, "uploads.tar")
		if err := createUploadsArchive(cfg.UploadDir, tempUploadsPath); err != nil {
			return Result{}, err
		}
		result.HasUploads = true
		result.UploadArchivePath = filepath.Join(finalPath, "uploads.tar")
	}

	if err := writeManifest(filepath.Join(tempPath, "manifest.env"), cfg, timestamp, result.HasUploads); err != nil {
		return Result{}, err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return Result{}, err
	}
	committed = true
	return result, nil
}

func Restore(cfg RestoreConfig) (RestoreResult, error) {
	cfg = normalizeRestoreConfig(cfg)
	if err := validateRestoreConfig(cfg); err != nil {
		return RestoreResult{}, err
	}
	cfg.DBPath = filepath.Clean(cfg.DBPath)
	cfg.UploadDir = filepath.Clean(cfg.UploadDir)
	backupPath, err := ResolvePath(cfg.BackupDir, cfg.BackupPath)
	if err != nil {
		return RestoreResult{}, err
	}
	backupDBPath := filepath.Join(backupPath, "pappice.db")
	if err := requireRegularFile(backupDBPath); err != nil {
		return RestoreResult{}, fmt.Errorf("backup database: %w", err)
	}

	dbParent := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbParent, 0o750); err != nil {
		return RestoreResult{}, err
	}
	tempDBDir, err := os.MkdirTemp(dbParent, "."+filepath.Base(cfg.DBPath)+".restore-tmp-")
	if err != nil {
		return RestoreResult{}, err
	}
	defer os.RemoveAll(tempDBDir)
	tempDBPath := filepath.Join(tempDBDir, filepath.Base(cfg.DBPath))
	if err := copyFile(backupDBPath, tempDBPath, 0o600); err != nil {
		return RestoreResult{}, err
	}

	tempUploadDir, err := prepareRestoreUploads(backupPath, cfg.UploadDir)
	if err != nil {
		return RestoreResult{}, err
	}
	defer os.RemoveAll(tempUploadDir)

	stamp := cfg.now().Format(timestampFormat)
	dbSafetyDir, err := createUniqueDir(dbParent, "."+filepath.Base(cfg.DBPath)+".restore-pre-"+stamp)
	if err != nil {
		return RestoreResult{}, err
	}
	uploadSafetyDir, err := createUniqueDir(filepath.Dir(cfg.UploadDir), "."+filepath.Base(cfg.UploadDir)+".restore-pre-"+stamp)
	if err != nil {
		_ = os.Remove(dbSafetyDir)
		return RestoreResult{}, err
	}

	moves := []restoreMove{
		{cfg.DBPath, filepath.Join(dbSafetyDir, filepath.Base(cfg.DBPath)), true},
		{cfg.DBPath + "-wal", filepath.Join(dbSafetyDir, filepath.Base(cfg.DBPath)+"-wal"), true},
		{cfg.DBPath + "-shm", filepath.Join(dbSafetyDir, filepath.Base(cfg.DBPath)+"-shm"), true},
		{cfg.UploadDir, filepath.Join(uploadSafetyDir, "uploads"), true},
		{tempDBPath, cfg.DBPath, false},
		{tempUploadDir, cfg.UploadDir, false},
	}
	if err := moveRestoreFiles(moves, os.Rename); err != nil {
		// Remove empty directories after rollback, but never saved originals.
		_ = os.Remove(dbSafetyDir)
		_ = os.Remove(uploadSafetyDir)
		return RestoreResult{}, err
	}
	return RestoreResult{
		BackupPath:        backupPath,
		DatabaseSafetyDir: dbSafetyDir,
		UploadSafetyDir:   uploadSafetyDir,
	}, nil
}

func ResolvePath(backupDir, target string) (string, error) {
	backupDir = strings.TrimSpace(backupDir)
	target = strings.TrimSpace(target)
	if target == "" || target == "latest" {
		return latestBackupPath(backupDir)
	}
	return target, nil
}

func normalizeConfig(cfg Config) Config {
	cfg.DBPath = strings.TrimSpace(cfg.DBPath)
	cfg.UploadDir = strings.TrimSpace(cfg.UploadDir)
	cfg.BackupDir = strings.TrimSpace(cfg.BackupDir)
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return cfg
}

func normalizeRestoreConfig(cfg RestoreConfig) RestoreConfig {
	cfg.DBPath = strings.TrimSpace(cfg.DBPath)
	cfg.UploadDir = strings.TrimSpace(cfg.UploadDir)
	cfg.BackupDir = strings.TrimSpace(cfg.BackupDir)
	cfg.BackupPath = strings.TrimSpace(cfg.BackupPath)
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return cfg
}

func (cfg Config) now() time.Time {
	return cfg.Now().UTC()
}

func (cfg RestoreConfig) now() time.Time {
	return cfg.Now().UTC()
}

func validateBackupConfig(cfg Config) error {
	if cfg.DBPath == "" {
		return errors.New("database path is required")
	}
	if cfg.DBPath == ":memory:" {
		return errors.New("cannot back up an in-memory database")
	}
	if cfg.UploadDir == "" {
		return errors.New("upload directory is required")
	}
	if cfg.BackupDir == "" {
		return errors.New("backup directory is required")
	}
	if !strings.HasPrefix(cfg.DBPath, "file:") {
		if err := requireRegularFile(cfg.DBPath); err != nil {
			return fmt.Errorf("database: %w", err)
		}
	}
	return nil
}

func validateRestoreConfig(cfg RestoreConfig) error {
	if cfg.DBPath == "" {
		return errors.New("database path is required")
	}
	if cfg.DBPath == ":memory:" || strings.HasPrefix(cfg.DBPath, "file:") {
		return errors.New("restore requires a filesystem database path")
	}
	if cfg.UploadDir == "" {
		return errors.New("upload directory is required")
	}
	if cfg.BackupDir == "" {
		return errors.New("backup directory is required")
	}
	return nil
}
