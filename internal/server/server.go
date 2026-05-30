package server

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"pappice/internal/security"
	"pappice/internal/store"
)

//go:embed web/index.html web/static/*
var assets embed.FS

const (
	sessionCookieName      = "pappice_session"
	defaultEmailBatchDelay = 20 * time.Second
	accountLinkExpiry      = 24 * time.Hour
	defaultSessionTTL      = 14 * 24 * time.Hour
	defaultBrandName       = "Pappice"
	defaultBrandSubtitle   = "customer support"
	defaultBrandColor      = "#5bb974"
	defaultUploadDir       = "pappice-uploads"
	defaultBackupDir       = "pappice-backups"
	defaultMaxUploadSize   = 10 << 20
	defaultMaxUploadFiles  = 5
	defaultVersion         = "dev"
)

var (
	appAdminSections   = []string{"accounts", "tokens", "webhooks", "email", "maintenance", "audit"}
	appProductSections = []string{"members", "webhooks", "deliveries"}
)

type RateLimit struct {
	Limit  int
	Window time.Duration
}

type Options struct {
	AllowInsecureWebhooks bool
	AllowPrivateWebhooks  bool
	Branding              Branding
	EmailNotifications    bool
	EmailBatchDelay       time.Duration
	PublicURL             string
	SessionTTL            time.Duration
	Version               string
	UploadDir             string
	BackupDir             string
	MaxUploadSize         int64
	MaxUploadFiles        int
	AllowedUploadTypes    []string
	LoginRateLimit        RateLimit
	AccountLinkRateLimit  RateLimit
}

type Branding struct {
	Name     string `json:"name"`
	Subtitle string `json:"subtitle"`
	Mark     string `json:"mark"`
	Color    string `json:"color"`
}

type Server struct {
	store              *store.Store
	started            time.Time
	mux                *http.ServeMux
	handler            http.Handler
	client             *http.Client
	options            Options
	loginLimiter       *requestLimiter
	accountLinkLimiter *requestLimiter
}

type authContext struct {
	User         store.User
	CSRF         string
	SessionToken string
	ViaToken     bool
}

type ticketPatchInput struct {
	Title       *string           `json:"title"`
	Description *string           `json:"description"`
	Status      *string           `json:"status"`
	Priority    *string           `json:"priority"`
	Assignee    *string           `json:"assignee"`
	Comment     *store.AddComment `json:"comment"`
}

func (input ticketPatchInput) updateTicket() store.UpdateTicket {
	return store.UpdateTicket{
		Title:       input.Title,
		Description: input.Description,
		Status:      input.Status,
		Priority:    input.Priority,
		Assignee:    input.Assignee,
	}
}

func (input ticketPatchInput) hasTicketPatch() bool {
	return input.Title != nil || input.Description != nil || input.Status != nil || input.Priority != nil || input.Assignee != nil
}

func New(tracker *store.Store, opts ...Options) http.Handler {
	return NewServer(tracker, opts...)
}

func NewServer(tracker *store.Store, opts ...Options) *Server {
	options := Options{}
	if len(opts) > 0 {
		options = opts[0]
	}
	if options.EmailBatchDelay <= 0 {
		options.EmailBatchDelay = defaultEmailBatchDelay
	}
	if options.SessionTTL <= 0 {
		options.SessionTTL = defaultSessionTTL
	}
	options.Version = strings.TrimSpace(options.Version)
	if options.Version == "" {
		options.Version = defaultVersion
	}
	options.Branding = normalizeBranding(options.Branding)
	options = normalizeUploadOptions(options)
	options.BackupDir = strings.TrimSpace(options.BackupDir)
	if options.BackupDir == "" {
		options.BackupDir = defaultBackupDir
	}
	options.LoginRateLimit = withDefaultRateLimit(options.LoginRateLimit, 10, time.Minute)
	options.AccountLinkRateLimit = withDefaultRateLimit(options.AccountLinkRateLimit, 10, time.Minute)
	s := &Server{
		store:              tracker,
		started:            time.Now().UTC(),
		mux:                http.NewServeMux(),
		options:            options,
		loginLimiter:       newRequestLimiter(options.LoginRateLimit),
		accountLinkLimiter: newRequestLimiter(options.AccountLinkRateLimit),
	}
	s.client = s.newWebhookClient()
	s.routes()
	s.handler = securityHeaders(s.mux)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

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
	_, _ = w.Write(content)
}

func isAppIndexPath(path string) bool {
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	switch path {
	case "/", "/tickets", "/admin", "/products":
		return true
	}
	if strings.HasPrefix(path, "/admin/") {
		return slices.Contains(appAdminSections, strings.TrimPrefix(path, "/admin/"))
	}
	if strings.HasPrefix(path, "/products/") {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/products/"), "/"), "/")
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
	_, _ = w.Write(content)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"name":           "pappice",
		"branding":       s.options.Branding,
		"started_at":     s.started,
		"needs_setup":    s.store.SetupRequired(),
		"statuses":       store.Statuses(),
		"priorities":     store.Priorities(),
		"roles":          store.Roles(),
		"product_roles":  store.ProductRoles(),
		"webhook_events": store.Events(),
		"email_enabled":  s.options.EmailNotifications,
		"uploads":        s.publicUploadConfig(),
	})
}

