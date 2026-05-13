package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"pemmece/internal/security"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrValidation = errors.New("validation failed")
	ErrConflict   = errors.New("conflict")
)

type Issue struct {
	ID          int64        `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Project     string       `json:"project"`
	Status      string       `json:"status"`
	Severity    string       `json:"severity"`
	Priority    string       `json:"priority"`
	Assignee    string       `json:"assignee"`
	Reporter    string       `json:"reporter"`
	Tags        []string     `json:"tags"`
	Comments    []Comment    `json:"comments"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	ClosedAt    *time.Time   `json:"closed_at,omitempty"`
	Commits     []CommitLink `json:"commits,omitempty"`
}

type Comment struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateIssue struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Severity    string   `json:"severity"`
	Priority    string   `json:"priority"`
	Assignee    string   `json:"assignee"`
	Reporter    string   `json:"reporter"`
	Tags        []string `json:"tags"`
}

type UpdateIssue struct {
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Project     *string   `json:"project"`
	Status      *string   `json:"status"`
	Severity    *string   `json:"severity"`
	Priority    *string   `json:"priority"`
	Assignee    *string   `json:"assignee"`
	Tags        *[]string `json:"tags"`
}

type AddComment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

type Filter struct {
	Query    string
	Status   string
	Project  string
	Assignee string
}

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"password_hash,omitempty"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type PublicUser struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateUser struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

type UpdateUser struct {
	DisplayName *string `json:"display_name"`
	Password    *string `json:"password"`
	Role        *string `json:"role"`
	Disabled    *bool   `json:"disabled"`
}

type Session struct {
	TokenHash string    `json:"token_hash"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type APIToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	TokenHash  string     `json:"token_hash"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type PublicAPIToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type CreateAPIToken struct {
	Name string `json:"name"`
}

type Webhook struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	Secret          string     `json:"secret,omitempty"`
	Events          []string   `json:"events"`
	Enabled         bool       `json:"enabled"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastStatus      int        `json:"last_status"`
	LastError       string     `json:"last_error,omitempty"`
	LastDeliveredAt *time.Time `json:"last_delivered_at,omitempty"`
}

type PublicWebhook struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	Events          []string   `json:"events"`
	Enabled         bool       `json:"enabled"`
	HasSecret       bool       `json:"has_secret"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastStatus      int        `json:"last_status"`
	LastError       string     `json:"last_error,omitempty"`
	LastDeliveredAt *time.Time `json:"last_delivered_at,omitempty"`
}

type CreateWebhook struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Secret  string   `json:"secret"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled"`
}

type UpdateWebhook struct {
	Name    *string   `json:"name"`
	URL     *string   `json:"url"`
	Secret  *string   `json:"secret"`
	Events  *[]string `json:"events"`
	Enabled *bool     `json:"enabled"`
}

