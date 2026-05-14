package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pemmece/internal/security"
	"pemmece/internal/store"
)

//go:embed web/index.html web/static/*
var assets embed.FS

const sessionCookieName = "pemmece_session"

type Options struct {
	AllowInsecureWebhooks bool
	AllowPrivateWebhooks  bool
	EmailNotifications    bool
	PublicURL             string
}

type Server struct {
	store   *store.Store
	started time.Time
	mux     *http.ServeMux
	client  *http.Client
	options Options
}

type authContext struct {
	User     store.User
	CSRF     string
	ViaToken bool
}

func New(tracker *store.Store, opts ...Options) http.Handler {
	options := Options{}
	if len(opts) > 0 {
		options = opts[0]
	}
	s := &Server{
		store:   tracker,
		started: time.Now().UTC(),
		mux:     http.NewServeMux(),
		client: &http.Client{
			Timeout: 8 * time.Second,
		},
		options: options,
	}
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
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))

	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/session", s.handleSession)
	s.mux.HandleFunc("/api/me", s.handleSession)
	s.mux.HandleFunc("/api/setup", s.handleSetup)
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/logout", s.handleLogout)
	s.mux.HandleFunc("/api/support/projects", s.handleSupportProjects)
	s.mux.HandleFunc("/api/support/tickets", s.handleSupportTickets)
	s.mux.HandleFunc("/api/support/tickets/", s.handleSupportTicketByToken)
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
	user, err := s.store.Authenticate(input.Username, input.Password)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	csrf, ok := s.issueSession(w, user.ID)
	if !ok {
		return
	}
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

func (s *Server) handleSupportProjects(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	projects := s.store.ListProjects(auth.User)
	writable := make([]store.Project, 0, len(projects))
	for _, project := range projects {
		if s.canCreateIssue(auth.User, project.ID) {
			writable = append(writable, project)
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"projects": writable})
}

func (s *Server) handleSupportTickets(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPost:
		var input struct {
			ProjectID   int64  `json:"project_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if !s.canCreateIssue(auth.User, input.ProjectID) {
			respondError(w, http.StatusForbidden, "product ticket access is required")
			return
		}
		requesterEmail := strings.TrimSpace(auth.User.Email)
		if requesterEmail == "" {
			respondError(w, http.StatusBadRequest, "your account needs an email address before you can open support tickets")
			return
		}
		requesterName := defaultString(auth.User.DisplayName, auth.User.Username)
		issue, err := s.store.CreateIssue(store.CreateIssue{
			ProjectID:      input.ProjectID,
			Title:          input.Title,
			Description:    input.Description,
			Priority:       "normal",
			Reporter:       auth.User.Username,
			Source:         "portal",
			RequesterName:  requesterName,
			RequesterEmail: requesterEmail,
		})
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.emitIssueEvent("ticket.created", issue, auth.User)
		s.enqueueRequesterEmail("ticket.created", issue, "Pemmece Support")
		respondJSON(w, http.StatusCreated, map[string]any{
			"ticket": publicTicket(issue),
			"url":    s.ticketURL(issue.CustomerToken),
		})
	default:
		methodNotAllowed(w, http.MethodPost)
	}
}

func (s *Server) handleSupportTicketByToken(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/support/tickets/"), "/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	token := parts[0]
	issue, err := s.store.GetIssueByCustomerToken(token)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !s.canAccessSupportTicket(auth.User, issue) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"ticket": publicTicket(issue)})
		return
	}
	if len(parts) == 2 && parts[1] == "comments" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var input struct {
			Body string `json:"body"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		author := defaultString(auth.User.DisplayName, auth.User.Username)
		updated, err := s.store.AddComment(issue.ID, store.AddComment{Author: author, Body: input.Body, Visibility: "public"})
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.emitIssueEvent("ticket.commented", updated, auth.User)
		if !s.isSupportTicketRequester(auth.User, issue) {
			s.enqueueRequesterEmail("ticket.commented", updated, author)
		}
		publicIssue, err := s.store.GetIssueByCustomerToken(token)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusCreated, map[string]any{"ticket": publicTicket(publicIssue)})
		return
	}
	http.NotFound(w, r)
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
		respondJSON(w, http.StatusOK, project)
	case http.MethodDelete:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		if err := s.store.DeleteProject(projectID); err != nil {
			respondStoreError(w, err)
			return
		}
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
		if err := s.store.DeleteProjectMember(projectID, userID); err != nil {
			respondStoreError(w, err)
			return
		}
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
		if !s.canEditIssue(auth.User, issue.ProjectID) {
			respondError(w, http.StatusForbidden, "agent access is required")
			return
		}
		var patch store.UpdateIssue
		if !decodeJSON(w, r, &patch) {
			return
		}
		updated, err := s.store.UpdateIssue(issue.ID, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.emitIssueEvent("ticket.updated", updated, auth.User)
		if patch.Assignee != nil && strings.TrimSpace(*patch.Assignee) != "" && !strings.EqualFold(strings.TrimSpace(*patch.Assignee), strings.TrimSpace(issue.Assignee)) {
			s.emitIssueEvent("ticket.assigned", updated, auth.User)
		}
		respondJSON(w, http.StatusOK, s.issueForUser(auth.User, updated))
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch)
	}
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
		created, err := s.store.CreateUser(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusCreated, store.ToPublicUser(created))
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := parseTrailingID(w, r.URL.Path, "/api/users/")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var patch store.UpdateUser
		if !decodeJSON(w, r, &patch) {
			return
		}
		user, err := s.store.UpdateUser(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, store.ToPublicUser(user))
	case http.MethodDelete:
		if err := s.store.DeleteUser(id); err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodPatch, http.MethodDelete)
	}
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
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
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
		respondJSON(w, http.StatusOK, store.ToPublicWebhook(updated))
	case http.MethodDelete:
		if err := s.store.DeleteWebhook(id); err != nil {
			respondStoreError(w, err)
			return
		}
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
		"notifications": s.store.ListEmailNotifications(50),
		"enabled":       s.options.EmailNotifications,
	})
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
	token, csrf, expires, err := s.store.CreateSession(userID)
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
			return authContext{User: user, CSRF: csrf}, true
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

