package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"pemmece/internal/gitrepo"
	"pemmece/internal/security"
	"pemmece/internal/store"
)

//go:embed web/index.html web/static/*
var assets embed.FS

const sessionCookieName = "pemmece_session"

type Options struct {
	AllowInsecureWebhooks bool
	AllowPrivateWebhooks  bool
	RepoRoots             []string
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
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))

	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/session", s.handleSession)
	s.mux.HandleFunc("/api/me", s.handleSession)
	s.mux.HandleFunc("/api/setup", s.handleSetup)
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/logout", s.handleLogout)
	s.mux.HandleFunc("/api/projects", s.handleProjects)
	s.mux.HandleFunc("/api/projects/", s.handleProjectByID)
	s.mux.HandleFunc("/api/issues", s.handleIssues)
	s.mux.HandleFunc("/api/issues/", s.handleIssueByID)
	s.mux.HandleFunc("/api/users", s.handleUsers)
	s.mux.HandleFunc("/api/users/", s.handleUserByID)
	s.mux.HandleFunc("/api/tokens", s.handleTokens)
	s.mux.HandleFunc("/api/tokens/", s.handleTokenByID)
	s.mux.HandleFunc("/api/webhooks", s.handleWebhooks)
	s.mux.HandleFunc("/api/webhooks/", s.handleWebhookByID)
	s.mux.HandleFunc("/api/webhook-deliveries", s.handleWebhookDeliveries)
	s.mux.HandleFunc("/api/repo", s.handleLegacyRepo)
	s.mux.HandleFunc("/api/repo/scan", s.handleLegacyRepo)
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"name":              "pemmece",
		"started_at":        s.started,
		"needs_setup":       s.store.SetupRequired(),
		"statuses":          store.Statuses(),
		"severities":        store.Severities(),
		"priorities":        store.Priorities(),
		"roles":             store.Roles(),
		"project_roles":     store.ProjectRoles(),
		"webhook_events":    store.Events(),
		"repo_scan_enabled": len(s.options.RepoRoots) > 0,
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
		respondError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if len(parts) == 1 {
		s.handleSingleProject(w, r, auth, projectID)
		return
	}
	switch parts[1] {
	case "members":
		s.handleProjectMembers(w, r, auth, projectID, parts[2:])
	case "issues":
		s.handleProjectIssues(w, r, auth, projectID)
	case "repo":
		s.handleProjectRepo(w, r, auth, projectID, parts[2:])
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
			respondError(w, http.StatusForbidden, "project owner access is required")
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
		respondError(w, http.StatusForbidden, "project owner access is required")
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
			Status:    query.Get("status"),
			Assignee:  query.Get("assignee"),
		}, auth.User)
		respondJSON(w, http.StatusOK, map[string]any{
			"issues":     issues,
			"statuses":   store.Statuses(),
			"severities": store.Severities(),
			"priorities": store.Priorities(),
		})
	case http.MethodPost:
		if !s.canCreateIssue(auth.User, projectID) {
			respondError(w, http.StatusForbidden, "project write access is required")
			return
		}
		var input store.CreateIssue
		if !decodeJSON(w, r, &input) {
			return
		}
		input.ProjectID = projectID
		input.Reporter = auth.User.Username
		issue, err := s.store.CreateIssue(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.emitIssueEvent("issue.created", issue, auth.User)
		respondJSON(w, http.StatusCreated, issue)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
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
			Status:    query.Get("status"),
			Assignee:  query.Get("assignee"),
		}, auth.User)
		respondJSON(w, http.StatusOK, map[string]any{"issues": issues})
	case http.MethodPost:
		var input store.CreateIssue
		if !decodeJSON(w, r, &input) {
			return
		}
		if !s.canCreateIssue(auth.User, input.ProjectID) {
			respondError(w, http.StatusForbidden, "project write access is required")
			return
		}
		input.Reporter = auth.User.Username
		issue, err := s.store.CreateIssue(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.emitIssueEvent("issue.created", issue, auth.User)
		respondJSON(w, http.StatusCreated, issue)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleIssueByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/issues/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, "invalid issue id")
		return
	}
	issue, err := s.store.GetIssue(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !s.canReadProject(auth.User, issue.ProjectID) {
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
	if len(parts) == 2 && parts[1] == "commits" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"commits": s.store.ListCommitLinks(issue.ID)})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleSingleIssue(w http.ResponseWriter, r *http.Request, auth authContext, issue store.Issue) {
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, issue)
	case http.MethodPatch:
		if !s.canEditIssue(auth.User, issue.ProjectID) {
			respondError(w, http.StatusForbidden, "project developer access is required")
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
		s.emitIssueEvent("issue.updated", updated, auth.User)
		respondJSON(w, http.StatusOK, updated)
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
		respondError(w, http.StatusForbidden, "project comment access is required")
		return
	}
	var input store.AddComment
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Author = defaultString(auth.User.DisplayName, auth.User.Username)
	updated, err := s.store.AddComment(issue.ID, input)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.emitIssueEvent("issue.commented", updated, auth.User)
	respondJSON(w, http.StatusCreated, updated)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
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
	auth, ok := s.requireAuth(w, r)
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
	auth, ok := s.requireAuth(w, r)
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
		respondError(w, http.StatusForbidden, "project owner access is required")
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
	auth, ok := s.requireAuth(w, r)
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
		respondError(w, http.StatusForbidden, "project owner access is required")
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

