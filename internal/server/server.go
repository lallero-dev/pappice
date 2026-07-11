package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pappice/internal/security"
	"pappice/internal/store"
)

//go:embed web/index.html web/static/*
var assets embed.FS

const (
	sessionCookieName     = "pappice_session"
	accountLinkExpiry     = 24 * time.Hour
	defaultSessionTTL     = 14 * 24 * time.Hour
	defaultBrandName      = "Pappice"
	defaultBrandSubtitle  = "customer support"
	defaultBrandColor     = "#5bb974"
	defaultUploadDir      = "pappice-uploads"
	defaultBackupDir      = "pappice-backups"
	defaultMaxUploadSize  = 10 << 20
	defaultMaxUploadFiles = 5
	defaultVersion        = "dev"
)

var (
	appAdminSections   = []string{"accounts", "tokens", "webhooks", "email", "maintenance", "audit"}
	appProductSections = []string{"general", "members", "webhooks", "deliveries"}
)

type RateLimit struct {
	Limit  int
	Window time.Duration
}

type Logger interface {
	Printf(format string, args ...any)
}

type Options struct {
	AllowInsecureWebhooks bool
	AllowPrivateWebhooks  bool
	TrustProxyHeaders     bool
	Branding              Branding
	DomainEventRetention  time.Duration
	EmailNotifications    bool
	NotificationDelay     time.Duration
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
	Logger                Logger
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
	Title          *string           `json:"title"`
	Description    *string           `json:"description"`
	Status         *string           `json:"status"`
	Priority       *string           `json:"priority"`
	AssigneeUserID *int64            `json:"assignee_user_id"`
	Comment        *store.AddComment `json:"comment"`
}

func (input ticketPatchInput) updateTicket() store.UpdateTicket {
	return store.UpdateTicket{
		Title:          input.Title,
		Description:    input.Description,
		Status:         input.Status,
		Priority:       input.Priority,
		AssigneeUserID: input.AssigneeUserID,
	}
}

func (input ticketPatchInput) hasTicketPatch() bool {
	return input.Title != nil || input.Description != nil || input.Status != nil || input.Priority != nil || input.AssigneeUserID != nil
}

func New(tracker *store.Store, opts ...Options) http.Handler {
	return NewServer(tracker, opts...)
}