func (s *Server) handleAdminMaintenance(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"version":       s.options.Version,
		"started_at":    s.started,
		"database_path": s.store.Path(),
		"upload_path":   s.options.UploadDir,
		"backup":        backupStatus(s.options.BackupDir),
		"uploads":       s.publicUploadConfig(),
		"email": map[string]any{
			"enabled":             s.options.EmailNotifications,
			"public_url":          strings.TrimSpace(s.options.PublicURL),
			"batch_delay_seconds": int(s.options.EmailBatchDelay.Seconds()),
			"stats":               s.store.EmailNotificationStats(),
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

func normalizeBranding(branding Branding) Branding {
	branding.Name = strings.TrimSpace(branding.Name)
	if branding.Name == "" {
		branding.Name = defaultBrandName
	}
	branding.Subtitle = strings.TrimSpace(branding.Subtitle)
	if branding.Subtitle == "" {
		branding.Subtitle = defaultBrandSubtitle
	}
	branding.Mark = normalizeBrandMark(branding.Mark, branding.Name)
	branding.Color = strings.TrimSpace(branding.Color)
	if !isHexColor(branding.Color) {
		branding.Color = defaultBrandColor
	}
	return branding
}

func normalizeBrandMark(mark, name string) string {
	mark = strings.TrimSpace(mark)
	if mark == "" {
		for _, char := range name {
			return strings.ToUpper(string(char))
		}
		return "P"
	}
	runes := []rune(mark)
	if len(runes) > 3 {
		runes = runes[:3]
	}
	return string(runes)
}

func isHexColor(value string) bool {
	if len(value) != 4 && len(value) != 7 {
		return false
	}
	if value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') {
			continue
		}
		return false
	}
	return true
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	auth, ok := s.currentAuth(r)
	respondJSON(w, http.StatusOK, map[string]any{
		"authenticated": ok,
		"needs_setup":   s.store.SetupRequired(),
		"user":          nullableUser(auth.User, ok),
		"csrf_token":    nullableString(auth.CSRF, ok && !auth.ViaToken),
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleSession(w, r)
		return
	}
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPatch {
		methodNotAllowed(w, http.MethodGet, http.MethodPatch)
		return
	}
	var input struct {
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	updated, err := s.store.UpdateUser(auth.User.ID, store.UpdateUser{
		DisplayName: input.DisplayName,
		Email:       input.Email,
		Event:       s.eventContext(r, auth.User),
	})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, store.ToPublicUser(updated))
}

func (s *Server) handleMePassword(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if auth.ViaToken || strings.TrimSpace(auth.SessionToken) == "" {
		respondError(w, http.StatusForbidden, "browser session is required")
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	updated, err := s.store.ChangePassword(auth.User.ID, input.CurrentPassword, input.NewPassword, auth.SessionToken, s.eventContext(r, auth.User))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, map[string]any{
		"user":       store.ToPublicUser(updated),
		"csrf_token": auth.CSRF,
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.requireHTTPS(w, r) {
		return
	}
	var input store.CreateUser
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Event = store.EventContext{Enabled: true, IP: clientIP(r)}
	user, err := s.store.CreateFirstAdmin(input)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	csrf, ok := s.createSession(w, user.ID)
	if !ok {
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusCreated, map[string]any{
		"user":       store.ToPublicUser(user),
		"csrf_token": csrf,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.requireHTTPS(w, r) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	limitKey := "login|" + clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(input.Username))
	if !s.loginLimiter.Allow(limitKey, time.Now().UTC()) {
		respondRateLimited(w)
		return
	}
	user, err := s.store.Authenticate(input.Username, input.Password)
	if err != nil {
		if errors.Is(err, store.ErrPasswordResetRequired) {
			respondError(w, http.StatusUnauthorized, "password setup or reset is required; use the emailed link or contact an admin")
			return
		}
		respondError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	csrf, ok := s.createSession(w, user.ID)
	if !ok {
		return
	}
	s.loginLimiter.Reset(limitKey)
	respondJSON(w, http.StatusOK, map[string]any{
		"user":       store.ToPublicUser(user),
		"csrf_token": csrf,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	_ = auth
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.store.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAccountLinkByToken(w http.ResponseWriter, r *http.Request) {
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/account-links/"), "/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	limitKey := "account-link|" + clientIP(r) + "|" + security.HashToken(token)
	switch r.Method {
	case http.MethodGet:
		if !s.accountLinkLimiter.Allow(limitKey, time.Now().UTC()) {
			respondRateLimited(w)
			return
		}
		link, user, err := s.store.GetAccountLink(token)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.respondAccountLinkError(w, token)
				return
			}
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"purpose":    link.Purpose,
			"expires_at": link.ExpiresAt,
			"user":       store.ToPublicUser(user),
		})
	case http.MethodPost:
		if !s.requireHTTPS(w, r) {
			return
		}
		if !s.accountLinkLimiter.Allow(limitKey, time.Now().UTC()) {
			respondRateLimited(w)
			return
		}
		var input struct {
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		user, err := s.store.ConsumeAccountLink(token, input.Password, store.EventContext{Enabled: true, IP: clientIP(r)})
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.respondAccountLinkError(w, token)
				return
			}
			respondStoreError(w, err)
			return
		}
		csrf, ok := s.createSession(w, user.ID)
		if !ok {
			return
		}
		s.accountLinkLimiter.Reset(limitKey)
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]any{
			"user":       store.ToPublicUser(user),
			"csrf_token": csrf,
		})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) respondAccountLinkError(w http.ResponseWriter, token string) {
	status, err := s.store.AccountLinkStatus(token)
	if err != nil {
		respondJSON(w, http.StatusNotFound, map[string]any{
			"error":  "This account link is invalid. Ask an administrator for a new setup or reset link.",
			"reason": "invalid",
		})
		return
	}
	reason := "invalid"
	message := "This account link is no longer valid. Ask an administrator for a new one."
	code := http.StatusGone
	action := "account"
	if status.Purpose == "setup" || status.Purpose == "reset" {
		action = status.Purpose
	}
	switch {
	case status.UserDisabled:
		code = http.StatusForbidden
		reason = "disabled"
		message = "This account is disabled. Contact an administrator."
	case status.UsedAt != nil:
		reason = "used"
		message = "This " + action + " link has already been used. Sign in or ask an administrator for a new link."
	case !status.ExpiresAt.After(time.Now().UTC()):
		reason = "expired"
		message = "This " + action + " link expired on " + status.ExpiresAt.Format("2006-01-02 15:04 MST") + ". Ask an administrator for a new link."
	}
	respondJSON(w, code, map[string]any{
		"error":      message,
		"reason":     reason,
		"purpose":    status.Purpose,
		"expires_at": status.ExpiresAt,
	})
}

func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{"products": s.store.ListProducts(auth.User)})
	case http.MethodPost:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		var input store.CreateProduct
		if !decodeJSON(w, r, &input) {
			return
		}
		input.Event = s.eventContext(r, auth.User)
		product, err := s.store.CreateProduct(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, product)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProductByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/products/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	productID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || productID < 1 {
		respondError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	if len(parts) == 1 {
		s.handleSingleProduct(w, r, auth, productID)
		return
	}
	switch parts[1] {
	case "members":
		s.handleProductMembers(w, r, auth, productID, parts[2:])
	case "tickets":
		s.handleProductTickets(w, r, auth, productID)
	case "webhooks":
		s.handleProductWebhooks(w, r, auth, productID)
	case "webhook-deliveries":
		s.handleProductDeliveries(w, r, auth, productID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSingleProduct(w http.ResponseWriter, r *http.Request, auth authContext, productID int64) {
	switch r.Method {
	case http.MethodGet:
		if !s.canReadProduct(auth.User, productID) {
			respondError(w, http.StatusNotFound, "not found")
			return
		}
		product, err := s.store.GetProduct(productID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		if role, ok := s.store.ProductRole(auth.User.ID, productID); ok {
			product.Role = role
		}
		if isAdmin(auth.User) {
			product.Role = "owner"
		}
		respondJSON(w, http.StatusOK, product)
	case http.MethodPatch:
		if !s.canManageProduct(auth.User, productID) {
			respondError(w, http.StatusForbidden, "product owner access is required")
			return
		}
		var patch store.UpdateProduct
		if !decodeJSON(w, r, &patch) {
			return
		}
		patch.Event = s.eventContext(r, auth.User)
		product, err := s.store.UpdateProduct(productID, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, product)
	case http.MethodDelete:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		if err := s.store.DeleteProduct(productID, s.eventContext(r, auth.User)); err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleProductMembers(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, rest []string) {
	if !s.canManageProduct(auth.User, productID) {
		respondError(w, http.StatusForbidden, "product owner access is required")
		return
	}
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			respondJSON(w, http.StatusOK, map[string]any{"members": s.store.ListProductMembers(productID), "roles": store.ProductRoles()})
		case http.MethodPost:
			var input store.UpsertProductMember
			if !decodeJSON(w, r, &input) {
				return
			}
			input.Event = s.eventContext(r, auth.User)
			member, err := s.store.UpsertProductMember(productID, input)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			s.dispatchEventsSoon()
			respondJSON(w, http.StatusCreated, member)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(rest) == 1 && r.Method == http.MethodDelete {
		userID, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil || userID < 1 {
			respondError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		if err := s.store.DeleteProductMember(productID, userID, s.eventContext(r, auth.User)); err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleProductTickets(w http.ResponseWriter, r *http.Request, auth authContext, productID int64) {
	if !s.canReadProduct(auth.User, productID) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		tickets := s.listTicketsForQuery(auth.User, query, store.Filter{
			ProductID: productID,
			Query:     query.Get("q"),
			Statuses:  queryStatuses(query),
			Assignee:  query.Get("assignee"),
		})
		respondJSON(w, http.StatusOK, map[string]any{
			"tickets":    tickets,
			"statuses":   store.Statuses(),
			"priorities": store.Priorities(),
		})
	case http.MethodPost:
		if !s.canCreateTicket(auth.User, productID) {
			respondError(w, http.StatusForbidden, "product write access is required")
			return
		}
		var input store.CreateTicket
		var uploads []storedUpload
		if isMultipartRequest(r) {
			if !s.parseMultipartForm(w, r) {
				return
			}
			defer cleanupMultipartForm(r)
			var err error
			input, err = multipartCreateTicketInput(r, productID)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			var ok bool
			uploads, ok = s.saveRequestAttachments(w, r)
			if !ok {
				return
			}
		} else {
			if !decodeJSON(w, r, &input) {
				return
			}
			input.ProductID = productID
		}
		_, ok := s.prepareTicketInput(w, auth.User, productID, &input)
		if !ok {
			cleanupStoredUploads(uploads)
			return
		}
		input.Actor = store.EventActorFromUser(auth.User)
		ticket, err := s.store.CreateTicketWithAttachments(input, attachmentInputs(uploads), auth.User.ID)
		if err != nil {
			cleanupStoredUploads(uploads)
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, s.ticketForUser(auth.User, ticket))
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		productID, _ := strconv.ParseInt(query.Get("product_id"), 10, 64)
		tickets := s.listTicketsForQuery(auth.User, query, store.Filter{
			ProductID: productID,
			Query:     query.Get("q"),
			Statuses:  queryStatuses(query),
			Assignee:  query.Get("assignee"),
		})
		respondJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
	case http.MethodPost:
		var input store.CreateTicket
		var uploads []storedUpload
		if isMultipartRequest(r) {
			if !s.parseMultipartForm(w, r) {
				return
			}
			defer cleanupMultipartForm(r)
			var err error
			input, err = multipartCreateTicketInput(r, 0)
			if err != nil {
				respondStoreError(w, err)
				return
			}
		} else {
			if !decodeJSON(w, r, &input) {
				return
			}
		}
		if !s.canCreateTicket(auth.User, input.ProductID) {
			respondError(w, http.StatusForbidden, "product write access is required")
			return
		}
		_, ok := s.prepareTicketInput(w, auth.User, input.ProductID, &input)
		if !ok {
			return
		}
		if isMultipartRequest(r) {
			var ok bool
			uploads, ok = s.saveRequestAttachments(w, r)
			if !ok {
				return
			}
		}
		input.Actor = store.EventActorFromUser(auth.User)
		ticket, err := s.store.CreateTicketWithAttachments(input, attachmentInputs(uploads), auth.User.ID)
		if err != nil {
			cleanupStoredUploads(uploads)
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, s.ticketForUser(auth.User, ticket))
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleTicketPath(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/tickets/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "key" {
		s.handleTicketByKey(w, r, auth, parts[1])
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	ticket, err := s.store.GetTicket(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !s.canReadTicket(auth.User, ticket) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	if len(parts) == 1 {
		s.handleSingleTicket(w, r, auth, ticket)
		return
	}
	if len(parts) == 2 && parts[1] == "comments" {
		s.handleComments(w, r, auth, ticket)
		return
	}
	if len(parts) == 2 && parts[1] == "read" {
		s.handleTicketRead(w, r, auth, ticket)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleTicketByKey(w http.ResponseWriter, r *http.Request, auth authContext, key string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	ticket, err := s.store.GetTicketByKey(key)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !s.canReadTicket(auth.User, ticket) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, s.ticketForUser(auth.User, ticket))
}

func (s *Server) handleSingleTicket(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket) {
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, s.ticketForUser(auth.User, ticket))
	case http.MethodPatch:
		var input ticketPatchInput
		var uploads []storedUpload
		if isMultipartRequest(r) {
			if !s.parseMultipartForm(w, r) {
				return
			}
			defer cleanupMultipartForm(r)
			input = multipartTicketPatchInput(r)
			var ok bool
			uploads, ok = s.saveRequestAttachments(w, r)
			if !ok {
				return
			}
		} else {
			if !decodeJSON(w, r, &input) {
				return
			}
		}
		updated, ok := s.applyTicketPatch(w, auth, ticket, input, attachmentInputs(uploads), auth.User.ID)
		if !ok {
			cleanupStoredUploads(uploads)
			return
		}
		respondJSON(w, http.StatusOK, s.ticketForUser(auth.User, updated))
	case http.MethodDelete:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		orphanedStorageKeys, err := s.store.DeleteTicket(ticket.ID, s.eventContext(r, auth.User))
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.removeOrphanedAttachmentFiles(orphanedStorageKeys)
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) applyTicketPatch(w http.ResponseWriter, auth authContext, ticket store.Ticket, input ticketPatchInput, attachments []store.CreateAttachment, attachmentUserID int64) (store.Ticket, bool) {
	hasPatch := input.hasTicketPatch()
	hasAttachments := len(attachments) > 0
	if hasAttachments && input.Comment == nil {
		input.Comment = &store.AddComment{Visibility: "public"}
	}
	hasComment := input.Comment != nil && (strings.TrimSpace(input.Comment.Body) != "" || hasAttachments)
	if !hasPatch && !hasComment {
		respondError(w, http.StatusBadRequest, "ticket changes or comment are required")
		return store.Ticket{}, false
	}
	if hasPatch && !s.canEditTicket(auth.User, ticket.ProductID) {
		respondError(w, http.StatusForbidden, "agent access is required")
		return store.Ticket{}, false
	}
	if hasComment && !s.canCommentTicket(auth.User, ticket.ProductID) {
		respondError(w, http.StatusForbidden, "product comment access is required")
		return store.Ticket{}, false
	}

	var comment *store.AddComment
	if hasComment {
		next := *input.Comment
		next.Visibility = defaultString(next.Visibility, "public")
		if next.Visibility == "internal" && !s.canEditTicket(auth.User, ticket.ProductID) {
			respondError(w, http.StatusForbidden, "agent access is required for internal notes")
			return store.Ticket{}, false
		}
		next.Author = defaultString(auth.User.DisplayName, auth.User.Username)
		next.AuthorUserID = auth.User.ID
		comment = &next
	}

	result, err := s.store.SaveTicket(store.SaveTicketInput{
		TicketID:         ticket.ID,
		Patch:            input.updateTicket(),
		Comment:          comment,
		Attachments:      attachments,
		AttachmentUserID: attachmentUserID,
		Actor:            store.EventActorFromUser(auth.User),
	})
	if err != nil {
		respondStoreError(w, err)
		return store.Ticket{}, false
	}
	updated := result.Ticket
	s.dispatchEventsSoon()
	if result.HasPatch || result.HasComment {
		if err := s.store.MarkTicketRead(updated.ID, auth.User.ID, time.Now().UTC()); err == nil {
			if refreshed, err := s.store.GetTicket(updated.ID); err == nil {
				updated = refreshed
			}
		}
	}
	return updated, true
}

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.canCommentTicket(auth.User, ticket.ProductID) {
		respondError(w, http.StatusForbidden, "product comment access is required")
		return
	}
	var input store.AddComment
	var uploads []storedUpload
	if isMultipartRequest(r) {
		if !s.parseMultipartForm(w, r) {
			return
		}
		defer cleanupMultipartForm(r)
		input = multipartCommentInput(r)
		var ok bool
		uploads, ok = s.saveRequestAttachments(w, r)
		if !ok {
			return
		}
	} else {
		if !decodeJSON(w, r, &input) {
			return
		}
	}
	input.Visibility = defaultString(input.Visibility, "public")
	if input.Visibility == "internal" && !s.canEditTicket(auth.User, ticket.ProductID) {
		respondError(w, http.StatusForbidden, "agent access is required for internal notes")
		cleanupStoredUploads(uploads)
		return
	}
	updated, ok := s.applyTicketPatch(w, auth, ticket, ticketPatchInput{Comment: &input}, attachmentInputs(uploads), auth.User.ID)
	if !ok {
		cleanupStoredUploads(uploads)
		return
	}
	respondJSON(w, http.StatusCreated, s.ticketForUser(auth.User, updated))
}

func (s *Server) handleTicketRead(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := s.store.MarkTicketRead(ticket.ID, auth.User.ID, time.Now().UTC()); err != nil {
		respondStoreError(w, err)
		return
	}
	updated, err := s.store.GetTicket(ticket.ID)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, s.ticketForUser(auth.User, updated))
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		users := s.store.ListUsers()
		public := make([]store.PublicUser, 0, len(users))
		for _, user := range users {
			public = append(public, store.ToPublicUser(user))
		}
		respondJSON(w, http.StatusOK, map[string]any{"users": public, "roles": store.Roles()})
	case http.MethodPost:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		var input store.CreateUser
		if !decodeJSON(w, r, &input) {
			return
		}
		input.Event = s.eventContext(r, auth.User)
		created, link, token, err := s.store.CreateUserWithSetupLink(input, accountLinkExpiry)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		queued := s.accountLinkEmailRequested(created, token)
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, userAccountLinkResponse(created, s.accountLinkURL(link.Purpose, token), link.ExpiresAt, queued, s.options.EmailNotifications))
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/users/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if len(parts) == 2 && parts[1] == "password-reset" {
		s.handleUserPasswordReset(w, r, auth, id)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var patch store.UpdateUser
		if !decodeJSON(w, r, &patch) {
			return
		}
		if patch.Password != nil {
			respondError(w, http.StatusBadRequest, "use password reset")
			return
		}
		patch.Event = s.eventContext(r, auth.User)
		user, err := s.store.UpdateUser(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, store.ToPublicUser(user))
	case http.MethodDelete:
		if err := s.store.DeleteUser(id, s.eventContext(r, auth.User)); err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleUserPasswordReset(w http.ResponseWriter, r *http.Request, auth authContext, userID int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	user, link, token, err := s.store.CreatePasswordResetLink(userID, accountLinkExpiry, s.eventContext(r, auth.User))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	queued := s.accountLinkEmailRequested(user, token)
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusCreated, userAccountLinkResponse(user, s.accountLinkURL(link.Purpose, token), link.ExpiresAt, queued, s.options.EmailNotifications))
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{"tokens": s.store.ListAPITokens(auth.User.ID)})
	case http.MethodPost:
		var input store.CreateAPIToken
		if !decodeJSON(w, r, &input) {
			return
		}
		input.Event = s.eventContext(r, auth.User)
		token, raw, err := s.store.CreateAPIToken(auth.User.ID, input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, map[string]any{"token": token, "value": raw})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleTokenByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	id, ok := parseTrailingID(w, r.URL.Path, "/api/tokens/")
	if !ok {
		return
	}
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, http.MethodDelete)
		return
	}
	if err := s.store.DeleteAPIToken(auth.User.ID, id, s.eventContext(r, auth.User)); err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{
			"webhooks": publicWebhooks(s.store.ListWebhooks(nil)),
			"events":   store.Events(),
		})
	case http.MethodPost:
		var input store.CreateWebhook
		if !decodeJSON(w, r, &input) {
			return
		}
		input.ProductID = nil
		input.Event = s.eventContext(r, auth.User)
		hook, err := s.store.CreateWebhook(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, map[string]any{"webhook": store.ToPublicWebhook(hook), "secret": hook.Secret})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProductWebhooks(w http.ResponseWriter, r *http.Request, auth authContext, productID int64) {
	if !s.canManageProduct(auth.User, productID) {
		respondError(w, http.StatusForbidden, "product owner access is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{
			"webhooks": publicWebhooks(s.store.ListWebhooks(&productID)),
			"events":   store.Events(),
		})
	case http.MethodPost:
		var input store.CreateWebhook
		if !decodeJSON(w, r, &input) {
			return
		}
		input.ProductID = &productID
		input.Event = s.eventContext(r, auth.User)
		hook, err := s.store.CreateWebhook(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, map[string]any{"webhook": store.ToPublicWebhook(hook), "secret": hook.Secret})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleWebhookByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/webhooks/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, "invalid webhook id")
		return
	}
	hook, err := s.store.GetWebhook(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if hook.ProductID == nil {
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
	} else if !s.canManageProduct(auth.User, *hook.ProductID) {
		respondError(w, http.StatusForbidden, "product owner access is required")
		return
	}
	if len(parts) == 2 && parts[1] == "secret" {
		s.handleWebhookSecret(w, r, auth, hook)
		return
	}
	if len(parts) == 2 && parts[1] == "test" {
		s.handleWebhookTest(w, r, auth, hook)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var patch store.UpdateWebhook
		if !decodeJSON(w, r, &patch) {
			return
		}
		patch.Event = s.eventContext(r, auth.User)
		updated, err := s.store.UpdateWebhook(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, store.ToPublicWebhook(updated))
	case http.MethodDelete:
		if err := s.store.DeleteWebhook(id, s.eventContext(r, auth.User)); err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleWebhookSecret(w http.ResponseWriter, r *http.Request, auth authContext, hook store.Webhook) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	updated, secret, err := s.store.RotateWebhookSecret(hook.ID, s.eventContext(r, auth.User))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, map[string]any{"webhook": store.ToPublicWebhook(updated), "secret": secret})
}

func (s *Server) handleWebhookTest(w http.ResponseWriter, r *http.Request, auth authContext, hook store.Webhook) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	payload := map[string]any{
		"event":      "webhook.test",
		"created_at": time.Now().UTC(),
		"actor":      store.ToPublicUser(auth.User),
		"message":    "Pappice test delivery",
	}
	body, _ := json.Marshal(payload)
	delivery := s.deliverWebhook(hook, "webhook.test", 0, body)
	respondJSON(w, http.StatusOK, delivery)
}

func (s *Server) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deliveries": s.store.ListDeliveries(50)})
}

func (s *Server) handleEmailNotifications(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	limit, offset := paginationParams(r, 25, 100)
	page := s.store.ListEmailNotificationsPage(store.EmailNotificationFilter{
		Status: r.URL.Query().Get("status"),
		Query:  r.URL.Query().Get("q"),
		Limit:  limit,
		Offset: offset,
	})
	respondJSON(w, http.StatusOK, map[string]any{
		"notifications":       page.Notifications,
		"total":               page.Total,
		"limit":               page.Limit,
		"offset":              page.Offset,
		"enabled":             s.options.EmailNotifications,
		"batch_delay_seconds": int(s.options.EmailBatchDelay.Seconds()),
		"stats":               s.store.EmailNotificationStats(),
	})
}

func (s *Server) handleEmailNotificationByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/email-notifications/"), "/")
	if rest == "test" {
		s.handleEmailNotificationTest(w, r, auth)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "retry" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, "invalid email notification id")
		return
	}
	notification, err := s.store.RetryEmailNotification(id, s.eventContext(r, auth.User))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, map[string]any{"notification": notification})
}

func (s *Server) handleEmailNotificationTest(w http.ResponseWriter, r *http.Request, auth authContext) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.options.EmailNotifications {
		respondError(w, http.StatusConflict, "email notifications are not configured")
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	recipientEmail := strings.TrimSpace(input.Email)
	if recipientEmail == "" {
		recipientEmail = strings.TrimSpace(auth.User.Email)
	}
	if recipientEmail == "" {
		respondError(w, http.StatusBadRequest, "test recipient email is required")
		return
	}
	recipientName := defaultString(auth.User.DisplayName, auth.User.Username)
	subject := "Pappice test email"
	bodyText := "This is a no-reply test email from Pappice.\n\nIf you received this message, SMTP delivery is working."
	bodyHTML := "<!doctype html><meta charset=\"utf-8\"><p>This is a no-reply test email from Pappice.</p><p>If you received this message, SMTP delivery is working.</p>"
	created, err := s.store.EnqueueEmailNotificationsWithEvent([]store.CreateEmailNotification{{
		UserID:         auth.User.ID,
		RecipientEmail: recipientEmail,
		RecipientName:  recipientName,
		Event:          "email.test",
		Subject:        subject,
		BodyText:       bodyText,
		BodyHTML:       bodyHTML,
	}}, s.eventContext(r, auth.User), "email_notification.test_queued", "email_notification", map[string]any{"recipient": recipientEmail})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusCreated, map[string]any{"notification": created[0]})
}

func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	limit, offset := paginationParams(r, 25, 100)
	page := s.store.ListAuditEventsPage(store.AuditEventFilter{
		Query:  r.URL.Query().Get("q"),
		Limit:  limit,
		Offset: offset,
	})
	respondJSON(w, http.StatusOK, map[string]any{
		"events": page.Events,
		"total":  page.Total,
		"limit":  page.Limit,
		"offset": page.Offset,
	})
}

func (s *Server) handleProductDeliveries(w http.ResponseWriter, r *http.Request, auth authContext, productID int64) {
	if !s.canManageProduct(auth.User, productID) {
		respondError(w, http.StatusForbidden, "product owner access is required")
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	deliveries := s.store.ListDeliveries(200)
	filtered := make([]store.WebhookDelivery, 0)
	for _, delivery := range deliveries {
		if delivery.ProductID != nil && *delivery.ProductID == productID {
			filtered = append(filtered, delivery)
		}
	}
	if len(filtered) > 50 {
		filtered = filtered[:50]
	}
	respondJSON(w, http.StatusOK, map[string]any{"deliveries": filtered})
}

func (s *Server) createSession(w http.ResponseWriter, userID int64) (string, bool) {
	token, csrf, expires, err := s.store.CreateSessionFor(userID, s.options.SessionTTL)
	if err != nil {
		respondStoreError(w, err)
		return "", false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	return csrf, true
}

func (s *Server) currentAuth(r *http.Request) (authContext, bool) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if user, csrf, ok := s.store.UserBySession(cookie.Value); ok {
			return authContext{User: user, CSRF: csrf, SessionToken: cookie.Value}, true
		}
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		token := strings.TrimSpace(header[7:])
		if user, ok := s.store.UserByAPIToken(token); ok {
			return authContext{User: user, ViaToken: true}, true
		}
	}
	return authContext{}, false
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	if s.store.SetupRequired() {
		respondError(w, http.StatusConflict, "setup is required")
		return authContext{}, false
	}
	auth, ok := s.currentAuth(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication is required")
		return authContext{}, false
	}
	if isUnsafeMethod(r.Method) && !auth.ViaToken {
		if !s.verifyCSRF(w, r, auth.CSRF) {
			return authContext{}, false
		}
	}
	return auth, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return authContext{}, false
	}
	if !isAdmin(auth.User) {
		respondError(w, http.StatusForbidden, "admin role is required")
		return authContext{}, false
	}
	return auth, true
}

