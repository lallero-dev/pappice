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
	"net/http"
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

type Server struct {
	store   *store.Store
	started time.Time
	mux     *http.ServeMux
	client  *http.Client
}

func New(tracker *store.Store) http.Handler {
	s := &Server{
		store:   tracker,
		started: time.Now().UTC(),
		mux:     http.NewServeMux(),
		client: &http.Client{
			Timeout: 8 * time.Second,
		},
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
	s.mux.HandleFunc("/api/me", s.handleMe)
	s.mux.HandleFunc("/api/setup", s.handleSetup)
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/logout", s.handleLogout)
	s.mux.HandleFunc("/api/issues", s.handleIssues)
	s.mux.HandleFunc("/api/issues/", s.handleIssueByID)
	s.mux.HandleFunc("/api/users", s.handleUsers)
	s.mux.HandleFunc("/api/users/", s.handleUserByID)
	s.mux.HandleFunc("/api/tokens", s.handleTokens)
	s.mux.HandleFunc("/api/tokens/", s.handleTokenByID)
	s.mux.HandleFunc("/api/webhooks", s.handleWebhooks)
	s.mux.HandleFunc("/api/webhooks/", s.handleWebhookByID)
	s.mux.HandleFunc("/api/webhook-deliveries", s.handleWebhookDeliveries)
	s.mux.HandleFunc("/api/repo", s.handleRepo)
	s.mux.HandleFunc("/api/repo/scan", s.handleRepoScan)
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
		"name":           "pemmece",
		"started_at":     s.started,
		"needs_setup":    s.store.SetupRequired(),
		"statuses":       store.Statuses(),
		"severities":     store.Severities(),
		"priorities":     store.Priorities(),
		"roles":          store.Roles(),
		"webhook_events": store.Events(),
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	user, ok := s.currentUser(r)
	respondJSON(w, http.StatusOK, map[string]any{
		"authenticated": ok,
		"needs_setup":   s.store.SetupRequired(),
		"user":          nullableUser(user, ok),
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
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
	if !s.issueSession(w, user.ID) {
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{
		"user": store.ToPublicUser(user),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
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
	if !s.issueSession(w, user.ID) {
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"user": store.ToPublicUser(user),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
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
		SameSite: http.SameSiteStrictMode,
	})
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAuth(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listIssues(w, r)
	case http.MethodPost:
		if !canWriteIssues(user.Role) {
			respondError(w, http.StatusForbidden, "write access is required")
			return
		}
		s.createIssue(w, r, user)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	issues := s.store.ListIssues(store.Filter{
		Query:    query.Get("q"),
		Status:   query.Get("status"),
		Project:  query.Get("project"),
		Assignee: query.Get("assignee"),
	})
	respondJSON(w, http.StatusOK, map[string]any{
		"issues":     issues,
		"statuses":   store.Statuses(),
		"severities": store.Severities(),
		"priorities": store.Priorities(),
	})
}

func (s *Server) createIssue(w http.ResponseWriter, r *http.Request, user store.User) {
	var input store.CreateIssue
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Reporter = user.Username
	issue, err := s.store.CreateIssue(input)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.emitIssueEvent("issue.created", issue, user)
	respondJSON(w, http.StatusCreated, issue)
}

func (s *Server) handleIssueByID(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAuth(w, r)
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

	if len(parts) == 1 {
		s.handleSingleIssue(w, r, id, user)
		return
	}
	if len(parts) == 2 && parts[1] == "comments" {
		s.handleComments(w, r, id, user)
		return
	}
	if len(parts) == 2 && parts[1] == "commits" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"commits": s.store.ListCommitLinks(id)})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleSingleIssue(w http.ResponseWriter, r *http.Request, id int64, user store.User) {
	switch r.Method {
	case http.MethodGet:
		issue, err := s.store.GetIssue(id)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, issue)
	case http.MethodPatch:
		if !canWriteIssues(user.Role) {
			respondError(w, http.StatusForbidden, "write access is required")
			return
		}
		var patch store.UpdateIssue
		if !decodeJSON(w, r, &patch) {
			return
		}
		issue, err := s.store.UpdateIssue(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.emitIssueEvent("issue.updated", issue, user)
		respondJSON(w, http.StatusOK, issue)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch)
	}
}

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request, id int64, user store.User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !canWriteIssues(user.Role) {
		respondError(w, http.StatusForbidden, "write access is required")
		return
	}
	var input store.AddComment
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Author = user.DisplayName
	issue, err := s.store.AddComment(id, input)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.emitIssueEvent("issue.commented", issue, user)
	respondJSON(w, http.StatusCreated, issue)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	_ = user

	switch r.Method {
	case http.MethodGet:
		users := s.store.ListUsers()
		public := make([]store.PublicUser, 0, len(users))
		for _, user := range users {
			public = append(public, store.ToPublicUser(user))
		}
		respondJSON(w, http.StatusOK, map[string]any{"users": public, "roles": store.Roles()})
	case http.MethodPost:
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
	user, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{"tokens": s.store.ListAPITokens(user.ID)})
	case http.MethodPost:
		var input store.CreateAPIToken
		if !decodeJSON(w, r, &input) {
			return
		}
		token, raw, err := s.store.CreateAPIToken(user.ID, input)
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
	user, ok := s.requireAuth(w, r)
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
	if err := s.store.DeleteAPIToken(user.ID, id); err != nil {
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
		hooks := s.publicWebhooks()
		respondJSON(w, http.StatusOK, map[string]any{
			"webhooks": hooks,
			"events":   store.Events(),
		})
	case http.MethodPost:
		var input store.CreateWebhook
		if !decodeJSON(w, r, &input) {
			return
		}
		hook, err := s.store.CreateWebhook(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusCreated, map[string]any{
			"webhook": store.ToPublicWebhook(hook),
			"secret":  hook.Secret,
		})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleWebhookByID(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r)
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

	if len(parts) == 2 && parts[1] == "test" {
		s.handleWebhookTest(w, r, id, user)
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
		hook, err := s.store.UpdateWebhook(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, store.ToPublicWebhook(hook))
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

func (s *Server) handleWebhookTest(w http.ResponseWriter, r *http.Request, id int64, user store.User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var hook store.Webhook
	found := false
	for _, item := range s.store.ListWebhooks() {
		if item.ID == id {
			hook = item
			found = true
			break
		}
	}
	if !found {
		respondStoreError(w, store.ErrNotFound)
		return
	}
	payload := map[string]any{
		"event":      "webhook.test",
		"created_at": time.Now().UTC(),
		"actor":      store.ToPublicUser(user),
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

func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, http.StatusOK, map[string]any{
			"repo":    s.store.RepoConfig(),
			"commits": s.store.ListCommitLinks(0),
		})
	case http.MethodPatch:
		user, ok := s.requireAdmin(w, r)
		if !ok {
			return
		}
		_ = user
		var input store.RepoConfig
		if !decodeJSON(w, r, &input) {
			return
		}
		config, err := s.store.SetRepoConfig(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"repo": config})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch)
	}
}

func (s *Server) handleRepoScan(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	config := s.store.RepoConfig()
	if strings.TrimSpace(config.Path) == "" {
		respondError(w, http.StatusBadRequest, "repository path is not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	commits, err := gitrepo.Scan(ctx, config.Path, config.ScanLimit)
	if err != nil {
		config, _ = s.store.ReplaceCommitLinks(config.Path, nil, err.Error())
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "repo": config})
		return
	}

	links := make([]store.CommitLink, 0)
	for _, commit := range commits {
		for _, issueID := range commit.IssueIDs {
			links = append(links, store.CommitLink{
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
	config, err = s.store.ReplaceCommitLinks(config.Path, links, "")
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.emitRepoEvent("repo.scanned", config, len(links), user)
	respondJSON(w, http.StatusOK, map[string]any{
		"repo":    config,
		"commits": links,
	})
}

func (s *Server) issueSession(w http.ResponseWriter, userID int64) bool {
	token, expires, err := s.store.CreateSession(userID)
	if err != nil {
		respondStoreError(w, err)
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return true
}

func (s *Server) currentUser(r *http.Request) (store.User, bool) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if user, ok := s.store.UserBySession(cookie.Value); ok {
			return user, true
		}
	}

	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		token := strings.TrimSpace(header[7:])
		if user, ok := s.store.UserByAPIToken(token); ok {
			return user, true
		}
		if user, ok := s.store.UserBySession(token); ok {
			return user, true
		}
	}
	return store.User{}, false
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	if s.store.SetupRequired() {
		respondError(w, http.StatusConflict, "setup is required")
		return store.User{}, false
	}
	user, ok := s.currentUser(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication is required")
		return store.User{}, false
	}
	return user, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	user, ok := s.requireAuth(w, r)
	if !ok {
		return store.User{}, false
	}
	if user.Role != "admin" {
		respondError(w, http.StatusForbidden, "admin role is required")
		return store.User{}, false
	}
	return user, true
}

func (s *Server) publicWebhooks() []store.PublicWebhook {
	hooks := s.store.ListWebhooks()
	public := make([]store.PublicWebhook, 0, len(hooks))
	for _, hook := range hooks {
		public = append(public, store.ToPublicWebhook(hook))
	}
	return public
}

func (s *Server) emitIssueEvent(event string, issue store.Issue, actor store.User) {
	payload := map[string]any{
		"event":      event,
		"created_at": time.Now().UTC(),
		"actor":      store.ToPublicUser(actor),
		"issue":      issue,
	}
	body, _ := json.Marshal(payload)
	for _, hook := range s.store.ListWebhooksForEvent(event) {
		go s.deliverWebhook(hook, event, issue.ID, body)
	}
}

func (s *Server) emitRepoEvent(event string, config store.RepoConfig, commitLinks int, actor store.User) {
	payload := map[string]any{
		"event":        event,
		"created_at":   time.Now().UTC(),
		"actor":        store.ToPublicUser(actor),
		"repo":         config,
		"commit_links": commitLinks,
	}
	body, _ := json.Marshal(payload)
	for _, hook := range s.store.ListWebhooksForEvent(event) {
		go s.deliverWebhook(hook, event, 0, body)
	}
}

func (s *Server) deliverWebhook(hook store.Webhook, event string, issueID int64, body []byte) store.WebhookDelivery {
	started := time.Now()
	delivery := store.WebhookDelivery{
		WebhookID: hook.ID,
		Event:     event,
		IssueID:   issueID,
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

func canWriteIssues(role string) bool {
	return role == "admin" || role == "developer" || role == "reporter"
}

func nullableUser(user store.User, ok bool) any {
	if !ok {
		return nil
	}
	return store.ToPublicUser(user)
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

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
