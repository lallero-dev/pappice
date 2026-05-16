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
	"strconv"
	"strings"
	"time"

	"pemmece/internal/security"
	"pemmece/internal/store"
)

//go:embed web/index.html web/static/*
var assets embed.FS

const (
	sessionCookieName      = "pemmece_session"
	defaultEmailBatchDelay = 20 * time.Second
	accountLinkExpiry      = 24 * time.Hour
	defaultSessionTTL      = 14 * 24 * time.Hour
)

type RateLimit struct {
	Limit  int
	Window time.Duration
}

type Options struct {
	AllowInsecureWebhooks bool
	AllowPrivateWebhooks  bool
	EmailNotifications    bool
	EmailBatchDelay       time.Duration
	PublicURL             string
	SessionTTL            time.Duration
	LoginRateLimit        RateLimit
	AccountLinkRateLimit  RateLimit
}

type Server struct {
	store              *store.Store
	started            time.Time
	mux                *http.ServeMux
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

func (input ticketPatchInput) updateIssue() store.UpdateIssue {
	return store.UpdateIssue{
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

func (input ticketPatchInput) onlyAssigneePatch() bool {
	return input.Assignee != nil && input.Title == nil && input.Description == nil && input.Status == nil && input.Priority == nil
}

func New(tracker *store.Store, opts ...Options) http.Handler {
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
	return securityHeaders(s.mux)
}

func (s *Server) routes() {
	staticFiles, err := fs.Sub(assets, "web/static")
	if err != nil {
		panic(err)
	}

	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/support", s.handleSupportIndex)
	s.mux.HandleFunc("/support/", s.handleSupportIndex)
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
	s.mux.HandleFunc("/api/projects", s.handleProjects)
	s.mux.HandleFunc("/api/projects/", s.handleProjectByID)
	s.mux.HandleFunc("/api/tickets", s.handleTickets)
	s.mux.HandleFunc("/api/tickets/", s.handleTicketByID)
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
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
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

func (s *Server) handleSupportIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/support" && !strings.HasPrefix(r.URL.Path, "/support/tickets/") {
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
		"name":           "pemmece",
		"started_at":     s.started,
		"needs_setup":    s.store.SetupRequired(),
		"statuses":       store.Statuses(),
		"priorities":     store.Priorities(),
		"roles":          store.Roles(),
		"project_roles":  store.ProjectRoles(),
		"webhook_events": store.Events(),
		"email_enabled":  s.options.EmailNotifications,
	})
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
	})
	if err != nil {
		respondStoreError(w, err)
		return
	}
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
	updated, err := s.store.ChangePassword(auth.User.ID, input.CurrentPassword, input.NewPassword, auth.SessionToken)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.audit(r, auth.User, "password.changed", "user", auth.User.ID, auth.User.Username, nil)
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
	user, err := s.store.CreateFirstAdmin(input)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	csrf, ok := s.issueSession(w, user.ID)
	if !ok {
		return
	}
	s.audit(r, user, "setup.completed", "user", user.ID, user.Username, nil)
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
	csrf, ok := s.issueSession(w, user.ID)
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
		user, err := s.store.ConsumeAccountLink(token, input.Password)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		csrf, ok := s.issueSession(w, user.ID)
		if !ok {
			return
		}
		s.accountLinkLimiter.Reset(limitKey)
		respondJSON(w, http.StatusOK, map[string]any{
			"user":       store.ToPublicUser(user),
			"csrf_token": csrf,
		})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{"projects": s.store.ListProjects(auth.User)})
	case http.MethodPost:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		var input store.CreateProject
		if !decodeJSON(w, r, &input) {
			return
		}
		project, err := s.store.CreateProject(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "product.created", "product", project.ID, project.Key, map[string]any{"name": project.Name})
		respondJSON(w, http.StatusCreated, project)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProjectByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	projectID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || projectID < 1 {
		respondError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	if len(parts) == 1 {
		s.handleSingleProject(w, r, auth, projectID)
		return
	}
	switch parts[1] {
	case "members":
		s.handleProjectMembers(w, r, auth, projectID, parts[2:])
	case "tickets":
		s.handleProjectIssues(w, r, auth, projectID)
	case "webhooks":
		s.handleProjectWebhooks(w, r, auth, projectID)
	case "webhook-deliveries":
		s.handleProjectDeliveries(w, r, auth, projectID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSingleProject(w http.ResponseWriter, r *http.Request, auth authContext, projectID int64) {
	switch r.Method {
	case http.MethodGet:
		if !s.canReadProject(auth.User, projectID) {
			respondError(w, http.StatusNotFound, "not found")
			return
		}
		project, err := s.store.GetProject(projectID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		if role, ok := s.store.ProjectRole(auth.User.ID, projectID); ok {
			project.Role = role
		}
		if isAdmin(auth.User) {
			project.Role = "owner"
		}
		respondJSON(w, http.StatusOK, project)
	case http.MethodPatch:
		if !s.canManageProject(auth.User, projectID) {
			respondError(w, http.StatusForbidden, "product owner access is required")
			return
		}
		var patch store.UpdateProject
		if !decodeJSON(w, r, &patch) {
			return
		}
		project, err := s.store.UpdateProject(projectID, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "product.updated", "product", project.ID, project.Key, map[string]any{"name": project.Name})
		respondJSON(w, http.StatusOK, project)
	case http.MethodDelete:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		project, err := s.store.GetProject(projectID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		if err := s.store.DeleteProject(projectID); err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "product.deleted", "product", project.ID, project.Key, map[string]any{"name": project.Name})
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleProjectMembers(w http.ResponseWriter, r *http.Request, auth authContext, projectID int64, rest []string) {
	if !s.canManageProject(auth.User, projectID) {
		respondError(w, http.StatusForbidden, "product owner access is required")
		return
	}
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			respondJSON(w, http.StatusOK, map[string]any{"members": s.store.ListProjectMembers(projectID), "roles": store.ProjectRoles()})
		case http.MethodPost:
			var input store.UpsertProjectMember
			if !decodeJSON(w, r, &input) {
				return
			}
			member, err := s.store.UpsertProjectMember(projectID, input)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			s.audit(r, auth.User, "product_member.upserted", "user", member.UserID, member.Username, map[string]any{
				"project_id": projectID,
				"role":       member.Role,
			})
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
		user, _ := s.store.GetUser(userID)
		if err := s.store.DeleteProjectMember(projectID, userID); err != nil {
			respondStoreError(w, err)
			return
		}
		targetName := user.Username
		if targetName == "" {
			targetName = strconv.FormatInt(userID, 10)
		}
		s.audit(r, auth.User, "product_member.removed", "user", userID, targetName, map[string]any{"project_id": projectID})
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleProjectIssues(w http.ResponseWriter, r *http.Request, auth authContext, projectID int64) {
	if !s.canReadProject(auth.User, projectID) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		issues := s.store.ListIssuesForUser(store.Filter{
			ProjectID: projectID,
			Query:     query.Get("q"),
			Statuses:  queryStatuses(query),
			Assignee:  query.Get("assignee"),
		}, auth.User)
		respondJSON(w, http.StatusOK, map[string]any{
			"tickets":    s.issuesForUser(auth.User, issues),
			"statuses":   store.Statuses(),
			"priorities": store.Priorities(),
		})
	case http.MethodPost:
		if !s.canCreateIssue(auth.User, projectID) {
			respondError(w, http.StatusForbidden, "product write access is required")
			return
		}
		var input store.CreateIssue
		if !decodeJSON(w, r, &input) {
			return
		}
		input.ProjectID = projectID
		customerTicket, ok := s.prepareIssueInput(w, auth.User, projectID, &input)
		if !ok {
			return
		}
		issue, err := s.store.CreateIssue(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.emitIssueEvent("ticket.created", issue, auth.User)
		if customerTicket {
			s.enqueueRequesterEmail("ticket.created", issue, "Pemmece Support")
		}
		respondJSON(w, http.StatusCreated, s.issueForUser(auth.User, issue))
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
		projectID, _ := strconv.ParseInt(query.Get("project_id"), 10, 64)
		issues := s.store.ListIssuesForUser(store.Filter{
			ProjectID: projectID,
			Query:     query.Get("q"),
			Statuses:  queryStatuses(query),
			Assignee:  query.Get("assignee"),
		}, auth.User)
		respondJSON(w, http.StatusOK, map[string]any{"tickets": s.issuesForUser(auth.User, issues)})
	case http.MethodPost:
		var input store.CreateIssue
		if !decodeJSON(w, r, &input) {
			return
		}
		if !s.canCreateIssue(auth.User, input.ProjectID) {
			respondError(w, http.StatusForbidden, "product write access is required")
			return
		}
		customerTicket, ok := s.prepareIssueInput(w, auth.User, input.ProjectID, &input)
		if !ok {
			return
		}
		issue, err := s.store.CreateIssue(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.emitIssueEvent("ticket.created", issue, auth.User)
		if customerTicket {
			s.enqueueRequesterEmail("ticket.created", issue, "Pemmece Support")
		}
		respondJSON(w, http.StatusCreated, s.issueForUser(auth.User, issue))
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleTicketByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/tickets/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	issue, err := s.store.GetIssue(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !s.canReadIssue(auth.User, issue) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	if len(parts) == 1 {
		s.handleSingleIssue(w, r, auth, issue)
		return
	}
	if len(parts) == 2 && parts[1] == "comments" {
		s.handleComments(w, r, auth, issue)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleSingleIssue(w http.ResponseWriter, r *http.Request, auth authContext, issue store.Issue) {
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, s.issueForUser(auth.User, issue))
	case http.MethodPatch:
		var input ticketPatchInput
		if !decodeJSON(w, r, &input) {
			return
		}
		updated, ok := s.applyTicketPatch(w, auth, issue, input)
		if !ok {
			return
		}
		respondJSON(w, http.StatusOK, s.issueForUser(auth.User, updated))
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch)
	}
}

func (s *Server) applyTicketPatch(w http.ResponseWriter, auth authContext, issue store.Issue, input ticketPatchInput) (store.Issue, bool) {
	hasPatch := input.hasTicketPatch()
	hasComment := input.Comment != nil && strings.TrimSpace(input.Comment.Body) != ""
	if !hasPatch && !hasComment {
		respondError(w, http.StatusBadRequest, "ticket changes or comment are required")
		return store.Issue{}, false
	}
	if hasPatch && !s.canEditIssue(auth.User, issue.ProjectID) {
		respondError(w, http.StatusForbidden, "agent access is required")
		return store.Issue{}, false
	}
	if hasComment && !s.canCommentIssue(auth.User, issue.ProjectID) {
		respondError(w, http.StatusForbidden, "product comment access is required")
		return store.Issue{}, false
	}

	var comment *store.AddComment
	if hasComment {
		next := *input.Comment
		next.Visibility = defaultString(next.Visibility, "public")
		if next.Visibility == "internal" && !s.canEditIssue(auth.User, issue.ProjectID) {
			respondError(w, http.StatusForbidden, "agent access is required for internal notes")
			return store.Issue{}, false
		}
		next.Author = defaultString(auth.User.DisplayName, auth.User.Username)
		comment = &next
	}

	result, err := s.store.SaveIssue(store.SaveIssueInput{
		IssueID: issue.ID,
		Patch:   input.updateIssue(),
		Comment: comment,
	})
	if err != nil {
		respondStoreError(w, err)
		return store.Issue{}, false
	}
	updated := result.Issue

	if result.HasPatch {
		s.emitIssueWebhook("ticket.updated", updated, auth.User)
		if result.AssignmentChanged {
			s.emitIssueWebhook("ticket.assigned", updated, auth.User)
		}
	}
	if result.PublicComment {
		s.emitIssueWebhook("ticket.commented", updated, auth.User)
	}
	s.enqueueTicketPatchEmails(input, updated, auth.User, result.Previous, result.AssignmentChanged, result.PublicComment)
	return updated, true
}

func (s *Server) enqueueTicketPatchEmails(input ticketPatchInput, updated store.Issue, actor store.User, previous store.Issue, assignmentChanged, publicComment bool) {
	if !input.hasTicketPatch() && !publicComment {
		return
	}
	event := "ticket.updated"
	if !input.hasTicketPatch() && publicComment {
		event = "ticket.commented"
	}
	if input.hasTicketPatch() && !publicComment && assignmentChanged && input.onlyAssigneePatch() {
		event = "ticket.assigned"
	}
	s.enqueueIssueEmails(event, updated, actor)
	if requesterEvent, ok := requesterTicketPatchEmailEvent(input, previous, updated, publicComment); ok && !s.isSupportTicketRequester(actor, previous) {
		s.enqueueRequesterEmail(requesterEvent, updated, defaultString(actor.DisplayName, actor.Username))
	}
}

func requesterTicketPatchEmailEvent(input ticketPatchInput, previous, updated store.Issue, publicComment bool) (string, bool) {
	if publicComment {
		return "ticket.commented", true
	}
	if input.Status != nil && !strings.EqualFold(strings.TrimSpace(previous.Status), strings.TrimSpace(updated.Status)) && requesterTerminalStatus(updated.Status) {
		return "ticket.updated", true
	}
	return "", false
}

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request, auth authContext, issue store.Issue) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.canCommentIssue(auth.User, issue.ProjectID) {
		respondError(w, http.StatusForbidden, "product comment access is required")
		return
	}
	var input store.AddComment
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Visibility = defaultString(input.Visibility, "public")
	if input.Visibility == "internal" && !s.canEditIssue(auth.User, issue.ProjectID) {
		respondError(w, http.StatusForbidden, "agent access is required for internal notes")
		return
	}
	input.Author = defaultString(auth.User.DisplayName, auth.User.Username)
	updated, err := s.store.AddComment(issue.ID, input)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if input.Visibility == "public" {
		s.emitIssueEvent("ticket.commented", updated, auth.User)
		if !s.isSupportTicketRequester(auth.User, issue) {
			s.enqueueRequesterEmail("ticket.commented", updated, defaultString(auth.User.DisplayName, auth.User.Username))
		}
	}
	respondJSON(w, http.StatusCreated, s.issueForUser(auth.User, updated))
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
		created, link, token, err := s.store.CreateUserWithSetupLink(input, accountLinkExpiry)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		queued := s.enqueueAccountLinkEmail("account.setup", created, token, link.ExpiresAt)
		s.audit(r, auth.User, "user.created", "user", created.ID, created.Username, map[string]any{"role": created.Role, "email": created.Email != ""})
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
		before, err := s.store.GetUser(id)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		var patch store.UpdateUser
		if !decodeJSON(w, r, &patch) {
			return
		}
		if patch.Password != nil {
			respondError(w, http.StatusBadRequest, "use password reset")
			return
		}
		user, err := s.store.UpdateUser(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "user.updated", "user", user.ID, user.Username, userPatchAuditDetails(before, user, patch))
		respondJSON(w, http.StatusOK, store.ToPublicUser(user))
	case http.MethodDelete:
		user, err := s.store.GetUser(id)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		if err := s.store.DeleteUser(id); err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "user.deleted", "user", user.ID, user.Username, map[string]any{"role": user.Role})
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
	user, link, token, err := s.store.CreatePasswordResetLink(userID, accountLinkExpiry)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	queued := s.enqueueAccountLinkEmail("account.reset", user, token, link.ExpiresAt)
	s.audit(r, auth.User, "user.password_reset_requested", "user", user.ID, user.Username, map[string]any{"email_queued": queued})
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
		token, raw, err := s.store.CreateAPIToken(auth.User.ID, input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "api_token.created", "api_token", token.ID, token.Name, nil)
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
	if err := s.store.DeleteAPIToken(auth.User.ID, id); err != nil {
		respondStoreError(w, err)
		return
	}
	s.audit(r, auth.User, "api_token.deleted", "api_token", id, strconv.FormatInt(id, 10), nil)
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
		input.ProjectID = nil
		hook, err := s.store.CreateWebhook(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "webhook.created", "webhook", hook.ID, hook.Name, map[string]any{"scope": "global"})
		respondJSON(w, http.StatusCreated, map[string]any{"webhook": store.ToPublicWebhook(hook), "secret": hook.Secret})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProjectWebhooks(w http.ResponseWriter, r *http.Request, auth authContext, projectID int64) {
	if !s.canManageProject(auth.User, projectID) {
		respondError(w, http.StatusForbidden, "product owner access is required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{
			"webhooks": publicWebhooks(s.store.ListWebhooks(&projectID)),
			"events":   store.Events(),
		})
	case http.MethodPost:
		var input store.CreateWebhook
		if !decodeJSON(w, r, &input) {
			return
		}
		input.ProjectID = &projectID
		hook, err := s.store.CreateWebhook(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "webhook.created", "webhook", hook.ID, hook.Name, map[string]any{"project_id": projectID})
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
	if hook.ProjectID == nil {
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
	} else if !s.canManageProject(auth.User, *hook.ProjectID) {
		respondError(w, http.StatusForbidden, "product owner access is required")
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
		updated, err := s.store.UpdateWebhook(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "webhook.updated", "webhook", updated.ID, updated.Name, map[string]any{"enabled": updated.Enabled})
		respondJSON(w, http.StatusOK, store.ToPublicWebhook(updated))
	case http.MethodDelete:
		if err := s.store.DeleteWebhook(id); err != nil {
			respondStoreError(w, err)
			return
		}
		s.audit(r, auth.User, "webhook.deleted", "webhook", hook.ID, hook.Name, nil)
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodPatch, http.MethodDelete)
	}
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
		"message":    "Pemmece test delivery",
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
	respondJSON(w, http.StatusOK, map[string]any{
		"notifications":       s.store.ListEmailNotifications(50),
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
	notification, err := s.store.RetryEmailNotification(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.audit(r, auth.User, "email_notification.retried", "email_notification", notification.ID, notification.Subject, map[string]any{"recipient": notification.RecipientEmail})
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
	subject := "Pemmece test email"
	bodyText := "This is a no-reply test email from Pemmece.\n\nIf you received this message, SMTP delivery is working."
	bodyHTML := "<!doctype html><meta charset=\"utf-8\"><p>This is a no-reply test email from Pemmece.</p><p>If you received this message, SMTP delivery is working.</p>"
	created, err := s.store.EnqueueEmailNotifications([]store.CreateEmailNotification{{
		UserID:         auth.User.ID,
		RecipientEmail: recipientEmail,
		RecipientName:  recipientName,
		Event:          "email.test",
		Subject:        subject,
		BodyText:       bodyText,
		BodyHTML:       bodyHTML,
	}})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.audit(r, auth.User, "email_notification.test_queued", "email_notification", created[0].ID, created[0].Subject, map[string]any{"recipient": recipientEmail})
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
	respondJSON(w, http.StatusOK, map[string]any{"events": s.store.ListAuditEvents(100)})
}

func (s *Server) handleProjectDeliveries(w http.ResponseWriter, r *http.Request, auth authContext, projectID int64) {
	if !s.canManageProject(auth.User, projectID) {
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
		if delivery.ProjectID != nil && *delivery.ProjectID == projectID {
			filtered = append(filtered, delivery)
		}
	}
	if len(filtered) > 50 {
		filtered = filtered[:50]
	}
	respondJSON(w, http.StatusOK, map[string]any{"deliveries": filtered})
}

func (s *Server) issueSession(w http.ResponseWriter, userID int64) (string, bool) {
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
	token := strings.TrimSpace(r.Header.Get("X-Pemmece-CSRF"))
	if token == "" || !security.ConstantTimeEqual(token, expected) {
		respondError(w, http.StatusForbidden, "valid CSRF token is required")
		return false
	}
	return true
}

func (s *Server) canReadProject(user store.User, projectID int64) bool {
	if isAdmin(user) {
		return true
	}
	_, ok := s.store.ProjectRole(user.ID, projectID)
	return ok
}

func (s *Server) canReadIssue(user store.User, issue store.Issue) bool {
	if isAdmin(user) {
		return true
	}
	role, ok := s.store.ProjectRole(user.ID, issue.ProjectID)
	if !ok {
		return false
	}
	if isCustomer(user) || role == "customer" {
		return s.isSupportTicketRequester(user, issue)
	}
	return true
}

func (s *Server) canManageProject(user store.User, projectID int64) bool {
	if isCustomer(user) {
		return false
	}
	if isAdmin(user) {
		return true
	}
	role, ok := s.store.ProjectRole(user.ID, projectID)
	return ok && role == "owner"
}

func (s *Server) canCreateIssue(user store.User, projectID int64) bool {
	return s.hasProjectRole(user, projectID, "owner", "agent", "customer")
}

func (s *Server) canCommentIssue(user store.User, projectID int64) bool {
	return s.hasProjectRole(user, projectID, "owner", "agent", "customer")
}

func (s *Server) canEditIssue(user store.User, projectID int64) bool {
	if isCustomer(user) {
		return false
	}
	return s.hasProjectRole(user, projectID, "owner", "agent")
}

func (s *Server) isSupportTicketRequester(user store.User, issue store.Issue) bool {
	email := strings.TrimSpace(user.Email)
	if email != "" && strings.EqualFold(email, strings.TrimSpace(issue.RequesterEmail)) {
		return true
	}
	return issue.Source == "portal" && strings.EqualFold(strings.TrimSpace(issue.Reporter), strings.TrimSpace(user.Username))
}

func (s *Server) prepareIssueInput(w http.ResponseWriter, user store.User, projectID int64, input *store.CreateIssue) (bool, bool) {
	input.ProjectID = projectID
	input.Reporter = user.Username
	if !s.isCustomerTicketCreator(user, projectID) {
		return false, true
	}
	requesterEmail := strings.TrimSpace(user.Email)
	if requesterEmail == "" {
		respondError(w, http.StatusBadRequest, "your account needs an email address before you can open support tickets")
		return true, false
	}
	input.Assignee = ""
	input.Priority = "normal"
	input.Source = "portal"
	input.RequesterName = defaultString(user.DisplayName, user.Username)
	input.RequesterEmail = requesterEmail
	return true, true
}

func (s *Server) isCustomerTicketCreator(user store.User, projectID int64) bool {
	if isCustomer(user) {
		return true
	}
	role, ok := s.store.ProjectRole(user.ID, projectID)
	return ok && role == "customer"
}

func (s *Server) issueForUser(user store.User, issue store.Issue) store.Issue {
	if s.canEditIssue(user, issue.ProjectID) {
		return issue
	}
	issue.Comments = publicComments(issue.Comments)
	return issue
}

func (s *Server) issuesForUser(user store.User, issues []store.Issue) []store.Issue {
	result := make([]store.Issue, 0, len(issues))
	for _, issue := range issues {
		result = append(result, s.issueForUser(user, issue))
	}
	return result
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

func (s *Server) hasProjectRole(user store.User, projectID int64, allowed ...string) bool {
	if isAdmin(user) {
		return true
	}
	role, ok := s.store.ProjectRole(user.ID, projectID)
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

func (s *Server) audit(r *http.Request, actor store.User, action, targetType string, targetID int64, targetName string, details map[string]any) {
	var detailsJSON string
	if len(details) > 0 {
		if data, err := json.Marshal(details); err == nil {
			detailsJSON = string(data)
		}
	}
	_, _ = s.store.RecordAuditEvent(store.CreateAuditEvent{
		ActorUserID:   actor.ID,
		ActorUsername: actor.Username,
		Action:        action,
		TargetType:    targetType,
		TargetID:      targetID,
		TargetName:    targetName,
		IP:            clientIP(r),
		DetailsJSON:   detailsJSON,
	})
}

func userPatchAuditDetails(before, after store.User, patch store.UpdateUser) map[string]any {
	details := make(map[string]any)
	if patch.DisplayName != nil && before.DisplayName != after.DisplayName {
		details["display_name_changed"] = true
	}
	if patch.Email != nil && before.Email != after.Email {
		details["email_changed"] = true
	}
	if patch.Role != nil && before.Role != after.Role {
		details["role_from"] = before.Role
		details["role_to"] = after.Role
	}
	if patch.Disabled != nil && before.Disabled != after.Disabled {
		details["disabled"] = after.Disabled
	}
	if len(details) == 0 {
		return nil
	}
	return details
}

func (s *Server) emitIssueEvent(event string, issue store.Issue, actor store.User) {
	s.emitIssueWebhook(event, issue, actor)
	s.enqueueIssueEmails(event, issue, actor)
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
