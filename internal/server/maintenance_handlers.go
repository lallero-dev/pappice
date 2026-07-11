package server

import (
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
	respondJSON(w, http.StatusOK, map[string]any{
		"version":                        s.options.Version,
		"started_at":                     s.started,
		"database_path":                  s.store.Path(),
		"upload_path":                    s.options.UploadDir,
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
