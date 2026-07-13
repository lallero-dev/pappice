package backup

import (
	"errors"
	"fmt"
	"log"
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
	BackupPath string
	SafetyDir  string
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
			if err := os.RemoveAll(tempPath); err != nil {
				log.Printf("failed to remove temp path %s: %v", tempPath, err)
			}
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
	backupPath, err := ResolvePath(cfg.BackupDir, cfg.BackupPath)
	if err != nil {
		return RestoreResult{}, err
	}
	backupDBPath := filepath.Join(backupPath, "pappice.db")
	if err := requireRegularFile(backupDBPath); err != nil {
		return RestoreResult{}, fmt.Errorf("backup database: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o750); err != nil {
		return RestoreResult{}, err
	}
	tempDBPath, err := availablePath(filepath.Dir(cfg.DBPath), "."+filepath.Base(cfg.DBPath)+".restore-tmp")
	if err != nil {
		return RestoreResult{}, err
	}
	tempDBInstalled := false
	if err := copyFile(backupDBPath, tempDBPath, 0o600); err != nil {
		return RestoreResult{}, err
	}
	defer func() {
		if !tempDBInstalled {
			if err := os.Remove(tempDBPath); err != nil {
				log.Printf("failed to remove temp db path %s: %v", tempDBPath, err)
			}
		}
	}()

	tempUploadDir, err := prepareRestoreUploads(backupPath, cfg.UploadDir)
	if err != nil {
		return RestoreResult{}, err
	}
	tempUploadsInstalled := false
	defer func() {
		if !tempUploadsInstalled {
			if err := os.RemoveAll(tempUploadDir); err != nil {
				log.Printf("failed to remove temp upload dir %s: %v", tempUploadDir, err)
			}
		}
	}()

	if err := os.MkdirAll(cfg.BackupDir, 0o750); err != nil {
		return RestoreResult{}, err
	}
	safetyDir, err := createUniqueDir(cfg.BackupDir, "restore-pre-"+cfg.now().Format(timestampFormat))
	if err != nil {
		return RestoreResult{}, err
	}

	if err := moveIfExists(cfg.DBPath, filepath.Join(safetyDir, filepath.Base(cfg.DBPath))); err != nil {
		return RestoreResult{}, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		path := cfg.DBPath + suffix
		if err := moveIfExists(path, filepath.Join(safetyDir, filepath.Base(path))); err != nil {
			return RestoreResult{}, err
		}
	}
	if err := os.Rename(tempDBPath, cfg.DBPath); err != nil {
		return RestoreResult{}, err
	}
	tempDBInstalled = true

	if err := moveIfExists(cfg.UploadDir, filepath.Join(safetyDir, "uploads")); err != nil {
		return RestoreResult{}, err
	}
	if err := os.Rename(tempUploadDir, cfg.UploadDir); err != nil {
		return RestoreResult{}, err
	}
	tempUploadsInstalled = true

	return RestoreResult{BackupPath: backupPath, SafetyDir: safetyDir}, nil
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