func (s *Server) requireStaff(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return authContext{}, false
	}
	if !isStaff(auth.User) {
		respondError(w, http.StatusForbidden, "staff access is required")
		return authContext{}, false
	}
	return auth, true
}

func (s *Server) requireHTTPS(w http.ResponseWriter, r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	respondError(w, http.StatusBadRequest, "HTTPS is required for browser sessions")
	return false
}

func (s *Server) verifyCSRF(w http.ResponseWriter, r *http.Request, expected string) bool {
	if !sameOrigin(r) {
		respondError(w, http.StatusForbidden, "same-origin request is required")
		return false
	}
	token := strings.TrimSpace(r.Header.Get("X-Pappice-CSRF"))
	if token == "" || !security.ConstantTimeEqual(token, expected) {
		respondError(w, http.StatusForbidden, "valid CSRF token is required")
		return false
	}
	return true
}

func (s *Server) canReadProduct(user store.User, productID int64) bool {
	if isAdmin(user) {
		return true
	}
	_, ok := s.store.ProductRole(user.ID, productID)
	return ok
}

func (s *Server) canReadTicket(user store.User, ticket store.Ticket) bool {
	if isAdmin(user) {
		return true
	}
	role, ok := s.store.ProductRole(user.ID, ticket.ProductID)
	if !ok {
		return false
	}
	if isCustomer(user) || role == "customer" {
		return s.isSupportTicketRequester(user, ticket)
	}
	return true
}