func NewServer(tracker *store.Store, opts ...Options) *Server {
	options := Options{}
	if len(opts) > 0 {
		options = opts[0]
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
	s.handler = s.securityHeaders(s.mux)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	setupRequired, err := s.store.SetupRequired()
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"name":           "pappice",
		"branding":       s.options.Branding,
		"started_at":     s.started,
		"needs_setup":    setupRequired,
		"statuses":       store.Statuses(),
		"priorities":     store.Priorities(),
		"roles":          store.Roles(),
		"product_roles":  store.ProductRoles(),
		"webhook_events": store.Events(),
		"email_enabled":  s.options.EmailNotifications,
		"uploads":        s.publicUploadConfig(),
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	setupRequired, err := s.store.SetupRequired()
	if err != nil {
		respondStoreError(w, err)
		return
	}
	auth, err := s.currentAuth(r)
	authenticated := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"authenticated": authenticated,
		"needs_setup":   setupRequired,
		"user":          nullableUser(auth.User, authenticated),
		"csrf_token":    nullableString(auth.CSRF, authenticated && !auth.ViaToken),
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
	if !s.requireBrowserSession(w, auth) {
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
	if !s.requireBrowserSession(w, auth) {
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
	input.Event = store.EventContext{Enabled: true, IP: s.clientIP(r)}
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
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	limitKey := "login|" + s.clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(input.Email))
	if !s.loginLimiter.Allow(limitKey, time.Now().UTC()) {
		respondRateLimited(w)
		return
	}
	user, err := s.store.Authenticate(input.Email, input.Password)
	if err != nil {
		if errors.Is(err, store.ErrPasswordResetRequired) {
			respondError(w, http.StatusUnauthorized, "password setup or reset is required; use the emailed link or contact an admin")
			return
		}
		respondError(w, http.StatusUnauthorized, "invalid email or password")
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
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.requireBrowserSession(w, auth) {
		return
	}
	if err := s.store.DeleteSession(auth.SessionToken); err != nil {
		respondStoreError(w, err)
		return
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
	token := trimRoutePrefix(r.URL.Path, "/api/account-links/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	limitKey := "account-link|" + s.clientIP(r) + "|" + security.HashToken(token)
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
		user, err := s.store.ConsumeAccountLink(token, input.Password, store.EventContext{Enabled: true, IP: s.clientIP(r)})
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
		products, err := s.store.ListProducts(auth.User)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		assignees, err := s.store.ListProductAssignees(auth.User)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"products":  products,
			"assignees": assignees,
		})
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
	parts := routeParts(r.URL.Path, "/api/products/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	productID, ok := parsePositiveID(w, parts[0], "invalid product id")
	if !ok {
		return
	}
	access, err := s.productAccess(auth.User, productID)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if len(parts) == 1 {
		s.handleSingleProduct(w, r, auth, productID, access)
		return
	}
	switch parts[1] {
	case "members":
		s.handleProductMembers(w, r, auth, productID, access, parts[2:])
	case "tickets":
		s.handleProductTickets(w, r, auth, productID, access)
	case "webhooks":
		s.handleProductWebhooks(w, r, auth, productID, access)
	case "webhook-deliveries":
		s.handleProductDeliveries(w, r, auth, productID, access)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSingleProduct(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess) {
	switch r.Method {
	case http.MethodGet:
		if !access.read {
			respondError(w, http.StatusNotFound, "not found")
			return
		}
		product, err := s.store.GetProduct(productID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		product.Role = access.role
		respondJSON(w, http.StatusOK, product)
	case http.MethodPatch:
		if !access.manage {
			respondError(w, http.StatusForbidden, "product manager access is required")
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
		orphanedStorageKeys, err := s.store.DeleteProduct(productID, s.eventContext(r, auth.User))
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

func (s *Server) handleProductMembers(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess, rest []string) {
	if !access.manage {
		respondError(w, http.StatusForbidden, "product manager access is required")
		return
	}
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			members, err := s.store.ListProductMembers(productID)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			respondJSON(w, http.StatusOK, map[string]any{"members": members, "roles": store.ProductRoles()})
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
		userID, ok := parsePositiveID(w, rest[0], "invalid user id")
		if !ok {
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

func (s *Server) handleProductTickets(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess) {
	switch r.Method {
	case http.MethodGet:
		if !access.read {
			respondError(w, http.StatusNotFound, "not found")
			return
		}
		query := r.URL.Query()
		limit, offset := paginationParams(r, 50, 500)
		result, err := s.listTicketsForQuery(auth.User, ticketSummaryFilter(query, productID, limit, offset))
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"tickets":      result.Tickets,
			"counts":       result.Counts,
			"unread_total": result.UnreadTotal,
			"limit":        result.Limit,
			"offset":       result.Offset,
			"has_more":     result.HasMore,
			"statuses":     store.Statuses(),
			"priorities":   store.Priorities(),
		})
	case http.MethodPost:
		if !access.createTicket {
			respondError(w, http.StatusForbidden, "product write access is required")
			return
		}
		ticket, ok := s.createTicketFromRequest(w, r, auth, productID)
		if !ok {
			return
		}
		s.dispatchEventsSoon()
		s.respondTicketForUser(w, http.StatusCreated, auth.User, ticket)
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
		limit, offset := paginationParams(r, 50, 500)
		result, err := s.listTicketsForQuery(auth.User, ticketSummaryFilter(query, productID, limit, offset))
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, result)
	case http.MethodPost:
		ticket, ok := s.createTicketFromRequest(w, r, auth, 0)
		if !ok {
			return
		}
		s.dispatchEventsSoon()
		s.respondTicketForUser(w, http.StatusCreated, auth.User, ticket)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) createTicketFromRequest(w http.ResponseWriter, r *http.Request, auth authContext, fallbackProductID int64) (store.Ticket, bool) {
	var input store.CreateTicket
	multipart := isMultipartRequest(r)
	if multipart {
		if !s.parseMultipartForm(w, r) {
			return store.Ticket{}, false
		}
		defer cleanupMultipartForm(r)
		var err error
		input, err = multipartCreateTicketInput(r, fallbackProductID)
		if err != nil {
			respondStoreError(w, err)
			return store.Ticket{}, false
		}
	} else {
		if !decodeJSON(w, r, &input) {
			return store.Ticket{}, false
		}
		if fallbackProductID > 0 {
			input.ProductID = fallbackProductID
		}
	}

	if fallbackProductID == 0 {
		access, err := s.productAccess(auth.User, input.ProductID)
		if err != nil {
			respondStoreError(w, err)
			return store.Ticket{}, false
		}
		if !access.createTicket {
			respondError(w, http.StatusForbidden, "product write access is required")
			return store.Ticket{}, false
		}
	}
	input.ActorUserID = auth.User.ID

	var uploads []storedUpload
	if multipart {
		var ok bool
		uploads, ok = s.saveRequestAttachments(w, r)
		if !ok {
			return store.Ticket{}, false
		}
	}
	ticket, err := s.store.CreateTicketWithAttachments(input, attachmentInputs(uploads))
	if err != nil {
		cleanupStoredUploads(uploads)
		respondStoreError(w, err)
		return store.Ticket{}, false
	}
	return ticket, true
}

func (s *Server) handleTicketPath(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := routeParts(r.URL.Path, "/api/tickets/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "key" {
		s.handleTicketByKey(w, r, auth, parts[1])
		return
	}
	id, ok := parsePositiveID(w, parts[0], "invalid ticket id")
	if !ok {
		return
	}
	ticket, err := s.store.GetTicket(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	access, err := s.ticketAccess(auth.User, ticket)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !access.read {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	if len(parts) == 1 {
		s.handleSingleTicket(w, r, auth, ticket, access)
		return
	}
	if len(parts) == 2 && parts[1] == "comments" {
		s.handleComments(w, r, auth, ticket, access)
		return
	}
	if len(parts) == 2 && parts[1] == "read" {
		s.handleTicketRead(w, r, auth, ticket, access)
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
	access, err := s.ticketAccess(auth.User, ticket)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !access.read {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	s.respondTicket(w, http.StatusOK, auth.User, ticket, access)
}

func (s *Server) handleSingleTicket(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket, access ticketAccess) {
	switch r.Method {
	case http.MethodGet:
		s.respondTicket(w, http.StatusOK, auth.User, ticket, access)
	case http.MethodPatch:
		var input ticketPatchInput
		var uploads []storedUpload
		if isMultipartRequest(r) {
			if !s.parseMultipartForm(w, r) {
				return
			}
			defer cleanupMultipartForm(r)
			var err error
			input, err = multipartTicketPatchInput(r)
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
		}
		updated, ok := s.applyTicketPatch(w, auth, ticket, access, input, attachmentInputs(uploads))
		if !ok {
			cleanupStoredUploads(uploads)
			return
		}
		s.respondTicket(w, http.StatusOK, auth.User, updated, access)
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

func (s *Server) applyTicketPatch(w http.ResponseWriter, auth authContext, ticket store.Ticket, access ticketAccess, input ticketPatchInput, attachments []store.CreateAttachment) (store.Ticket, bool) {
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
	if hasPatch && !access.edit {
		respondError(w, http.StatusForbidden, "staff access is required")
		return store.Ticket{}, false
	}
	if hasComment && !access.comment {
		respondError(w, http.StatusForbidden, "product comment access is required")
		return store.Ticket{}, false
	}

	var comment *store.AddComment
	if hasComment {
		next := *input.Comment
		next.Visibility = defaultString(next.Visibility, "public")
		if next.Visibility == "internal" && !access.edit {
			respondError(w, http.StatusForbidden, "staff access is required for internal notes")
			return store.Ticket{}, false
		}
		comment = &next
	}

	result, err := s.store.SaveTicket(store.SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       input.updateTicket(),
		Comment:     comment,
		Attachments: attachments,
		ActorUserID: auth.User.ID,
	})
	if err != nil {
		respondStoreError(w, err)
		return store.Ticket{}, false
	}
	updated := result.Ticket
	s.dispatchEventsSoon()
	return updated, true
}

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket, access ticketAccess) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !access.comment {
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
	if input.Visibility == "internal" && !access.edit {
		respondError(w, http.StatusForbidden, "staff access is required for internal notes")
		cleanupStoredUploads(uploads)
		return
	}
	updated, ok := s.applyTicketPatch(w, auth, ticket, access, ticketPatchInput{Comment: &input}, attachmentInputs(uploads))
	if !ok {
		cleanupStoredUploads(uploads)
		return
	}
	s.respondTicket(w, http.StatusCreated, auth.User, updated, access)
}

func (s *Server) handleTicketRead(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket, access ticketAccess) {
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
	s.respondTicket(w, http.StatusOK, auth.User, updated, access)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := s.store.ListUsers()
		if err != nil {
			respondStoreError(w, err)
			return
		}
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
		if strings.TrimSpace(input.Password) != "" {
			created, err := s.store.CreateUser(input)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			s.dispatchEventsSoon()
			respondJSON(w, http.StatusCreated, store.ToPublicUser(created))
			return
		}
		created, link, token, err := s.store.CreateUserWithSetupLink(input, accountLinkExpiry)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		queued := s.options.EmailNotifications
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
	parts := routeParts(r.URL.Path, "/api/users/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, ok := parsePositiveID(w, parts[0], "invalid user id")
	if !ok {
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
	queued := s.options.EmailNotifications
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusCreated, userAccountLinkResponse(user, s.accountLinkURL(link.Purpose, token), link.ExpiresAt, queued, s.options.EmailNotifications))
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	if !s.requireBrowserSession(w, auth) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		tokens, err := s.store.ListAPITokens(auth.User.ID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
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
	if !s.requireBrowserSession(w, auth) {
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
	s.handleWebhookCollection(w, r, auth, nil)
}

func (s *Server) handleProductWebhooks(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess) {
	if !access.manage {
		respondError(w, http.StatusForbidden, "product manager access is required")
		return
	}
	s.handleWebhookCollection(w, r, auth, &productID)
}

func (s *Server) handleWebhookCollection(w http.ResponseWriter, r *http.Request, auth authContext, productID *int64) {
	switch r.Method {
	case http.MethodGet:
		hooks, err := s.store.ListWebhooks(productID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"webhooks": publicWebhooks(hooks),
			"events":   store.Events(),
		})
	case http.MethodPost:
		var input store.CreateWebhook
		if !decodeJSON(w, r, &input) {
			return
		}
		input.ProductID = productID
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
	parts := routeParts(r.URL.Path, "/api/webhooks/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, ok := parsePositiveID(w, parts[0], "invalid webhook id")
	if !ok {
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
	} else {
		access, err := s.productAccess(auth.User, *hook.ProductID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		if !access.manage {
			respondError(w, http.StatusForbidden, "product manager access is required")
			return
		}
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
	body, err := json.Marshal(payload)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	delivery, err := s.deliverWebhook(hook, "webhook.test", 0, body)
	if err != nil {
		respondStoreError(w, err)
		return
	}
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
	deliveries, err := s.store.ListDeliveries(nil, 50)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
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
	page, err := s.store.ListEmailNotificationsPage(store.EmailNotificationFilter{
		Status: r.URL.Query().Get("status"),
		Query:  r.URL.Query().Get("q"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	stats, err := s.store.EmailNotificationStats()
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"notifications":              page.Notifications,
		"total":                      page.Total,
		"limit":                      page.Limit,
		"offset":                     page.Offset,
		"enabled":                    s.options.EmailNotifications,
		"notification_delay_seconds": int(s.options.NotificationDelay.Seconds()),
		"stats":                      stats,
	})
}

func (s *Server) handleEmailNotificationByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	rest := trimRoutePrefix(r.URL.Path, "/api/email-notifications/")
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
	id, ok := parsePositiveID(w, parts[0], "invalid email notification id")
	if !ok {
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
	recipientName := defaultString(auth.User.DisplayName, auth.User.Email)
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
	page, err := s.store.ListAuditEventsPage(store.AuditEventFilter{
		Query:  r.URL.Query().Get("q"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"events": page.Events,
		"total":  page.Total,
		"limit":  page.Limit,
		"offset": page.Offset,
	})
}

func (s *Server) handleProductDeliveries(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess) {
	if !access.manage {
		respondError(w, http.StatusForbidden, "product manager access is required")
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	deliveries, err := s.store.ListDeliveries(&productID, 50)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
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

func (s *Server) currentAuth(r *http.Request) (authContext, error) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		user, csrf, err := s.store.UserBySession(cookie.Value)
		if err == nil {
			return authContext{User: user, CSRF: csrf, SessionToken: cookie.Value}, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return authContext{}, err
		}
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		token := strings.TrimSpace(header[7:])
		user, err := s.store.UserByAPIToken(token)
		if err == nil {
			return authContext{User: user, ViaToken: true}, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return authContext{}, err
		}
	}
	return authContext{}, store.ErrNotFound
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	setupRequired, err := s.store.SetupRequired()
	if err != nil {
		respondStoreError(w, err)
		return authContext{}, false
	}
	if setupRequired {
		respondError(w, http.StatusConflict, "setup is required")
		return authContext{}, false
	}
	auth, err := s.currentAuth(r)
	if errors.Is(err, store.ErrNotFound) {
		respondError(w, http.StatusUnauthorized, "authentication is required")
		return authContext{}, false
	}
	if err != nil {
		respondStoreError(w, err)
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

func (s *Server) requireBrowserSession(w http.ResponseWriter, auth authContext) bool {
	if auth.ViaToken || strings.TrimSpace(auth.SessionToken) == "" {
		respondError(w, http.StatusForbidden, "browser session is required")
		return false
	}
	return true
}

func (s *Server) requireHTTPS(w http.ResponseWriter, r *http.Request) bool {
	if s.requestIsSecure(r) {
		return true
	}
	respondError(w, http.StatusBadRequest, "HTTPS is required for browser sessions")
	return false
}

func (s *Server) verifyCSRF(w http.ResponseWriter, r *http.Request, expected string) bool {
	if !s.sameOrigin(r) {
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

func (s *Server) isSupportTicketRequester(user store.User, ticket store.Ticket) bool {
	return user.ID > 0 && user.ID == ticket.RequesterUserID
}

type productAccess struct {
	role         string
	read         bool
	manage       bool
	createTicket bool
}

func (s *Server) productAccess(user store.User, productID int64) (productAccess, error) {
	if isAdmin(user) {
		return productAccess{role: "manager", read: true, manage: true, createTicket: true}, nil
	}
	role, err := s.store.ProductRole(user.ID, productID)
	if errors.Is(err, store.ErrNotFound) {
		return productAccess{}, nil
	}
	if err != nil {
		return productAccess{}, err
	}
	return productAccess{
		role:         role,
		read:         true,
		manage:       !isCustomer(user) && role == "manager",
		createTicket: role == "manager" || role == "staff" || role == "customer",
	}, nil
}

type ticketAccess struct {
	read         bool
	comment      bool
	edit         bool
	viewAssignee bool
}

func (s *Server) ticketAccess(user store.User, ticket store.Ticket) (ticketAccess, error) {
	product, err := s.productAccess(user, ticket.ProductID)
	if err != nil || !product.read {
		return ticketAccess{}, err
	}
	role := product.role
	requesterOnly := isCustomer(user) || role == "customer"
	return ticketAccess{
		read:         !requesterOnly || s.isSupportTicketRequester(user, ticket),
		comment:      product.createTicket,
		edit:         !isCustomer(user) && (role == "manager" || role == "staff"),
		viewAssignee: !isCustomer(user) && role != "customer",
	}, nil
}

func (s *Server) ticketForUser(user store.User, ticket store.Ticket, access ticketAccess) (store.Ticket, error) {
	if !access.edit {
		ticket.Comments = publicComments(ticket.Comments)
	}
	if !access.viewAssignee {
		ticket.AssigneeUserID = 0
		ticket.AssigneeEmail = ""
	}
	summary, err := s.store.TicketSummaryForUser(user, ticket.ID)
	if err != nil {
		return store.Ticket{}, err
	}
	ticket.UnreadCount = summary.UnreadCount
	ticket.HasUnread = summary.HasUnread
	ticket.LastReadAt = summary.LastReadAt
	return ticket, nil
}

func (s *Server) respondTicket(w http.ResponseWriter, status int, user store.User, ticket store.Ticket, access ticketAccess) {
	ticket, err := s.ticketForUser(user, ticket, access)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, status, ticket)
}

func (s *Server) respondTicketForUser(w http.ResponseWriter, status int, user store.User, ticket store.Ticket) {
	access, err := s.ticketAccess(user, ticket)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.respondTicket(w, status, user, ticket, access)
}

type ticketListResult struct {
	Tickets     []store.TicketSummary `json:"tickets"`
	Counts      map[string]int        `json:"counts"`
	UnreadTotal int                   `json:"unread_total"`
	Limit       int                   `json:"limit"`
	Offset      int                   `json:"offset"`
	HasMore     bool                  `json:"has_more"`
}

func ticketSummaryFilter(query url.Values, productID int64, limit, offset int) store.TicketSummaryFilter {
	assigneeUserID, _ := strconv.ParseInt(query.Get("assignee_user_id"), 10, 64)
	return store.TicketSummaryFilter{
		Query:                      query.Get("q"),
		Statuses:                   queryStatuses(query),
		ProductID:                  productID,
		AssigneeUserID:             assigneeUserID,
		UnreadOnly:                 queryFlag(query, "unread"),
		IncludeUnreadOutsideStatus: queryFlag(query, "include_unread_outside_status"),
		Sort:                       query.Get("sort"),
		Direction:                  query.Get("direction"),
		Limit:                      limit,
		Offset:                     offset,
	}
}

func (s *Server) listTicketsForQuery(user store.User, filter store.TicketSummaryFilter) (ticketListResult, error) {
	if isCustomer(user) {
		filter.AssigneeUserID = 0
	}
	page, err := s.store.ListTicketSummariesPage(user, filter)
	if err != nil {
		return ticketListResult{}, err
	}
	aggregates, err := s.store.TicketSummaryAggregatesForUser(user, filter.ProductID)
	if err != nil {
		return ticketListResult{}, err
	}
	for i := range page.Tickets {
		if !s.canViewTicketSummaryAssignee(user, page.Tickets[i]) {
			page.Tickets[i].AssigneeUserID = 0
			page.Tickets[i].AssigneeEmail = ""
		}
	}
	return ticketListResult{
		Tickets:     page.Tickets,
		Counts:      aggregates.Counts,
		UnreadTotal: aggregates.UnreadTotal,
		Limit:       page.Limit,
		Offset:      page.Offset,
		HasMore:     page.HasMore,
	}, nil
}

func (s *Server) canViewTicketSummaryAssignee(user store.User, summary store.TicketSummary) bool {
	if isCustomer(user) {
		return false
	}
	return isAdmin(user) || summary.ProductRole != "customer"
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

func (s *Server) eventContext(r *http.Request, actor store.User) store.EventContext {
	ctx := store.EventContext{
		Enabled: true,
		Actor:   store.EventActorFromUser(actor),
	}
	if r != nil {
		ctx.IP = s.clientIP(r)
	}
	return ctx
}

func (s *Server) dispatchEventsSoon() {
	if err := s.dispatchPendingEvents(context.Background(), 10); err != nil && s.options.Logger != nil {
		s.options.Logger.Printf("domain event dispatch: %v", err)
	}
}
