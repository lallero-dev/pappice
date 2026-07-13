package server

import (
	"io/fs"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

func (s *Server) routes() {
	staticFiles, err := fs.Sub(assets, "web/static")
	if err != nil {
		panic(err)
	}

	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/account/", s.handleAccountIndex)
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))

	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/session", s.handleSession)
	s.mux.HandleFunc("/api/me", s.handleMe)
	s.mux.HandleFunc("/api/me/password", s.handleMePassword)
	s.mux.HandleFunc("/api/setup", s.handleSetup)
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/logout", s.handleLogout)
	s.mux.HandleFunc("/api/account-links/", s.handleAccountLinkByToken)
	s.mux.HandleFunc("/api/products", s.handleProducts)
	s.mux.HandleFunc("/api/products/", s.handleProductByID)
	s.mux.HandleFunc("/api/tickets", s.handleTickets)
	s.mux.HandleFunc("/api/tickets/", s.handleTicketPath)
	s.mux.HandleFunc("/api/attachments/", s.handleAttachmentByID)
	s.mux.HandleFunc("/api/users", s.handleUsers)
	s.mux.HandleFunc("/api/users/", s.handleUserByID)
	s.mux.HandleFunc("/api/tokens", s.handleTokens)
	s.mux.HandleFunc("/api/tokens/", s.handleTokenByID)
	s.mux.HandleFunc("/api/webhooks", s.handleWebhooks)
	s.mux.HandleFunc("/api/webhooks/", s.handleWebhookByID)
	s.mux.HandleFunc("/api/webhook-deliveries", s.handleWebhookDeliveries)
	s.mux.HandleFunc("/api/email-notifications", s.handleEmailNotifications)
	s.mux.HandleFunc("/api/email-notifications/", s.handleEmailNotificationByID)
	s.mux.HandleFunc("/api/audit-events", s.handleAuditEvents)
	s.mux.HandleFunc("/api/admin/maintenance", s.handleAdminMaintenance)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !isAppIndexPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	content, err := assets.ReadFile("web/index.html")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "index asset not found")
		return
	}
	if _, err := w.Write(content); err != nil {
		log.Printf("failed to write index response: %v", err)
	}
}

func isAppIndexPath(path string) bool {
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	switch path {
	case "/", "/tickets", "/admin", "/products":
		return true
	}
	if after, ok := strings.CutPrefix(path, "/admin/"); ok {
		return slices.Contains(appAdminSections, after)
	}
	if after, ok := strings.CutPrefix(path, "/products/"); ok {
		parts := strings.Split(strings.Trim(after, "/"), "/")
		if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
			return false
		}
		parsed, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || parsed < 1 {
			return false
		}
		if len(parts) == 1 {
			return true
		}
		return slices.Contains(appProductSections, parts[1])
	}
	return false
}

func (s *Server) handleAccountIndex(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/account/setup/") && !strings.HasPrefix(r.URL.Path, "/account/reset/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	content, err := assets.ReadFile("web/index.html")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "index asset not found")
		return
	}
	if _, err := w.Write(content); err != nil {
		log.Printf("failed to write index response: %v", err)
	}
}