func (s *Server) canManageProduct(user store.User, productID int64) bool {
	if isCustomer(user) {
		return false
	}
	if isAdmin(user) {
		return true
	}
	role, ok := s.store.ProductRole(user.ID, productID)
	return ok && role == "owner"
}

func (s *Server) canCreateTicket(user store.User, productID int64) bool {
	return s.hasProductRole(user, productID, "owner", "agent", "customer")
}

func (s *Server) canCommentTicket(user store.User, productID int64) bool {
	return s.hasProductRole(user, productID, "owner", "agent", "customer")
}

func (s *Server) canEditTicket(user store.User, productID int64) bool {
	if isCustomer(user) {
		return false
	}
	return s.hasProductRole(user, productID, "owner", "agent")
}

func (s *Server) isSupportTicketRequester(user store.User, ticket store.Ticket) bool {
	email := strings.TrimSpace(user.Email)
	if email != "" && strings.EqualFold(email, strings.TrimSpace(ticket.RequesterEmail)) {
		return true
	}
	return ticket.Source == "portal" && strings.EqualFold(strings.TrimSpace(ticket.Reporter), strings.TrimSpace(user.Username))
}

func (s *Server) prepareTicketInput(w http.ResponseWriter, user store.User, productID int64, input *store.CreateTicket) (bool, bool) {
	input.ProductID = productID
	input.Reporter = user.Username
	if !s.isCustomerTicketCreator(user, productID) {
		return false, true
	}
	requesterEmail := strings.TrimSpace(user.Email)
	if requesterEmail == "" {
		respondError(w, http.StatusBadRequest, "your account needs an email address before you can open support tickets")
		return true, false
	}
	input.Assignee = ""
	input.Source = "portal"
	input.RequesterName = defaultString(user.DisplayName, user.Username)
	input.RequesterEmail = requesterEmail
	return true, true
}