type WebhookDelivery struct {
	ID         int64     `json:"id"`
	WebhookID  int64     `json:"webhook_id"`
	Event      string    `json:"event"`
	IssueID    int64     `json:"issue_id,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

type RepoConfig struct {
	Path          string     `json:"path"`
	ScanLimit     int        `json:"scan_limit"`
	LastScannedAt *time.Time `json:"last_scanned_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

type CommitLink struct {
	IssueID   int64     `json:"issue_id"`
	RepoPath  string    `json:"repo_path"`
	Hash      string    `json:"hash"`
	ShortHash string    `json:"short_hash"`
	Author    string    `json:"author"`
	Email     string    `json:"email"`
	Date      time.Time `json:"date"`
	Subject   string    `json:"subject"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data fileData
}

type fileData struct {
	NextIssueID    int64             `json:"next_issue_id"`
	NextCommentID  int64             `json:"next_comment_id"`
	NextUserID     int64             `json:"next_user_id"`
	NextAPITokenID int64             `json:"next_api_token_id"`
	NextWebhookID  int64             `json:"next_webhook_id"`
	NextDeliveryID int64             `json:"next_delivery_id"`
	Issues         []Issue           `json:"issues"`
	Users          []User            `json:"users"`
	Sessions       []Session         `json:"sessions"`
	APITokens      []APIToken        `json:"api_tokens"`
	Webhooks       []Webhook         `json:"webhooks"`
	Deliveries     []WebhookDelivery `json:"deliveries"`
	Repo           RepoConfig        `json:"repo"`
	Commits        []CommitLink      `json:"commits"`
}

var validStatuses = map[string]struct{}{
	"new":          {},
	"acknowledged": {},
	"confirmed":    {},
	"assigned":     {},
	"resolved":     {},
	"closed":       {},
}

var validSeverities = map[string]struct{}{
	"feature": {},
	"trivial": {},
	"minor":   {},
	"major":   {},
	"crash":   {},
	"blocker": {},
}

var validPriorities = map[string]struct{}{
	"low":    {},
	"normal": {},
	"high":   {},
	"urgent": {},
}

var validRoles = map[string]struct{}{
	"admin":     {},
	"developer": {},
	"reporter":  {},
	"viewer":    {},
}

var validEvents = map[string]struct{}{
	"issue.created":   {},
	"issue.updated":   {},
	"issue.commented": {},
	"repo.scanned":    {},
	"*":               {},
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,46}[a-z0-9]$`)

func Open(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: fileData{
			NextIssueID:    1,
			NextCommentID:  1,
			NextUserID:     1,
			NextAPITokenID: 1,
			NextWebhookID:  1,
			NextDeliveryID: 1,
			Repo: RepoConfig{
				ScanLimit: 200,
			},
		},
	}

	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(content, &s.data); err != nil {
		return nil, err
	}
	s.repairCountersLocked()
	return s, nil
}

func (s *Store) SetupRequired() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data.Users) == 0
}

func (s *Store) CreateFirstAdmin(input CreateUser) (User, error) {
	input.Role = "admin"
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Users) > 0 {
		return User{}, fmt.Errorf("%w: setup is already complete", ErrConflict)
	}
	return s.createUserLocked(input)
}

func (s *Store) CreateUser(input CreateUser) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createUserLocked(input)
}

func (s *Store) ListUsers() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]User, 0, len(s.data.Users))
	for _, user := range s.data.Users {
		users = append(users, publicUserCopy(user))
	}
	sort.SliceStable(users, func(i, j int) bool {
		return users[i].Username < users[j].Username
	})
	return users
}

func (s *Store) UpdateUser(id int64, patch UpdateUser) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Users {
		if s.data.Users[i].ID != id {
			continue
		}
		user := &s.data.Users[i]
		oldRole := user.Role
		oldDisabled := user.Disabled

		if patch.DisplayName != nil {
			user.DisplayName = defaultString(*patch.DisplayName, user.Username)
		}
		if patch.Password != nil {
			hash, err := security.HashPassword(*patch.Password)
			if err != nil {
				return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
			}
			user.PasswordHash = hash
		}
		if patch.Role != nil {
			role := strings.TrimSpace(*patch.Role)
			if !isValid(validRoles, role) {
				return User{}, fmt.Errorf("%w: invalid role %q", ErrValidation, role)
			}
			user.Role = role
		}
		if patch.Disabled != nil {
			user.Disabled = *patch.Disabled
		}
		if (oldRole == "admin" || !oldDisabled) && !s.hasActiveAdminLocked() {
			user.Role = oldRole
			user.Disabled = oldDisabled
			return User{}, fmt.Errorf("%w: at least one active admin is required", ErrValidation)
		}
		user.UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(); err != nil {
			return User{}, err
		}
		return publicUserCopy(*user), nil
	}
	return User{}, ErrNotFound
}

