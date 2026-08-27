package server

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) handleAdminMaintenance(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	emailStats, err := s.store.EmailNotificationStats()
	if err != nil {
		respondStoreError(w, err)
		return
	}
	databaseSize, err := s.store.DatabaseSizeBytes()
	if err != nil {
		respondStoreError(w, err)
		return
	}
	attachmentSize, err := directorySize(s.options.UploadDir)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"version":                        s.options.Version,
		"started_at":                     s.started,
		"database_path":                  s.store.Path(),
		"database_size_bytes":            databaseSize,
		"upload_path":                    s.options.UploadDir,
		"attachment_storage_bytes":       attachmentSize,
		"domain_event_retention_seconds": int(s.options.DomainEventRetention.Seconds()),
		"backup":                         backupStatus(s.options.BackupDir),
		"uploads":                        s.publicUploadConfig(),
		"email": map[string]any{
			"enabled":                    s.options.EmailNotifications,
			"public_url":                 strings.TrimSpace(s.options.PublicURL),
			"notification_delay_seconds": int(s.options.NotificationDelay.Seconds()),
			"stats":                      emailStats,
		},
	})
}

func directorySize(path string) (int64, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var size int64
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func backupStatus(dir string) map[string]any {
	status := map[string]any{
		"path": strings.TrimSpace(dir),
	}
	if status["path"] == "" {
		status["path"] = defaultBackupDir
	}
	entries, err := os.ReadDir(status["path"].(string))
	if err != nil {
		if os.IsNotExist(err) {
			status["latest_name"] = ""
			return status
		}
		status["error"] = err.Error()
		return status
	}

	var newestName string
	var newestPath string
	var newestTime time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidatePath := filepath.Join(status["path"].(string), entry.Name())
		if _, err := os.Stat(filepath.Join(candidatePath, "pappice.db")); err != nil {
			continue
		}
		if newestName == "" || info.ModTime().After(newestTime) {
			newestName = entry.Name()
			newestPath = candidatePath
			newestTime = info.ModTime()
		}
	}
	status["latest_name"] = newestName
	if newestName != "" {
		status["latest_path"] = newestPath
		status["latest_at"] = newestTime.UTC()
	}
	return status
}