func (s *Server) isCustomerTicketCreator(user store.User, productID int64) bool {
	if isCustomer(user) {
		return true
	}
	role, ok := s.store.ProductRole(user.ID, productID)
	return ok && role == "customer"
}

func (s *Server) ticketForUser(user store.User, ticket store.Ticket) store.Ticket {
	tickets := s.ticketsForUser(user, []store.Ticket{ticket})
	if len(tickets) == 0 {
		return ticket
	}
	return tickets[0]
}

func (s *Server) listTicketsForQuery(user store.User, query url.Values, filter store.Filter) []store.Ticket {
	statuses := append([]string(nil), filter.Statuses...)
	if queryIncludeUnreadOutsideStatus(query) && len(statuses) > 0 && !queryUnread(query) {
		filter.Statuses = nil
	}
	tickets := s.store.ListTicketsForUser(filter, user)
	tickets = s.ticketsForUser(user, tickets)
	if queryUnread(query) {
		return unreadTickets(tickets)
	}
	if queryIncludeUnreadOutsideStatus(query) && len(statuses) > 0 {
		return ticketsMatchingStatusOrUnread(tickets, statuses)
	}
	return tickets
}

func (s *Server) ticketsForUser(user store.User, tickets []store.Ticket) []store.Ticket {
	result := make([]store.Ticket, 0, len(tickets))
	for _, ticket := range tickets {
		if !s.canEditTicket(user, ticket.ProductID) {
			ticket.Comments = publicComments(ticket.Comments)
		}
		result = append(result, ticket)
	}
	s.annotateUnread(user, result)
	return result
}

