package backup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func requireRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
}

func uploadDirExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("upload path is not a directory: %s", path)
	}
	return true, nil
}

func prepareRestoreUploads(backupPath, uploadDir string) (string, error) {
	parent := filepath.Dir(uploadDir)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", err
	}
	tempUploadDir, err := createUniqueDir(parent, "."+filepath.Base(uploadDir)+".restore-tmp")
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			if err := os.RemoveAll(tempUploadDir); err != nil {
				log.Printf("failed to remove temp upload directory %s: %v", tempUploadDir, err)
			}
		}
	}()
	uploadsArchive := filepath.Join(backupPath, "uploads.tar")
	if err := requireRegularFile(uploadsArchive); err == nil {
		if err := extractUploadsArchive(uploadsArchive, tempUploadDir); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("uploads archive: %w", err)
	}
	committed = true
	return tempUploadDir, nil
}

func writeManifest(path string, cfg Config, timestamp string, hasUploads bool) error {
	content := fmt.Sprintf("created_at=%s\ndb_path=%s\nupload_dir=%s\nhas_uploads=%t\n", timestamp, cfg.DBPath, cfg.UploadDir, hasUploads)
	return os.WriteFile(path, []byte(content), 0o600)
}

func latestBackupPath(backupDir string) (string, error) {
	if backupDir == "" {
		return "", errors.New("backup directory is required")
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return "", err
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "restore-pre-") {
			continue
		}
		candidate := filepath.Join(backupDir, name)
		err := requireRegularFile(filepath.Join(candidate, "pappice.db"))
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("candidate backup %s: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("no backups found in %s", backupDir)
}

func availablePath(parent, name string) (string, error) {
	for index := range 100 {
		candidate := filepath.Join(parent, name)
		if index > 0 {
			candidate = filepath.Join(parent, fmt.Sprintf("%s-%d", name, index))
		}
		_, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not find available path for %s", name)
}

func createUniqueDir(parent, name string) (string, error) {
	for index := range 100 {
		candidate := filepath.Join(parent, name)
		if index > 0 {
			candidate = filepath.Join(parent, fmt.Sprintf("%s-%d", name, index))
		}
		err := os.Mkdir(candidate, 0o750)
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not create unique directory for %s", name)
}

func moveIfExists(source, destination string) error {
	_, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.Rename(source, destination)
}

func copyFile(source, destination string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