func (s *Store) DeleteUser(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Users {
		if s.data.Users[i].ID != id {
			continue
		}
		removed := s.data.Users[i]
		s.data.Users = append(s.data.Users[:i], s.data.Users[i+1:]...)
		if removed.Role == "admin" && !s.hasActiveAdminLocked() {
			s.data.Users = append(s.data.Users, removed)
			sort.SliceStable(s.data.Users, func(i, j int) bool {
				return s.data.Users[i].ID < s.data.Users[j].ID
			})
			return fmt.Errorf("%w: at least one active admin is required", ErrValidation)
		}
		s.removeUserSecretsLocked(id)
		return s.saveLocked()
	}
	return ErrNotFound
}

func (s *Store) Authenticate(username, password string) (User, error) {
	username = normalizeUsername(username)
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, user := range s.data.Users {
		if user.Username != username {
			continue
		}
		if user.Disabled {
			return User{}, fmt.Errorf("%w: user is disabled", ErrValidation)
		}
		if !security.VerifyPassword(user.PasswordHash, password) {
			return User{}, fmt.Errorf("%w: invalid username or password", ErrValidation)
		}
		return publicUserCopy(user), nil
	}
	return User{}, fmt.Errorf("%w: invalid username or password", ErrValidation)
}

func (s *Store) CreateSession(userID int64) (string, time.Time, error) {
	token, err := security.RandomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(14 * 24 * time.Hour)

	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.findUserByIDLocked(userID)
	if !ok || user.Disabled {
		return "", time.Time{}, ErrNotFound
	}
	s.data.Sessions = append(s.data.Sessions, Session{
		TokenHash: security.HashToken(token),
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: expires,
	})
	if err := s.saveLocked(); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

func (s *Store) UserBySession(token string) (User, bool) {
	hash := security.HashToken(token)
	now := time.Now().UTC()

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, session := range s.data.Sessions {
		if session.TokenHash != hash || !session.ExpiresAt.After(now) {
			continue
		}
		user, ok := s.findUserByIDLocked(session.UserID)
		if !ok || user.Disabled {
			return User{}, false
		}
		return publicUserCopy(user), true
	}
	return User{}, false
}

func (s *Store) DeleteSession(token string) error {
	hash := security.HashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Sessions {
		if s.data.Sessions[i].TokenHash == hash {
			s.data.Sessions = append(s.data.Sessions[:i], s.data.Sessions[i+1:]...)
			return s.saveLocked()
		}
	}
	return nil
}

func (s *Store) CreateAPIToken(userID int64, input CreateAPIToken) (PublicAPIToken, string, error) {
	name := defaultString(input.Name, "API token")
	raw, err := security.RandomToken()
	if err != nil {
		return PublicAPIToken{}, "", err
	}
	token := "pme_" + raw
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if user, ok := s.findUserByIDLocked(userID); !ok || user.Disabled {
		return PublicAPIToken{}, "", ErrNotFound
	}
	apiToken := APIToken{
		ID:        s.data.NextAPITokenID,
		UserID:    userID,
		Name:      name,
		Prefix:    token[:12],
		TokenHash: security.HashToken(token),
		CreatedAt: now,
	}
	s.data.NextAPITokenID++
	s.data.APITokens = append(s.data.APITokens, apiToken)
	if err := s.saveLocked(); err != nil {
		return PublicAPIToken{}, "", err
	}
	return publicAPIToken(apiToken), token, nil
}

func (s *Store) ListAPITokens(userID int64) []PublicAPIToken {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens := make([]PublicAPIToken, 0)
	for _, token := range s.data.APITokens {
		if token.UserID == userID {
			tokens = append(tokens, publicAPIToken(token))
		}
	}
	sort.SliceStable(tokens, func(i, j int) bool {
		return tokens[i].CreatedAt.After(tokens[j].CreatedAt)
	})
	return tokens
}

func (s *Store) DeleteAPIToken(userID, tokenID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.APITokens {
		if s.data.APITokens[i].ID == tokenID && s.data.APITokens[i].UserID == userID {
			s.data.APITokens = append(s.data.APITokens[:i], s.data.APITokens[i+1:]...)
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

func (s *Store) UserByAPIToken(token string) (User, bool) {
	hash := security.HashToken(token)
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.APITokens {
		if s.data.APITokens[i].TokenHash != hash {
			continue
		}
		user, ok := s.findUserByIDLocked(s.data.APITokens[i].UserID)
		if !ok || user.Disabled {
			return User{}, false
		}
		if s.data.APITokens[i].LastUsedAt == nil || now.Sub(*s.data.APITokens[i].LastUsedAt) > time.Hour {
			s.data.APITokens[i].LastUsedAt = &now
			_ = s.saveLocked()
		}
		return publicUserCopy(user), true
	}
	return User{}, false
}

func (s *Store) CreateIssue(input CreateIssue) (Issue, error) {
	now := time.Now().UTC()
	issue := Issue{
		Title:       strings.TrimSpace(input.Title),
		Description: strings.TrimSpace(input.Description),
		Project:     defaultString(input.Project, "Inbox"),
		Status:      "new",
		Severity:    defaultString(input.Severity, "minor"),
		Priority:    defaultString(input.Priority, "normal"),
		Assignee:    strings.TrimSpace(input.Assignee),
		Reporter:    strings.TrimSpace(input.Reporter),
		Tags:        normalizeTags(input.Tags),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if issue.Title == "" {
		return Issue{}, fmt.Errorf("%w: title is required", ErrValidation)
	}
	if !isValid(validSeverities, issue.Severity) {
		return Issue{}, fmt.Errorf("%w: invalid severity %q", ErrValidation, issue.Severity)
	}
	if !isValid(validPriorities, issue.Priority) {
		return Issue{}, fmt.Errorf("%w: invalid priority %q", ErrValidation, issue.Priority)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	issue.ID = s.data.NextIssueID
	s.data.NextIssueID++
	s.data.Issues = append(s.data.Issues, issue)
	if err := s.saveLocked(); err != nil {
		return Issue{}, err
	}
	return s.copyIssueLocked(issue), nil
}

func (s *Store) ListIssues(filter Filter) []Issue {
	filter.Query = strings.ToLower(strings.TrimSpace(filter.Query))
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Project = strings.TrimSpace(filter.Project)
	filter.Assignee = strings.TrimSpace(filter.Assignee)

	s.mu.RLock()
	defer s.mu.RUnlock()

	issues := make([]Issue, 0, len(s.data.Issues))
	for _, issue := range s.data.Issues {
		if filter.Status != "" && issue.Status != filter.Status {
			continue
		}
		if filter.Project != "" && issue.Project != filter.Project {
			continue
		}
		if filter.Assignee != "" && issue.Assignee != filter.Assignee {
			continue
		}
		if filter.Query != "" && !matchesQuery(issue, filter.Query) {
			continue
		}
		issues = append(issues, s.copyIssueLocked(issue))
	}
	sort.SliceStable(issues, func(i, j int) bool {
		return issues[i].UpdatedAt.After(issues[j].UpdatedAt)
	})
	return issues
}

func (s *Store) GetIssue(id int64) (Issue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, issue := range s.data.Issues {
		if issue.ID == id {
			return s.copyIssueLocked(issue), nil
		}
	}
	return Issue{}, ErrNotFound
}

func (s *Store) UpdateIssue(id int64, patch UpdateIssue) (Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Issues {
		if s.data.Issues[i].ID != id {
			continue
		}

		issue := &s.data.Issues[i]
		if patch.Title != nil {
			title := strings.TrimSpace(*patch.Title)
			if title == "" {
				return Issue{}, fmt.Errorf("%w: title is required", ErrValidation)
			}
			issue.Title = title
		}
		if patch.Description != nil {
			issue.Description = strings.TrimSpace(*patch.Description)
		}
		if patch.Project != nil {
			issue.Project = defaultString(*patch.Project, "Inbox")
		}
		if patch.Status != nil {
			status := strings.TrimSpace(*patch.Status)
			if !isValid(validStatuses, status) {
				return Issue{}, fmt.Errorf("%w: invalid status %q", ErrValidation, status)
			}
			issue.Status = status
			if status == "closed" || status == "resolved" {
				now := time.Now().UTC()
				issue.ClosedAt = &now
			} else {
				issue.ClosedAt = nil
			}
		}
		if patch.Severity != nil {
			severity := defaultString(*patch.Severity, "minor")
			if !isValid(validSeverities, severity) {
				return Issue{}, fmt.Errorf("%w: invalid severity %q", ErrValidation, severity)
			}
			issue.Severity = severity
		}
		if patch.Priority != nil {
			priority := defaultString(*patch.Priority, "normal")
			if !isValid(validPriorities, priority) {
				return Issue{}, fmt.Errorf("%w: invalid priority %q", ErrValidation, priority)
			}
			issue.Priority = priority
		}
		if patch.Assignee != nil {
			issue.Assignee = strings.TrimSpace(*patch.Assignee)
		}
		if patch.Tags != nil {
			issue.Tags = normalizeTags(*patch.Tags)
		}
		issue.UpdatedAt = time.Now().UTC()

		if err := s.saveLocked(); err != nil {
			return Issue{}, err
		}
		return s.copyIssueLocked(*issue), nil
	}
	return Issue{}, ErrNotFound
}

func (s *Store) AddComment(id int64, input AddComment) (Issue, error) {
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return Issue{}, fmt.Errorf("%w: comment body is required", ErrValidation)
	}
	author := defaultString(input.Author, "anonymous")
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Issues {
		if s.data.Issues[i].ID != id {
			continue
		}
		comment := Comment{
			ID:        s.data.NextCommentID,
			Author:    author,
			Body:      body,
			CreatedAt: now,
		}
		s.data.NextCommentID++
		s.data.Issues[i].Comments = append(s.data.Issues[i].Comments, comment)
		s.data.Issues[i].UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			return Issue{}, err
		}
		return s.copyIssueLocked(s.data.Issues[i]), nil
	}
	return Issue{}, ErrNotFound
}

func (s *Store) CreateWebhook(input CreateWebhook) (Webhook, error) {
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	events, err := normalizeEvents(input.Events)
	if err != nil {
		return Webhook{}, err
	}
	hook := Webhook{
		Name:      strings.TrimSpace(input.Name),
		URL:       strings.TrimSpace(input.URL),
		Secret:    strings.TrimSpace(input.Secret),
		Events:    events,
		Enabled:   enabled,
		CreatedAt: time.Now().UTC(),
	}
	hook.UpdatedAt = hook.CreatedAt
	if hook.Name == "" {
		return Webhook{}, fmt.Errorf("%w: webhook name is required", ErrValidation)
	}
	if err := validateWebhookURL(hook.URL); err != nil {
		return Webhook{}, err
	}
	if hook.Secret == "" {
		secret, err := security.RandomToken()
		if err != nil {
			return Webhook{}, err
		}
		hook.Secret = secret
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	hook.ID = s.data.NextWebhookID
	s.data.NextWebhookID++
	s.data.Webhooks = append(s.data.Webhooks, hook)
	if err := s.saveLocked(); err != nil {
		return Webhook{}, err
	}
	return copyWebhook(hook), nil
}

func (s *Store) ListWebhooks() []Webhook {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hooks := make([]Webhook, 0, len(s.data.Webhooks))
	for _, hook := range s.data.Webhooks {
		hooks = append(hooks, copyWebhook(hook))
	}
	sort.SliceStable(hooks, func(i, j int) bool {
		return hooks[i].ID < hooks[j].ID
	})
	return hooks
}

func (s *Store) ListWebhooksForEvent(event string) []Webhook {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hooks := make([]Webhook, 0)
	for _, hook := range s.data.Webhooks {
		if hook.Enabled && eventMatches(hook.Events, event) {
			hooks = append(hooks, copyWebhook(hook))
		}
	}
	return hooks
}

func (s *Store) UpdateWebhook(id int64, patch UpdateWebhook) (Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Webhooks {
		if s.data.Webhooks[i].ID != id {
			continue
		}
		hook := &s.data.Webhooks[i]
		if patch.Name != nil {
			hook.Name = strings.TrimSpace(*patch.Name)
			if hook.Name == "" {
				return Webhook{}, fmt.Errorf("%w: webhook name is required", ErrValidation)
			}
		}
		if patch.URL != nil {
			hook.URL = strings.TrimSpace(*patch.URL)
			if err := validateWebhookURL(hook.URL); err != nil {
				return Webhook{}, err
			}
		}
		if patch.Secret != nil {
			hook.Secret = strings.TrimSpace(*patch.Secret)
		}
		if patch.Events != nil {
			events, err := normalizeEvents(*patch.Events)
			if err != nil {
				return Webhook{}, err
			}
			hook.Events = events
		}
		if patch.Enabled != nil {
			hook.Enabled = *patch.Enabled
		}
		hook.UpdatedAt = time.Now().UTC()
		if err := s.saveLocked(); err != nil {
			return Webhook{}, err
		}
		return copyWebhook(*hook), nil
	}
	return Webhook{}, ErrNotFound
}

func (s *Store) DeleteWebhook(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Webhooks {
		if s.data.Webhooks[i].ID == id {
			s.data.Webhooks = append(s.data.Webhooks[:i], s.data.Webhooks[i+1:]...)
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

func (s *Store) RecordDelivery(delivery WebhookDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	delivery.ID = s.data.NextDeliveryID
	s.data.NextDeliveryID++
	delivery.CreatedAt = now
	s.data.Deliveries = append(s.data.Deliveries, delivery)
	if len(s.data.Deliveries) > 200 {
		s.data.Deliveries = append([]WebhookDelivery(nil), s.data.Deliveries[len(s.data.Deliveries)-200:]...)
	}
	for i := range s.data.Webhooks {
		if s.data.Webhooks[i].ID == delivery.WebhookID {
			s.data.Webhooks[i].LastStatus = delivery.StatusCode
			s.data.Webhooks[i].LastError = delivery.Error
			s.data.Webhooks[i].LastDeliveredAt = &now
			break
		}
	}
	return s.saveLocked()
}

func (s *Store) ListDeliveries(limit int) []WebhookDelivery {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	deliveries := append([]WebhookDelivery(nil), s.data.Deliveries...)
	sort.SliceStable(deliveries, func(i, j int) bool {
		return deliveries[i].CreatedAt.After(deliveries[j].CreatedAt)
	})
	if len(deliveries) > limit {
		deliveries = deliveries[:limit]
	}
	return deliveries
}

func (s *Store) RepoConfig() RepoConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Repo
}

func (s *Store) SetRepoConfig(config RepoConfig) (RepoConfig, error) {
	config.Path = strings.TrimSpace(config.Path)
	if config.ScanLimit < 1 || config.ScanLimit > 1000 {
		config.ScanLimit = 200
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	config.LastScannedAt = s.data.Repo.LastScannedAt
	config.LastError = s.data.Repo.LastError
	s.data.Repo = config
	if err := s.saveLocked(); err != nil {
		return RepoConfig{}, err
	}
	return s.data.Repo, nil
}

func (s *Store) ReplaceCommitLinks(repoPath string, links []CommitLink, scanErr string) (RepoConfig, error) {
	now := time.Now().UTC()
	repoPath = strings.TrimSpace(repoPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	kept := make([]CommitLink, 0, len(s.data.Commits))
	for _, link := range s.data.Commits {
		if link.RepoPath != repoPath {
			kept = append(kept, link)
		}
	}
	seen := make(map[string]struct{}, len(links))
	for _, link := range links {
		if link.IssueID < 1 || link.Hash == "" {
			continue
		}
		link.RepoPath = repoPath
		key := fmt.Sprintf("%d:%s", link.IssueID, link.Hash)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, link)
	}
	s.data.Commits = kept
	s.data.Repo.LastScannedAt = &now
	s.data.Repo.LastError = scanErr
	if err := s.saveLocked(); err != nil {
		return RepoConfig{}, err
	}
	return s.data.Repo, nil
}

func (s *Store) ListCommitLinks(issueID int64) []CommitLink {
	s.mu.RLock()
	defer s.mu.RUnlock()

	links := make([]CommitLink, 0)
	for _, link := range s.data.Commits {
		if issueID == 0 || link.IssueID == issueID {
			links = append(links, link)
		}
	}
	sort.SliceStable(links, func(i, j int) bool {
		return links[i].Date.After(links[j].Date)
	})
	return links
}

func Statuses() []string {
	return []string{"new", "acknowledged", "confirmed", "assigned", "resolved", "closed"}
}

func Severities() []string {
	return []string{"feature", "trivial", "minor", "major", "crash", "blocker"}
}

func Priorities() []string {
	return []string{"low", "normal", "high", "urgent"}
}

func Roles() []string {
	return []string{"admin", "developer", "reporter", "viewer"}
}

func Events() []string {
	return []string{"issue.created", "issue.updated", "issue.commented", "repo.scanned"}
}

func ToPublicUser(user User) PublicUser {
	return PublicUser{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		Disabled:    user.Disabled,
		CreatedAt:   user.CreatedAt,
		UpdatedAt:   user.UpdatedAt,
	}
}

func ToPublicWebhook(hook Webhook) PublicWebhook {
	return PublicWebhook{
		ID:              hook.ID,
		Name:            hook.Name,
		URL:             hook.URL,
		Events:          append([]string(nil), hook.Events...),
		Enabled:         hook.Enabled,
		HasSecret:       hook.Secret != "",
		CreatedAt:       hook.CreatedAt,
		UpdatedAt:       hook.UpdatedAt,
		LastStatus:      hook.LastStatus,
		LastError:       hook.LastError,
		LastDeliveredAt: hook.LastDeliveredAt,
	}
}

func (s *Store) createUserLocked(input CreateUser) (User, error) {
	username := normalizeUsername(input.Username)
	if !usernamePattern.MatchString(username) {
		return User{}, fmt.Errorf("%w: username must be 3-48 lowercase letters, numbers, dot, dash, or underscore", ErrValidation)
	}
	role := defaultString(input.Role, "reporter")
	if !isValid(validRoles, role) {
		return User{}, fmt.Errorf("%w: invalid role %q", ErrValidation, role)
	}
	if _, ok := s.findUserByUsernameLocked(username); ok {
		return User{}, fmt.Errorf("%w: username already exists", ErrConflict)
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	now := time.Now().UTC()
	user := User{
		ID:           s.data.NextUserID,
		Username:     username,
		DisplayName:  defaultString(input.DisplayName, username),
		Role:         role,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.data.NextUserID++
	s.data.Users = append(s.data.Users, user)
	if err := s.saveLocked(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	content, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".pemmece-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func (s *Store) repairCountersLocked() {
	if s.data.NextIssueID < 1 {
		s.data.NextIssueID = 1
	}
	if s.data.NextCommentID < 1 {
		s.data.NextCommentID = 1
	}
	if s.data.NextUserID < 1 {
		s.data.NextUserID = 1
	}
	if s.data.NextAPITokenID < 1 {
		s.data.NextAPITokenID = 1
	}
	if s.data.NextWebhookID < 1 {
		s.data.NextWebhookID = 1
	}
	if s.data.NextDeliveryID < 1 {
		s.data.NextDeliveryID = 1
	}
	if s.data.Repo.ScanLimit < 1 {
		s.data.Repo.ScanLimit = 200
	}
	for _, issue := range s.data.Issues {
		if issue.ID >= s.data.NextIssueID {
			s.data.NextIssueID = issue.ID + 1
		}
		for _, comment := range issue.Comments {
			if comment.ID >= s.data.NextCommentID {
				s.data.NextCommentID = comment.ID + 1
			}
		}
	}
	for _, user := range s.data.Users {
		if user.ID >= s.data.NextUserID {
			s.data.NextUserID = user.ID + 1
		}
	}
	for _, token := range s.data.APITokens {
		if token.ID >= s.data.NextAPITokenID {
			s.data.NextAPITokenID = token.ID + 1
		}
	}
	for _, hook := range s.data.Webhooks {
		if hook.ID >= s.data.NextWebhookID {
			s.data.NextWebhookID = hook.ID + 1
		}
	}
	for _, delivery := range s.data.Deliveries {
		if delivery.ID >= s.data.NextDeliveryID {
			s.data.NextDeliveryID = delivery.ID + 1
		}
	}
}

func (s *Store) copyIssueLocked(issue Issue) Issue {
	issue.Tags = append([]string(nil), issue.Tags...)
	issue.Comments = append([]Comment(nil), issue.Comments...)
	issue.Commits = nil
	for _, link := range s.data.Commits {
		if link.IssueID == issue.ID {
			issue.Commits = append(issue.Commits, link)
		}
	}
	sort.SliceStable(issue.Commits, func(i, j int) bool {
		return issue.Commits[i].Date.After(issue.Commits[j].Date)
	})
	return issue
}

func (s *Store) findUserByIDLocked(id int64) (User, bool) {
	for _, user := range s.data.Users {
		if user.ID == id {
			return user, true
		}
	}
	return User{}, false
}

func (s *Store) findUserByUsernameLocked(username string) (User, bool) {
	for _, user := range s.data.Users {
		if user.Username == username {
			return user, true
		}
	}
	return User{}, false
}

func (s *Store) hasActiveAdminLocked() bool {
	for _, user := range s.data.Users {
		if user.Role == "admin" && !user.Disabled {
			return true
		}
	}
	return false
}

func (s *Store) removeUserSecretsLocked(userID int64) {
	sessions := s.data.Sessions[:0]
	for _, session := range s.data.Sessions {
		if session.UserID != userID {
			sessions = append(sessions, session)
		}
	}
	s.data.Sessions = sessions

	tokens := s.data.APITokens[:0]
	for _, token := range s.data.APITokens {
		if token.UserID != userID {
			tokens = append(tokens, token)
		}
	}
	s.data.APITokens = tokens
}

func matchesQuery(issue Issue, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		issue.Title,
		issue.Description,
		issue.Project,
		issue.Assignee,
		issue.Reporter,
		strings.Join(issue.Tags, " "),
	}, " "))
	return strings.Contains(haystack, query)
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func isValid(allowed map[string]struct{}, value string) bool {
	_, ok := allowed[value]
	return ok
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func normalizeEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		return []string{"issue.created", "issue.updated", "issue.commented"}, nil
	}
	seen := make(map[string]struct{}, len(events))
	result := make([]string, 0, len(events))
	for _, event := range events {
		event = strings.TrimSpace(event)
		if event == "" {
			continue
		}
		if !isValid(validEvents, event) {
			return nil, fmt.Errorf("%w: invalid webhook event %q", ErrValidation, event)
		}
		if _, ok := seen[event]; ok {
			continue
		}
		seen[event] = struct{}{}
		result = append(result, event)
	}
	if len(result) == 0 {
		return []string{"issue.created", "issue.updated", "issue.commented"}, nil
	}
	return result, nil
}

func eventMatches(events []string, event string) bool {
	for _, allowed := range events {
		if allowed == "*" || allowed == event {
			return true
		}
	}
	return false
}

func validateWebhookURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: invalid webhook URL", ErrValidation)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: webhook URL must be http or https", ErrValidation)
	}
	return nil
}

func publicUserCopy(user User) User {
	user.PasswordHash = ""
	return user
}

func publicAPIToken(token APIToken) PublicAPIToken {
	return PublicAPIToken{
		ID:         token.ID,
		UserID:     token.UserID,
		Name:       token.Name,
		Prefix:     token.Prefix,
		CreatedAt:  token.CreatedAt,
		LastUsedAt: token.LastUsedAt,
	}
}

func copyWebhook(hook Webhook) Webhook {
	hook.Events = append([]string(nil), hook.Events...)
	return hook
}