func (s *Server) annotateUnread(user store.User, tickets []store.Ticket) {
	ids := make([]int64, 0, len(tickets))
	for _, ticket := range tickets {
		ids = append(ids, ticket.ID)
	}
	readTimes, err := s.store.TicketReadTimes(user.ID, ids)
	if err != nil {
		return
	}
	for index := range tickets {
		lastRead := readTimes[tickets[index].ID]
		if !lastRead.IsZero() {
			readAt := lastRead
			tickets[index].LastReadAt = &readAt
		}
		tickets[index].UnreadCount = unreadActivityCount(user, tickets[index], lastRead)
		tickets[index].HasUnread = tickets[index].UnreadCount > 0
	}
}

func ticketsMatchingStatusOrUnread(tickets []store.Ticket, statuses []string) []store.Ticket {
	active := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		active[strings.TrimSpace(status)] = struct{}{}
	}
	result := make([]store.Ticket, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket.HasUnread {
			result = append(result, ticket)
			continue
		}
		if _, ok := active[ticket.Status]; ok {
			result = append(result, ticket)
		}
	}
	return result
}

func unreadTickets(tickets []store.Ticket) []store.Ticket {
	result := make([]store.Ticket, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket.HasUnread {
			result = append(result, ticket)
		}
	}
	return result
}