func (s *Server) canAccessSupportTicket(user store.User, issue store.Issue) bool {
	if isAdmin(user) || s.canEditIssue(user, issue.ProjectID) {
		return true
	}
	return s.isSupportTicketRequester(user, issue)
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

func (s *Server) emitIssueEvent(event string, issue store.Issue, actor store.User) {
	payload := map[string]any{
		"event":      event,
		"created_at": time.Now().UTC(),
		"actor":      store.ToPublicUser(actor),
		"ticket":     issue,
	}
	body, _ := json.Marshal(payload)
	for _, hook := range s.store.ListWebhooksForEvent(event, issue.ProjectID) {
		go s.deliverWebhook(hook, event, issue.ID, body)
	}
	s.enqueueIssueEmails(event, issue, actor)
}

func (s *Server) enqueueIssueEmails(event string, issue store.Issue, actor store.User) {
	if !s.options.EmailNotifications {
		return
	}
	recipients := s.store.IssueEmailRecipients(event, issue, actor)
	if len(recipients) == 0 {
		return
	}
	project, _ := s.store.GetProject(issue.ProjectID)
	subject, textBody, htmlBody := s.issueEmailContent(event, project, issue, actor)
	inputs := make([]store.CreateEmailNotification, 0, len(recipients))
	for _, recipient := range recipients {
		inputs = append(inputs, store.CreateEmailNotification{
			ProjectID:      issue.ProjectID,
			IssueID:        issue.ID,
			UserID:         recipient.UserID,
			RecipientEmail: recipient.Email,
			RecipientName:  defaultString(recipient.DisplayName, recipient.Username),
			Event:          event,
			Subject:        subject,
			BodyText:       textBody,
			BodyHTML:       htmlBody,
		})
	}
	_, _ = s.store.EnqueueEmailNotifications(inputs)
}

func (s *Server) enqueueRequesterEmail(event string, issue store.Issue, actorName string) {
	if !s.options.EmailNotifications || strings.TrimSpace(issue.RequesterEmail) == "" || strings.TrimSpace(issue.CustomerToken) == "" {
		return
	}
	subject, textBody, htmlBody := s.requesterEmailContent(event, issue, actorName)
	_, _ = s.store.EnqueueEmailNotifications([]store.CreateEmailNotification{{
		ProjectID:      issue.ProjectID,
		IssueID:        issue.ID,
		UserID:         0,
		RecipientEmail: issue.RequesterEmail,
		RecipientName:  defaultString(issue.RequesterName, issue.RequesterEmail),
		Event:          event,
		Subject:        subject,
		BodyText:       textBody,
		BodyHTML:       htmlBody,
	}})
}

func (s *Server) requesterEmailContent(event string, issue store.Issue, actorName string) (string, string, string) {
	action := issueEventAction(event)
	if event == "ticket.created" {
		action = "Received"
	}
	subject := fmt.Sprintf("[%s] %s: %s", issue.Key, action, issue.Title)
	link := s.ticketURL(issue.CustomerToken)
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\n", subject)
	if strings.TrimSpace(actorName) != "" && event == "ticket.commented" {
		fmt.Fprintf(&text, "%s replied to your ticket.\n\n", actorName)
	}
	if link != "" {
		fmt.Fprintf(&text, "Open your ticket:\n%s\n\n", link)
	}
	text.WriteString("Replies to this email are not read. Please open Pemmece to continue the conversation.\n")

	var htmlBody strings.Builder
	htmlBody.WriteString("<!doctype html><meta charset=\"utf-8\">")
	fmt.Fprintf(&htmlBody, "<h1>%s</h1>", html.EscapeString(subject))
	if strings.TrimSpace(actorName) != "" && event == "ticket.commented" {
		fmt.Fprintf(&htmlBody, "<p><strong>%s</strong> replied to your ticket.</p>", html.EscapeString(actorName))
	}
	if link != "" {
		fmt.Fprintf(&htmlBody, "<p><a href=\"%s\">Open your ticket</a></p>", html.EscapeString(link))
	}
	htmlBody.WriteString("<p>Replies to this email are not read. Please open Pemmece to continue the conversation.</p>")
	return subject, strings.TrimSpace(text.String()), htmlBody.String()
}

func (s *Server) issueEmailContent(event string, project store.Project, issue store.Issue, actor store.User) (string, string, string) {
	actorName := defaultString(actor.DisplayName, actor.Username)
	action := issueEventAction(event)
	subject := fmt.Sprintf("[%s] %s: %s", issue.Key, action, issue.Title)
	projectLabel := issue.ProjectKey
	if project.Name != "" {
		projectLabel = fmt.Sprintf("%s / %s", project.Key, project.Name)
	}
	link := strings.TrimRight(s.options.PublicURL, "/")
	if link != "" {
		link += "/"
	}

	var text strings.Builder
	fmt.Fprintf(&text, "%s %s %s\n\n", actorName, strings.ToLower(action), issue.Key)
	fmt.Fprintf(&text, "Title: %s\n", issue.Title)
	fmt.Fprintf(&text, "Product: %s\n", projectLabel)
	fmt.Fprintf(&text, "Status: %s\nPriority: %s\n", issue.Status, issue.Priority)
	if strings.TrimSpace(issue.Assignee) != "" {
		fmt.Fprintf(&text, "Assignee: %s\n", issue.Assignee)
	}
	if strings.TrimSpace(issue.Reporter) != "" {
		fmt.Fprintf(&text, "Requester: %s\n", issue.Reporter)
	}
	if link != "" {
		fmt.Fprintf(&text, "Open: %s\n", link)
	}
	if strings.TrimSpace(issue.Description) != "" {
		fmt.Fprintf(&text, "\n%s\n", issue.Description)
	}
	if event == "ticket.commented" && len(issue.Comments) > 0 {
		comment := issue.Comments[len(issue.Comments)-1]
		fmt.Fprintf(&text, "\nLatest comment from %s:\n%s\n", comment.Author, comment.Body)
	}

	var htmlBody strings.Builder
	htmlBody.WriteString("<!doctype html><meta charset=\"utf-8\">")
	fmt.Fprintf(&htmlBody, "<p><strong>%s</strong> %s <strong>%s</strong>.</p>", html.EscapeString(actorName), html.EscapeString(strings.ToLower(action)), html.EscapeString(issue.Key))
	htmlBody.WriteString("<dl>")
	fmt.Fprintf(&htmlBody, "<dt>Title</dt><dd>%s</dd>", html.EscapeString(issue.Title))
	fmt.Fprintf(&htmlBody, "<dt>Product</dt><dd>%s</dd>", html.EscapeString(projectLabel))
	fmt.Fprintf(&htmlBody, "<dt>Status</dt><dd>%s</dd>", html.EscapeString(issue.Status))
	fmt.Fprintf(&htmlBody, "<dt>Priority</dt><dd>%s</dd>", html.EscapeString(issue.Priority))
	if strings.TrimSpace(issue.Assignee) != "" {
		fmt.Fprintf(&htmlBody, "<dt>Assignee</dt><dd>%s</dd>", html.EscapeString(issue.Assignee))
	}
	htmlBody.WriteString("</dl>")
	if link != "" {
		fmt.Fprintf(&htmlBody, "<p><a href=\"%s\">Open in Pemmece</a></p>", html.EscapeString(link))
	}
	if strings.TrimSpace(issue.Description) != "" {
		fmt.Fprintf(&htmlBody, "<pre>%s</pre>", html.EscapeString(issue.Description))
	}
	if event == "ticket.commented" && len(issue.Comments) > 0 {
		comment := issue.Comments[len(issue.Comments)-1]
		fmt.Fprintf(&htmlBody, "<h2>Latest comment from %s</h2><pre>%s</pre>", html.EscapeString(comment.Author), html.EscapeString(comment.Body))
	}
	return subject, strings.TrimSpace(text.String()), htmlBody.String()
}

func issueEventAction(event string) string {
	switch event {
	case "ticket.created":
		return "Created"
	case "ticket.commented":
		return "Commented on"
	case "ticket.assigned":
		return "Assigned"
	default:
		return "Updated"
	}
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

func (s *Server) deliverWebhook(hook store.Webhook, event string, issueID int64, body []byte) store.WebhookDelivery {
	started := time.Now()
	delivery := store.WebhookDelivery{
		WebhookID: hook.ID,
		ProjectID: hook.ProjectID,
		Event:     event,
		IssueID:   issueID,
	}
	if err := s.validateWebhookTarget(hook.URL); err != nil {
		delivery.Error = err.Error()
		delivery.DurationMS = time.Since(started).Milliseconds()
		_ = s.store.RecordDelivery(delivery)
		return delivery
	}
	req, err := http.NewRequest(http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		delivery.Error = err.Error()
		delivery.DurationMS = time.Since(started).Milliseconds()
		_ = s.store.RecordDelivery(delivery)
		return delivery
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "pemmece-webhook")
	req.Header.Set("X-Pemmece-Event", event)
	if hook.Secret != "" {
		req.Header.Set("X-Pemmece-Signature", "sha256="+security.HMACSHA256(hook.Secret, body))
	}

	resp, err := s.client.Do(req)
	delivery.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		delivery.Error = err.Error()
		_ = s.store.RecordDelivery(delivery)
		return delivery
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	delivery.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		delivery.Error = fmt.Sprintf("webhook returned HTTP %d", resp.StatusCode)
	}
	_ = s.store.RecordDelivery(delivery)
	return delivery
}

func (s *Server) validateWebhookTarget(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("invalid webhook URL")
	}
	if parsed.Scheme != "https" && !(s.options.AllowInsecureWebhooks && parsed.Scheme == "http") {
		return fmt.Errorf("webhook URL must use https")
	}
	if s.options.AllowPrivateWebhooks {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return fmt.Errorf("webhook private targets are blocked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		ip, ok := netip.AddrFromSlice(addr.IP)
		if !ok {
			return fmt.Errorf("webhook private targets are blocked")
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("webhook private targets are blocked")
		}
	}
	return nil
}

func publicWebhooks(hooks []store.Webhook) []store.PublicWebhook {
	public := make([]store.PublicWebhook, 0, len(hooks))
	for _, hook := range hooks {
		public = append(public, store.ToPublicWebhook(hook))
	}
	return public
}

func publicTicket(issue store.Issue) map[string]any {
	return map[string]any{
		"id":              issue.ID,
		"key":             issue.Key,
		"project_id":      issue.ProjectID,
		"project_key":     issue.ProjectKey,
		"title":           issue.Title,
		"description":     issue.Description,
		"status":          issue.Status,
		"priority":        issue.Priority,
		"requester_name":  issue.RequesterName,
		"requester_email": issue.RequesterEmail,
		"comments":        issue.Comments,
		"created_at":      issue.CreatedAt,
		"updated_at":      issue.UpdatedAt,
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
