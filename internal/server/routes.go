package server

import (
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

func (s *Server) routes() *http.ServeMux {
	staticFiles, err := fs.Sub(assets, "web/static")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/account/", s.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))

	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/api/me", s.handleMe)
	mux.HandleFunc("/api/me/password", s.handleMePassword)
	mux.HandleFunc("/api/setup", s.handleSetup)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/account-links/", s.handleAccountLinkByToken)
	mux.HandleFunc("/api/products", s.handleProducts)
	mux.HandleFunc("/api/products/", s.handleProductByID)
	mux.HandleFunc("/api/tickets", s.handleTickets)
	mux.HandleFunc("/api/tickets/", s.handleTicketPath)
	mux.HandleFunc("/api/attachments/", s.handleAttachmentByID)
	mux.HandleFunc("/api/users", s.handleUsers)
	mux.HandleFunc("/api/users/", s.handleUserByID)
	mux.HandleFunc("/api/tokens", s.handleTokens)
	mux.HandleFunc("/api/tokens/", s.handleTokenByID)
	mux.HandleFunc("/api/webhooks", s.handleWebhooks)
	mux.HandleFunc("/api/webhooks/", s.handleWebhookByID)
	mux.HandleFunc("/api/webhook-deliveries", s.handleWebhookDeliveries)
	mux.HandleFunc("/api/email-notifications", s.handleEmailNotifications)
	mux.HandleFunc("/api/email-notifications/", s.handleEmailNotificationByID)
	mux.HandleFunc("/api/audit-events", s.handleAuditEvents)
	mux.HandleFunc("/api/admin/maintenance", s.handleAdminMaintenance)
	return mux
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
	_, _ = w.Write(content)
}

func isAppIndexPath(path string) bool {
	if strings.HasPrefix(path, "/account/setup/") || strings.HasPrefix(path, "/account/reset/") {
		return true
	}
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
		if len(parts) > 2 || parts[0] == "" {
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