func unreadActivityCount(user store.User, ticket store.Ticket, lastRead time.Time) int {
	count := 0
	if ticket.CreatedAt.After(lastRead) && !ticketOpenedByUser(user, ticket) {
		count++
	}
	if ticket.UpdatedAt.After(lastRead) && requesterTerminalStatus(ticket.Status) {
		count++
	}
	for _, comment := range ticket.Comments {
		if comment.CreatedAt.After(lastRead) && !commentByUser(user, comment) {
			count++
		}
	}
	return count
}

func ticketOpenedByUser(user store.User, ticket store.Ticket) bool {
	values := userAuthorValues(user)
	for _, value := range []string{ticket.Reporter, ticket.RequesterName, ticket.RequesterEmail, emailLocalPart(ticket.RequesterEmail)} {
		if values[normalizeAuthor(value)] {
			return true
		}
	}
	return false
}

func commentByUser(user store.User, comment store.Comment) bool {
	if comment.AuthorUserID > 0 {
		return comment.AuthorUserID == user.ID
	}
	return userAuthorValues(user)[normalizeAuthor(comment.Author)]
}

func userAuthorValues(user store.User) map[string]bool {
	values := map[string]bool{}
	for _, value := range []string{user.Username, user.DisplayName, user.Email, emailLocalPart(user.Email)} {
		if normalized := normalizeAuthor(value); normalized != "" {
			values[normalized] = true
		}
	}
	return values
}