func (s *Server) handleProjectDeliveries(w http.ResponseWriter, r *http.Request, auth authContext, projectID int64) {
	if !s.canManageProject(auth.User, projectID) {
		respondError(w, http.StatusForbidden, "project owner access is required")
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

func (s *Server) handleProjectRepo(w http.ResponseWriter, r *http.Request, auth authContext, projectID int64, rest []string) {
	if len(rest) == 1 && rest[0] == "scan" {
		s.handleProjectRepoScan(w, r, auth, projectID)
		return
	}
	if len(rest) != 0 {
		http.NotFound(w, r)
		return
	}
	if !s.canReadProject(auth.User, projectID) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{
			"repo":    s.store.RepoConfig(projectID),
			"commits": s.store.ListProjectCommitLinks(projectID),
		})
	case http.MethodPatch:
		if !s.canManageProject(auth.User, projectID) {
			respondError(w, http.StatusForbidden, "project owner access is required")
			return
		}
		var input store.RepoConfig
		if !decodeJSON(w, r, &input) {
			return
		}
		if err := s.validateRepoPath(input.Path); err != nil && strings.TrimSpace(input.Path) != "" {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		config, err := s.store.SetRepoConfig(projectID, input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"repo": config})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch)
	}
}

func (s *Server) handleProjectRepoScan(w http.ResponseWriter, r *http.Request, auth authContext, projectID int64) {
	if !s.canManageProject(auth.User, projectID) {
		respondError(w, http.StatusForbidden, "project owner access is required")
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	project, err := s.store.GetProject(projectID)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	config := s.store.RepoConfig(projectID)
	if strings.TrimSpace(config.Path) == "" {
		respondError(w, http.StatusBadRequest, "repository path is not configured")
		return
	}
	if err := s.validateRepoPath(config.Path); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	commits, err := gitrepo.Scan(ctx, config.Path, config.ScanLimit)
	if err != nil {
		config, _ = s.store.ReplaceCommitLinks(projectID, config.Path, nil, err.Error())
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "repo": config})
		return
	}
	links := make([]store.CommitLink, 0)
	for _, commit := range commits {
		for _, ref := range commit.IssueRefs {
			if ref.ProjectKey != "" && !strings.EqualFold(ref.ProjectKey, project.Key) {
				continue
			}
			issueID, ok := s.store.IssueIDByProjectNumber(projectID, ref.Number)
			if !ok {
				continue
			}
			links = append(links, store.CommitLink{
				ProjectID: projectID,
				IssueID:   issueID,
				RepoPath:  config.Path,
				Hash:      commit.Hash,
				ShortHash: commit.ShortHash,
				Author:    commit.Author,
				Email:     commit.Email,
				Date:      commit.Date,
				Subject:   commit.Subject,
			})
		}
	}
	config, err = s.store.ReplaceCommitLinks(projectID, config.Path, links, "")
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.emitRepoEvent("repo.scanned", project, config, len(links), auth.User)
	respondJSON(w, http.StatusOK, map[string]any{"repo": config, "commits": links})
}

func (s *Server) handleLegacyRepo(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	respondError(w, http.StatusGone, "repository settings are project-scoped; use /api/projects/{id}/repo")
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

func (s *Server) canManageProject(user store.User, projectID int64) bool {
	if isAdmin(user) {
		return true
	}
	role, ok := s.store.ProjectRole(user.ID, projectID)
	return ok && role == "owner"
}

func (s *Server) canCreateIssue(user store.User, projectID int64) bool {
	return s.hasProjectRole(user, projectID, "owner", "developer", "reporter")
}

func (s *Server) canCommentIssue(user store.User, projectID int64) bool {
	return s.hasProjectRole(user, projectID, "owner", "developer", "reporter")
}

func (s *Server) canEditIssue(user store.User, projectID int64) bool {
	return s.hasProjectRole(user, projectID, "owner", "developer")
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
		"issue":      issue,
	}
	body, _ := json.Marshal(payload)
	for _, hook := range s.store.ListWebhooksForEvent(event, issue.ProjectID) {
		go s.deliverWebhook(hook, event, issue.ID, body)
	}
}

func (s *Server) emitRepoEvent(event string, project store.Project, config store.RepoConfig, commitLinks int, actor store.User) {
	payload := map[string]any{
		"event":        event,
		"created_at":   time.Now().UTC(),
		"actor":        store.ToPublicUser(actor),
		"project":      project,
		"repo":         config,
		"commit_links": commitLinks,
	}
	body, _ := json.Marshal(payload)
	for _, hook := range s.store.ListWebhooksForEvent(event, project.ID) {
		go s.deliverWebhook(hook, event, 0, body)
	}
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

func (s *Server) validateRepoPath(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if len(s.options.RepoRoots) == 0 {
		return fmt.Errorf("repository scanning is disabled; configure PEMMECE_REPO_ROOTS or -repo-root")
	}
	path, err := filepath.Abs(raw)
	if err != nil {
		return fmt.Errorf("invalid repository path")
	}
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		path = evaluated
	}
	for _, root := range s.options.RepoRoots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if evaluated, err := filepath.EvalSymlinks(rootAbs); err == nil {
			rootAbs = evaluated
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("repository path is outside the configured scan roots")
}

func publicWebhooks(hooks []store.Webhook) []store.PublicWebhook {
	public := make([]store.PublicWebhook, 0, len(hooks))
	for _, hook := range hooks {
		public = append(public, store.ToPublicWebhook(hook))
	}
	return public
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