func normalizeAuthor(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func emailLocalPart(email string) string {
	local, _, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok {
		return ""
	}
	return local
}

func publicComments(comments []store.Comment) []store.Comment {
	result := make([]store.Comment, 0, len(comments))
	for _, comment := range comments {
		if comment.Visibility == "" || comment.Visibility == "public" {
			comment.Visibility = "public"
			result = append(result, comment)
		}
	}
	return result
}

func (s *Server) hasProductRole(user store.User, productID int64, allowed ...string) bool {
	if isAdmin(user) {
		return true
	}
	role, ok := s.store.ProductRole(user.ID, productID)
	if !ok {
		return false
	}
	for _, item := range allowed {
		if role == item {
			return true
		}
	}
	return false
}

func (s *Server) eventContext(r *http.Request, actor store.User) store.EventContext {
	ctx := store.EventContext{
		Enabled: true,
		Actor:   store.EventActorFromUser(actor),
	}
	if r != nil {
		ctx.IP = clientIP(r)
	}
	return ctx
}

func (s *Server) dispatchEventsSoon() {
	_ = s.dispatchPendingEvents(nil, 10)
}

func (s *Server) accountLinkEmailRequested(user store.User, token string) bool {
	return s.options.EmailNotifications && store.AccountLinkEmailRequested(user, token)
}

func queryStatuses(query url.Values) []string {
	statuses := make([]string, 0, len(query["status"]))
	for _, value := range query["status"] {
		for _, status := range strings.Split(value, ",") {
			status = strings.TrimSpace(status)
			if status != "" {
				statuses = append(statuses, status)
			}
		}
	}
	return statuses
}

func queryUnread(query url.Values) bool {
	value := strings.ToLower(strings.TrimSpace(query.Get("unread")))
	return value == "1" || value == "true" || value == "yes"
}

func queryIncludeUnreadOutsideStatus(query url.Values) bool {
	value := strings.ToLower(strings.TrimSpace(query.Get("include_unread_outside_status")))
	return value == "1" || value == "true" || value == "yes"
}

func paginationParams(r *http.Request, defaultLimit, maxLimit int) (int, int) {
	query := r.URL.Query()
	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil || limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset, err := strconv.Atoi(query.Get("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}
	return limit, offset
}

func publicWebhooks(hooks []store.Webhook) []store.PublicWebhook {
	public := make([]store.PublicWebhook, 0, len(hooks))
	for _, hook := range hooks {
		public = append(public, store.ToPublicWebhook(hook))
	}
	return public
}

type accountLinkResponse struct {
	URL          string    `json:"url"`
	ExpiresAt    time.Time `json:"expires_at"`
	EmailQueued  bool      `json:"email_queued"`
	EmailEnabled bool      `json:"email_enabled"`
}

type userAccountLinkPayload struct {
	store.PublicUser
	AccountLink accountLinkResponse `json:"account_link"`
}

func userAccountLinkResponse(user store.User, url string, expiresAt time.Time, emailQueued, emailEnabled bool) userAccountLinkPayload {
	return userAccountLinkPayload{
		PublicUser: store.ToPublicUser(user),
		AccountLink: accountLinkResponse{
			URL:          url,
			ExpiresAt:    expiresAt,
			EmailQueued:  emailQueued,
			EmailEnabled: emailEnabled,
		},
	}
}

func (s *Server) ticketURL(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	base := strings.TrimRight(s.options.PublicURL, "/")
	if base == "" {
		return "/"
	}
	return base + "/"
}

func (s *Server) accountLinkURL(purpose, token string) string {
	purpose = strings.TrimSpace(purpose)
	token = strings.TrimSpace(token)
	if purpose == "" || token == "" {
		return ""
	}
	base := strings.TrimRight(s.options.PublicURL, "/")
	path := "/account/" + purpose + "/" + token
	if base == "" {
		return path
	}
	return base + path
}

func nullableUser(user store.User, ok bool) any {
	if !ok {
		return nil
	}
	return store.ToPublicUser(user)
}

func nullableString(value string, ok bool) any {
	if !ok {
		return nil
	}
	return value
}

func isAdmin(user store.User) bool {
	return user.Role == "admin"
}

func isStaff(user store.User) bool {
	return user.Role == "admin" || user.Role == "staff" || user.Role == "user"
}

func isCustomer(user store.User) bool {
	return user.Role == "customer" || user.Role == "client"
}

func isUnsafeMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut || method == http.MethodDelete
}

func sameOrigin(r *http.Request) bool {
	if r.TLS == nil {
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return originMatches(origin, r.Host)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return originMatches(referer, r.Host)
	}
	return false
}

func originMatches(raw, host string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && strings.EqualFold(parsed.Host, host)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func parseTrailingID(w http.ResponseWriter, path, prefix string) (int64, bool) {
	raw := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if raw == "" || strings.Contains(raw, "/") {
		respondError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		respondError(w, http.StatusBadRequest, "request body is required")
		return false
	}
	defer r.Body.Close()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func respondStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		respondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrPasswordResetRequired):
		respondError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, store.ErrValidation):
		respondError(w, http.StatusBadRequest, err.Error())
	default:
		respondError(w, http.StatusInternalServerError, "internal server error")
	}
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func respondRateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	respondError(w, http.StatusTooManyRequests, "too many attempts; try again later")
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	respondError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
