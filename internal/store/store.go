package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"pemmece/internal/security"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrValidation = errors.New("validation failed")
	ErrConflict   = errors.New("conflict")
)

type Issue struct {
	ID             int64      `json:"id"`
	ProjectID      int64      `json:"project_id"`
	ProjectKey     string     `json:"project_key"`
	Number         int64      `json:"number"`
	Key            string     `json:"key"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Project        string     `json:"project"`
	Status         string     `json:"status"`
	Severity       string     `json:"-"`
	Priority       string     `json:"priority"`
	Assignee       string     `json:"assignee"`
	Reporter       string     `json:"requester"`
	Source         string     `json:"source"`
	RequesterName  string     `json:"requester_name,omitempty"`
	RequesterEmail string     `json:"requester_email,omitempty"`
	CustomerToken  string     `json:"-"`
	Tags           []string   `json:"tags"`
	Comments       []Comment  `json:"comments"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
}

type Comment struct {
	ID         int64     `json:"id"`
	Author     string    `json:"author"`
	Body       string    `json:"body"`
	Visibility string    `json:"visibility"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateIssue struct {
	ProjectID      int64    `json:"project_id"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Project        string   `json:"project"`
	Severity       string   `json:"-"`
	Priority       string   `json:"priority"`
	Assignee       string   `json:"assignee"`
	Reporter       string   `json:"requester"`
	Source         string   `json:"source"`
	RequesterName  string   `json:"requester_name"`
	RequesterEmail string   `json:"requester_email"`
	Tags           []string `json:"tags"`
}

type UpdateIssue struct {
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Status      *string   `json:"status"`
	Severity    *string   `json:"-"`
	Priority    *string   `json:"priority"`
	Assignee    *string   `json:"assignee"`
	Tags        *[]string `json:"tags"`
}

type AddComment struct {
	Author     string `json:"author"`
	Body       string `json:"body"`
	Visibility string `json:"visibility"`
}

type Filter struct {
	Query     string
	Status    string
	ProjectID int64
	Assignee  string
}

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Email        string    `json:"email"`
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
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateUser struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

type UpdateUser struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	Password    *string `json:"password"`
	Role        *string `json:"role"`
	Disabled    *bool   `json:"disabled"`
}

type Session struct {
	TokenHash string    `json:"token_hash"`
	CSRFToken string    `json:"-"`
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

type Project struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Role        string    `json:"role,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateProject struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UpdateProject struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

type ProjectMember struct {
	ProjectID   int64     `json:"project_id"`
	UserID      int64     `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

type UpsertProjectMember struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

type Webhook struct {
	ID              int64      `json:"id"`
	ProjectID       *int64     `json:"project_id,omitempty"`
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
	ProjectID       *int64     `json:"project_id,omitempty"`
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
	ProjectID *int64   `json:"project_id"`
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Secret    string   `json:"secret"`
	Events    []string `json:"events"`
	Enabled   *bool    `json:"enabled"`
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
	ProjectID  *int64    `json:"project_id,omitempty"`
	Event      string    `json:"event"`
	IssueID    int64     `json:"ticket_id,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

type EmailRecipient struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        string `json:"role"`
}

type EmailNotification struct {
	ID             int64      `json:"id"`
	ProjectID      int64      `json:"project_id,omitempty"`
	IssueID        int64      `json:"ticket_id,omitempty"`
	UserID         int64      `json:"user_id"`
	RecipientEmail string     `json:"recipient_email"`
	RecipientName  string     `json:"recipient_name"`
	Event          string     `json:"event"`
	Subject        string     `json:"subject"`
	BodyText       string     `json:"body_text,omitempty"`
	BodyHTML       string     `json:"body_html,omitempty"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	NextAttemptAt  time.Time  `json:"next_attempt_at"`
	LockedUntil    *time.Time `json:"locked_until,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
}

type CreateEmailNotification struct {
	ProjectID      int64
	IssueID        int64
	UserID         int64
	RecipientEmail string
	RecipientName  string
	Event          string
	Subject        string
	BodyText       string
	BodyHTML       string
}

type Store struct {
	db *sql.DB
}

var validStatuses = map[string]struct{}{
	"new":      {},
	"open":     {},
	"pending":  {},
	"assigned": {},
	"resolved": {},
	"closed":   {},
}

var validSeverities = map[string]struct{}{
	"support":  {},
	"question": {},
	"incident": {},
	"task":     {},
}

var validPriorities = map[string]struct{}{
	"low":    {},
	"normal": {},
	"high":   {},
	"urgent": {},
}

var validGlobalRoles = map[string]struct{}{
	"admin":  {},
	"user":   {},
	"client": {},
}

var validProjectRoles = map[string]struct{}{
	"owner":    {},
	"agent":    {},
	"customer": {},
	"viewer":   {},
}

var validIssueSources = map[string]struct{}{
	"staff":  {},
	"portal": {},
}

var validCommentVisibility = map[string]struct{}{
	"public":   {},
	"internal": {},
}

var validEvents = map[string]struct{}{
	"ticket.created":   {},
	"ticket.updated":   {},
	"ticket.commented": {},
	"ticket.assigned":  {},
	"*":                {},
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,46}[a-z0-9]$`)
var projectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,15}$`)

func Open(path string) (*Store, error) {
	if path == "" {
		path = "pemmece.db"
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init() error {
	if _, err := s.db.Exec(`PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA journal_mode = WAL;`); err != nil && !strings.Contains(err.Error(), "memory") {
		return err
	}
	_, err := s.db.Exec(schemaSQL)
	if err != nil {
		return err
	}
	return s.migrate()
}

func (s *Store) migrate() error {
	if ok, err := s.columnExists("users", "email"); err != nil {
		return err
	} else if !ok {
		if _, err := s.db.Exec(`ALTER TABLE users ADD COLUMN email TEXT`); err != nil {
			return err
		}
	}
	if ok, err := s.usersRoleAllowsClient(); err != nil {
		return err
	} else if !ok {
		if err := s.rebuildUsersRoleConstraint(); err != nil {
			return err
		}
	}
	if ok, err := s.projectRolesAreTicketing(); err != nil {
		return err
	} else if !ok {
		if err := s.rebuildProjectMembersRoleConstraint(); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE project_members SET role = 'agent' WHERE role = 'developer'`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE project_members SET role = 'customer' WHERE role = 'reporter'`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE issues SET status = 'open' WHERE status IN ('acknowledged', 'confirmed')`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
		UPDATE webhooks
		SET events_json = replace(
			replace(
				replace(
					replace(events_json, 'issue.created', 'ticket.created'),
					'issue.updated', 'ticket.updated'
				),
				'issue.commented', 'ticket.commented'
			),
			'issue.assigned', 'ticket.assigned'
		)
	`); err != nil {
		return err
	}
	for _, migration := range []struct {
		table  string
		column string
		sql    string
	}{
		{"issues", "source", `ALTER TABLE issues ADD COLUMN source TEXT NOT NULL DEFAULT 'staff'`},
		{"issues", "requester_name", `ALTER TABLE issues ADD COLUMN requester_name TEXT NOT NULL DEFAULT ''`},
		{"issues", "requester_email", `ALTER TABLE issues ADD COLUMN requester_email TEXT NOT NULL DEFAULT ''`},
		{"issues", "customer_token", `ALTER TABLE issues ADD COLUMN customer_token TEXT`},
		{"comments", "visibility", `ALTER TABLE comments ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public'`},
	} {
		ok, err := s.columnExists(migration.table, migration.column)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := s.db.Exec(migration.sql); err != nil {
				return err
			}
		}
	}
	if notNull, err := s.columnNotNull("email_notifications", "user_id"); err != nil {
		return err
	} else if notNull {
		if err := s.rebuildEmailNotifications(); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email IS NOT NULL AND email <> ''`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_issues_customer_token ON issues(customer_token) WHERE customer_token IS NOT NULL AND customer_token <> '';
		CREATE INDEX IF NOT EXISTS idx_issues_requester_email ON issues(requester_email);
		CREATE INDEX IF NOT EXISTS idx_comments_visibility ON comments(issue_id, visibility);
	`)
	return err
}

func (s *Store) columnExists(table, column string) (bool, error) {
	switch table {
	case "users", "issues", "comments", "project_members":
	default:
		return false, fmt.Errorf("unsupported table %q", table)
	}
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) columnNotNull(table, column string) (bool, error) {
	switch table {
	case "email_notifications":
	default:
		return false, fmt.Errorf("unsupported table %q", table)
	}
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return notNull != 0, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) usersRoleAllowsClient() (bool, error) {
	var sqlText string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&sqlText); err != nil {
		return false, err
	}
	return !strings.Contains(sqlText, "CHECK") || strings.Contains(sqlText, "'client'") || strings.Contains(sqlText, `"client"`), nil
}

func (s *Store) projectRolesAreTicketing() (bool, error) {
	var sqlText string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'project_members'`).Scan(&sqlText); err != nil {
		return false, err
	}
	return !strings.Contains(sqlText, "CHECK") || strings.Contains(sqlText, "'agent'") || strings.Contains(sqlText, `"agent"`), nil
}

func (s *Store) rebuildUsersRoleConstraint() error {
	if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer s.db.Exec(`PRAGMA foreign_keys = ON`)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		CREATE TABLE users_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			email TEXT,
			role TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO users_new (id, username, display_name, email, role, password_hash, disabled, created_at, updated_at)
		SELECT id, username, display_name, email, role, password_hash, disabled, created_at, updated_at
		FROM users`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE users`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE users_new RENAME TO users`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) rebuildProjectMembersRoleConstraint() error {
	if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer s.db.Exec(`PRAGMA foreign_keys = ON`)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		CREATE TABLE project_members_new (
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (project_id, user_id)
		)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO project_members_new (project_id, user_id, role, created_at)
		SELECT project_id, user_id,
		       CASE role WHEN 'developer' THEN 'agent' WHEN 'reporter' THEN 'customer' ELSE role END,
		       created_at
		FROM project_members`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE project_members`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE project_members_new RENAME TO project_members`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_project_members_user ON project_members(user_id)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) rebuildEmailNotifications() error {
	_, err := s.db.Exec(`
		ALTER TABLE email_notifications RENAME TO email_notifications_old;
		CREATE TABLE email_notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
			issue_id INTEGER REFERENCES issues(id) ON DELETE CASCADE,
			user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			recipient_email TEXT NOT NULL,
			recipient_name TEXT NOT NULL,
			event TEXT NOT NULL,
			subject TEXT NOT NULL,
			body_text TEXT NOT NULL,
			body_html TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL CHECK (status IN ('pending', 'sending', 'sent', 'failed')) DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT NOT NULL,
			locked_until TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			sent_at TEXT
		);
		INSERT INTO email_notifications (
			id, project_id, issue_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
			status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		)
		SELECT id, project_id, issue_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
		       status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		FROM email_notifications_old;
		DROP TABLE email_notifications_old;
	`)
	return err
}

func (s *Store) SetupRequired() bool {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return true
	}
	return count == 0
}

func (s *Store) CreateFirstAdmin(input CreateUser) (User, error) {
	input.Role = "admin"
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return User{}, err
	}
	if count > 0 {
		return User{}, fmt.Errorf("%w: setup is already complete", ErrConflict)
	}

	user, err := createUserTx(tx, input)
	if err != nil {
		return User{}, err
	}
	project, err := createProjectTx(tx, CreateProject{Key: "PME", Name: "Inbox"})
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(
		`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		project.ID, user.ID, formatTime(time.Now().UTC()),
	); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) CreateUser(input CreateUser) (User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	user, err := createUserTx(tx, input)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) ListUsers() []User {
	rows, err := s.db.Query(`SELECT id, username, display_name, email, role, disabled, created_at, updated_at FROM users ORDER BY username`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err == nil {
			users = append(users, user)
		}
	}
	return users
}

func (s *Store) UpdateUser(id int64, patch UpdateUser) (User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	user, err := getUserTx(tx, id)
	if err != nil {
		return User{}, err
	}
	oldRole := user.Role
	oldDisabled := user.Disabled

	if patch.DisplayName != nil {
		user.DisplayName = defaultString(*patch.DisplayName, user.Username)
	}
	if patch.Email != nil {
		email, err := normalizeEmail(*patch.Email)
		if err != nil {
			return User{}, err
		}
		user.Email = email
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
		if !isValid(validGlobalRoles, role) {
			return User{}, fmt.Errorf("%w: invalid role %q", ErrValidation, role)
		}
		user.Role = role
	}
	if patch.Disabled != nil {
		user.Disabled = *patch.Disabled
	}
	user.UpdatedAt = time.Now().UTC()

	if _, err := tx.Exec(
		`UPDATE users SET display_name = ?, email = ?, role = ?, password_hash = ?, disabled = ?, updated_at = ? WHERE id = ?`,
		user.DisplayName, nullEmptyString(user.Email), user.Role, user.PasswordHash, boolInt(user.Disabled), formatTime(user.UpdatedAt), user.ID,
	); err != nil {
		return User{}, normalizeSQLError(err)
	}
	if (oldRole == "admin" || !oldDisabled) && !hasActiveAdminTx(tx) {
		return User{}, fmt.Errorf("%w: at least one active admin is required", ErrValidation)
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) DeleteUser(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	user, err := getUserTx(tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	if user.Role == "admin" && !hasActiveAdminTx(tx) {
		return fmt.Errorf("%w: at least one active admin is required", ErrValidation)
	}
	return tx.Commit()
}

func (s *Store) Authenticate(username, password string) (User, error) {
	username = normalizeUsername(username)
	user, err := s.userByUsername(username, true)
	if err != nil {
		return User{}, fmt.Errorf("%w: invalid username or password", ErrValidation)
	}
	if user.Disabled {
		return User{}, fmt.Errorf("%w: user is disabled", ErrValidation)
	}
	if !security.VerifyPassword(user.PasswordHash, password) {
		return User{}, fmt.Errorf("%w: invalid username or password", ErrValidation)
	}
	return publicUserCopy(user), nil
}

func (s *Store) CreateSession(userID int64) (string, string, time.Time, error) {
	token, err := security.RandomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	csrf, err := security.RandomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(14 * 24 * time.Hour)

	user, err := s.GetUser(userID)
	if err != nil || user.Disabled {
		return "", "", time.Time{}, ErrNotFound
	}
	_, err = s.db.Exec(
		`INSERT INTO sessions (token_hash, csrf_token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		security.HashToken(token), csrf, userID, formatTime(now), formatTime(expires),
	)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return token, csrf, expires, nil
}

func (s *Store) UserBySession(token string) (User, string, bool) {
	hash := security.HashToken(token)
	now := formatTime(time.Now().UTC())
	row := s.db.QueryRow(`
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.disabled, u.created_at, u.updated_at, s.csrf_token
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		hash, now,
	)
	var user User
	var disabled int
	var email sql.NullString
	var created, updated, csrf string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &created, &updated, &csrf); err != nil {
		return User{}, "", false
	}
	user.Email = nullString(email)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	if user.Disabled {
		return User{}, "", false
	}
	return publicUserCopy(user), csrf, true
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, security.HashToken(token))
	return err
}

func (s *Store) CreateAPIToken(userID int64, input CreateAPIToken) (PublicAPIToken, string, error) {
	name := defaultString(input.Name, "API token")
	raw, err := security.RandomToken()
	if err != nil {
		return PublicAPIToken{}, "", err
	}
	token := "pme_" + raw
	now := time.Now().UTC()

	user, err := s.GetUser(userID)
	if err != nil || user.Disabled {
		return PublicAPIToken{}, "", ErrNotFound
	}
	result, err := s.db.Exec(
		`INSERT INTO api_tokens (user_id, name, prefix, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, name, token[:12], security.HashToken(token), formatTime(now),
	)
	if err != nil {
		return PublicAPIToken{}, "", err
	}
	id, _ := result.LastInsertId()
	apiToken := APIToken{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Prefix:    token[:12],
		TokenHash: security.HashToken(token),
		CreatedAt: now,
	}
	return publicAPIToken(apiToken), token, nil
}

func (s *Store) ListAPITokens(userID int64) []PublicAPIToken {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, prefix, created_at, last_used_at FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var tokens []PublicAPIToken
	for rows.Next() {
		var token PublicAPIToken
		var created string
		var last sql.NullString
		if err := rows.Scan(&token.ID, &token.UserID, &token.Name, &token.Prefix, &created, &last); err == nil {
			token.CreatedAt = parseTime(created)
			token.LastUsedAt = parseNullTime(last)
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func (s *Store) DeleteAPIToken(userID, tokenID int64) error {
	result, err := s.db.Exec(`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, tokenID, userID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UserByAPIToken(token string) (User, bool) {
	hash := security.HashToken(token)
	row := s.db.QueryRow(`
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.disabled, u.created_at, u.updated_at, t.id, t.last_used_at
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`,
		hash,
	)
	var user User
	var tokenID int64
	var disabled int
	var email sql.NullString
	var created, updated string
	var last sql.NullString
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &created, &updated, &tokenID, &last); err != nil {
		return User{}, false
	}
	user.Email = nullString(email)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	if user.Disabled {
		return User{}, false
	}
	now := time.Now().UTC()
	lastUsed := parseNullTime(last)
	if lastUsed == nil || now.Sub(*lastUsed) > time.Hour {
		_, _ = s.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, formatTime(now), tokenID)
	}
	return publicUserCopy(user), true
}

func (s *Store) GetUser(id int64) (User, error) {
	row := s.db.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, created_at, updated_at FROM users WHERE id = ?`, id)
	var user User
	var disabled int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	return user, nil
}

func (s *Store) CreateProject(input CreateProject) (Project, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback()
	project, err := createProjectTx(tx, input)
	if err != nil {
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Store) ListProjects(user User) []Project {
	var rows *sql.Rows
	var err error
	if user.Role == "admin" {
		rows, err = s.db.Query(`
			SELECT id, key, name, description, 'owner', created_at, updated_at
			FROM projects
			ORDER BY key`)
	} else {
		rows, err = s.db.Query(`
			SELECT p.id, p.key, p.name, p.description, pm.role, p.created_at, p.updated_at
			FROM projects p
			JOIN project_members pm ON pm.project_id = p.id
			WHERE pm.user_id = ?
			ORDER BY p.key`,
			user.ID,
		)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		project, err := scanProject(rows)
		if err == nil {
			projects = append(projects, project)
		}
	}
	return projects
}

func (s *Store) GetProject(id int64) (Project, error) {
	row := s.db.QueryRow(`
		SELECT id, key, name, description, '', created_at, updated_at
		FROM projects
		WHERE id = ?`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

func (s *Store) UpdateProject(id int64, patch UpdateProject) (Project, error) {
	project, err := s.GetProject(id)
	if err != nil {
		return Project{}, err
	}
	if patch.Name != nil {
		project.Name = strings.TrimSpace(*patch.Name)
		if project.Name == "" {
			return Project{}, fmt.Errorf("%w: project name is required", ErrValidation)
		}
	}
	if patch.Description != nil {
		project.Description = strings.TrimSpace(*patch.Description)
	}
	project.UpdatedAt = time.Now().UTC()
	_, err = s.db.Exec(
		`UPDATE projects SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		project.Name, project.Description, formatTime(project.UpdatedAt), id,
	)
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(id)
}

func (s *Store) DeleteProject(id int64) error {
	result, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ProjectRole(userID, projectID int64) (string, bool) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM project_members WHERE user_id = ? AND project_id = ?`, userID, projectID).Scan(&role)
	return normalizeProjectRole(role), err == nil
}

func (s *Store) ListProjectMembers(projectID int64) []ProjectMember {
	rows, err := s.db.Query(`
		SELECT pm.project_id, u.id, u.username, u.display_name, pm.role, pm.created_at
		FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = ?
		ORDER BY pm.role, u.username`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var members []ProjectMember
	for rows.Next() {
		var member ProjectMember
		var created string
		if err := rows.Scan(&member.ProjectID, &member.UserID, &member.Username, &member.DisplayName, &member.Role, &created); err == nil {
			member.Role = normalizeProjectRole(member.Role)
			member.CreatedAt = parseTime(created)
			members = append(members, member)
		}
	}
	return members
}

func (s *Store) UpsertProjectMember(projectID int64, input UpsertProjectMember) (ProjectMember, error) {
	role := normalizeProjectRole(input.Role)
	if !isValid(validProjectRoles, role) {
		return ProjectMember{}, fmt.Errorf("%w: invalid project role %q", ErrValidation, role)
	}
	if _, err := s.GetProject(projectID); err != nil {
		return ProjectMember{}, err
	}
	user, err := s.GetUser(input.UserID)
	if err != nil {
		return ProjectMember{}, err
	}
	if user.Disabled {
		return ProjectMember{}, fmt.Errorf("%w: user is disabled", ErrValidation)
	}
	now := time.Now().UTC()
	_, err = s.db.Exec(`
		INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(project_id, user_id) DO UPDATE SET role = excluded.role`,
		projectID, input.UserID, role, formatTime(now),
	)
	if err != nil {
		return ProjectMember{}, err
	}
	for _, member := range s.ListProjectMembers(projectID) {
		if member.UserID == input.UserID {
			return member, nil
		}
	}
	return ProjectMember{}, ErrNotFound
}

func (s *Store) DeleteProjectMember(projectID, userID int64) error {
	result, err := s.db.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateIssue(input CreateIssue) (Issue, error) {
	now := time.Now().UTC()
	source := defaultString(input.Source, "staff")
	if !isValid(validIssueSources, source) {
		return Issue{}, fmt.Errorf("%w: invalid issue source %q", ErrValidation, source)
	}
	requesterEmail, err := normalizeEmail(input.RequesterEmail)
	if err != nil {
		return Issue{}, err
	}
	issue := Issue{
		ProjectID:      input.ProjectID,
		Title:          strings.TrimSpace(input.Title),
		Description:    strings.TrimSpace(input.Description),
		Status:         "new",
		Severity:       defaultString(input.Severity, "support"),
		Priority:       defaultString(input.Priority, "normal"),
		Assignee:       strings.TrimSpace(input.Assignee),
		Reporter:       strings.TrimSpace(input.Reporter),
		Source:         source,
		RequesterName:  strings.TrimSpace(input.RequesterName),
		RequesterEmail: requesterEmail,
		Tags:           normalizeTags(input.Tags),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if issue.Source == "portal" {
		if issue.RequesterEmail == "" {
			return Issue{}, fmt.Errorf("%w: requester email is required", ErrValidation)
		}
		if issue.RequesterName == "" {
			issue.RequesterName = issue.RequesterEmail
		}
		if issue.Reporter == "" {
			issue.Reporter = issue.RequesterEmail
		}
	}
	if issue.RequesterEmail != "" {
		token, err := security.RandomToken()
		if err != nil {
			return Issue{}, err
		}
		issue.CustomerToken = token
	}
	if issue.ProjectID < 1 {
		return Issue{}, fmt.Errorf("%w: project_id is required", ErrValidation)
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

	tx, err := s.db.Begin()
	if err != nil {
		return Issue{}, err
	}
	defer tx.Rollback()
	if _, err := getProjectTx(tx, issue.ProjectID); err != nil {
		return Issue{}, err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(number), 0) + 1 FROM issues WHERE project_id = ?`, issue.ProjectID).Scan(&issue.Number); err != nil {
		return Issue{}, err
	}
	result, err := tx.Exec(`
		INSERT INTO issues (
			project_id, number, title, description, status, severity, priority, assignee, reporter,
			source, requester_name, requester_email, customer_token, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ProjectID, issue.Number, issue.Title, issue.Description, issue.Status, issue.Severity, issue.Priority,
		issue.Assignee, issue.Reporter, issue.Source, issue.RequesterName, issue.RequesterEmail,
		nullEmptyString(issue.CustomerToken), formatTime(issue.CreatedAt), formatTime(issue.UpdatedAt),
	)
	if err != nil {
		return Issue{}, err
	}
	issue.ID, _ = result.LastInsertId()
	if err := replaceTagsTx(tx, issue.ID, issue.Tags); err != nil {
		return Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return Issue{}, err
	}
	return s.GetIssue(issue.ID)
}

func (s *Store) ListIssues(filter Filter) []Issue {
	return s.listIssues(filter, User{Role: "admin"})
}

func (s *Store) ListIssuesForUser(filter Filter, user User) []Issue {
	return s.listIssues(filter, user)
}

func (s *Store) listIssues(filter Filter, user User) []Issue {
	filter.Query = strings.ToLower(strings.TrimSpace(filter.Query))
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Assignee = strings.TrimSpace(filter.Assignee)

	conditions := []string{"1 = 1"}
	args := []any{}
	if user.Role != "admin" {
		conditions = append(conditions, `EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = i.project_id AND pm.user_id = ?)`)
		args = append(args, user.ID)
	}
	if filter.ProjectID > 0 {
		conditions = append(conditions, "i.project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.Status != "" {
		conditions = append(conditions, "i.status = ?")
		args = append(args, filter.Status)
	}
	if filter.Assignee != "" {
		conditions = append(conditions, "i.assignee = ?")
		args = append(args, filter.Assignee)
	}
	if filter.Query != "" {
		conditions = append(conditions, `(
			lower(i.title) LIKE ? OR lower(i.description) LIKE ? OR lower(p.key) LIKE ? OR lower(p.name) LIKE ? OR
			lower(i.assignee) LIKE ? OR lower(i.reporter) LIKE ? OR lower(i.requester_name) LIKE ? OR lower(i.requester_email) LIKE ? OR
			EXISTS (SELECT 1 FROM issue_tags it WHERE it.issue_id = i.id AND lower(it.tag) LIKE ?)
		)`)
		q := "%" + filter.Query + "%"
		args = append(args, q, q, q, q, q, q, q, q, q)
	}

	query := `
		SELECT i.id, i.project_id, p.key, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, i.requester_name, i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY i.updated_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var issues []Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err == nil {
			issues = append(issues, issue)
		}
	}
	rows.Close()
	for i := range issues {
		_ = s.hydrateIssue(&issues[i])
	}
	return issues
}

func (s *Store) GetIssue(id int64) (Issue, error) {
	row := s.db.QueryRow(`
		SELECT i.id, i.project_id, p.key, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, i.requester_name, i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		WHERE i.id = ?`, id)
	issue, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	return issue, s.hydrateIssue(&issue)
}

func (s *Store) GetIssueByCustomerToken(token string) (Issue, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Issue{}, ErrNotFound
	}
	row := s.db.QueryRow(`
		SELECT i.id, i.project_id, p.key, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, i.requester_name, i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		WHERE i.customer_token = ?`, token)
	issue, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	if err := s.hydrateIssue(&issue); err != nil {
		return Issue{}, err
	}
	issue.Comments = publicComments(issue.Comments)
	return issue, nil
}

func (s *Store) UpdateIssue(id int64, patch UpdateIssue) (Issue, error) {
	current, err := s.GetIssue(id)
	if err != nil {
		return Issue{}, err
	}
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if title == "" {
			return Issue{}, fmt.Errorf("%w: title is required", ErrValidation)
		}
		current.Title = title
	}
	if patch.Description != nil {
		current.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.Status != nil {
		status := strings.TrimSpace(*patch.Status)
		if !isValid(validStatuses, status) {
			return Issue{}, fmt.Errorf("%w: invalid status %q", ErrValidation, status)
		}
		current.Status = status
		if status == "closed" || status == "resolved" {
			now := time.Now().UTC()
			current.ClosedAt = &now
		} else {
			current.ClosedAt = nil
		}
	}
	if patch.Severity != nil {
		severity := defaultString(*patch.Severity, "support")
		if !isValid(validSeverities, severity) {
			return Issue{}, fmt.Errorf("%w: invalid severity %q", ErrValidation, severity)
		}
		current.Severity = severity
	}
	if patch.Priority != nil {
		priority := defaultString(*patch.Priority, "normal")
		if !isValid(validPriorities, priority) {
			return Issue{}, fmt.Errorf("%w: invalid priority %q", ErrValidation, priority)
		}
		current.Priority = priority
	}
	if patch.Assignee != nil {
		current.Assignee = strings.TrimSpace(*patch.Assignee)
	}
	if patch.Tags != nil {
		current.Tags = normalizeTags(*patch.Tags)
	}
	current.UpdatedAt = time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return Issue{}, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
		UPDATE issues
		SET title = ?, description = ?, status = ?, severity = ?, priority = ?, assignee = ?, updated_at = ?, closed_at = ?
		WHERE id = ?`,
		current.Title, current.Description, current.Status, current.Severity, current.Priority, current.Assignee,
		formatTime(current.UpdatedAt), formatTimePtr(current.ClosedAt), current.ID,
	)
	if err != nil {
		return Issue{}, err
	}
	if patch.Tags != nil {
		if err := replaceTagsTx(tx, current.ID, current.Tags); err != nil {
			return Issue{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Issue{}, err
	}
	return s.GetIssue(id)
}

func (s *Store) AddComment(id int64, input AddComment) (Issue, error) {
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return Issue{}, fmt.Errorf("%w: comment body is required", ErrValidation)
	}
	author := defaultString(input.Author, "anonymous")
	visibility := defaultString(input.Visibility, "public")
	if !isValid(validCommentVisibility, visibility) {
		return Issue{}, fmt.Errorf("%w: invalid comment visibility %q", ErrValidation, visibility)
	}
	now := time.Now().UTC()

	result, err := s.db.Exec(
		`INSERT INTO comments (issue_id, author, body, visibility, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, author, body, visibility, formatTime(now),
	)
	if err != nil {
		return Issue{}, normalizeSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Issue{}, ErrNotFound
	}
	_, _ = s.db.Exec(`UPDATE issues SET updated_at = ? WHERE id = ?`, formatTime(now), id)
	return s.GetIssue(id)
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
	if input.ProjectID != nil {
		if _, err := s.GetProject(*input.ProjectID); err != nil {
			return Webhook{}, err
		}
	}
	hook := Webhook{
		ProjectID: input.ProjectID,
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
	eventsJSON, _ := json.Marshal(hook.Events)
	result, err := s.db.Exec(`
		INSERT INTO webhooks (project_id, name, url, secret, events_json, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableInt64(hook.ProjectID), hook.Name, hook.URL, hook.Secret, string(eventsJSON), boolInt(hook.Enabled),
		formatTime(hook.CreatedAt), formatTime(hook.UpdatedAt),
	)
	if err != nil {
		return Webhook{}, err
	}
	hook.ID, _ = result.LastInsertId()
	return copyWebhook(hook), nil
}

func (s *Store) ListWebhooks(projectID *int64) []Webhook {
	query := `
		SELECT id, project_id, name, url, secret, events_json, enabled, created_at, updated_at, last_status, last_error, last_delivered_at
		FROM webhooks`
	args := []any{}
	if projectID == nil {
		query += ` WHERE project_id IS NULL`
	} else {
		query += ` WHERE project_id = ?`
		args = append(args, *projectID)
	}
	query += ` ORDER BY id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanWebhooks(rows)
}

func (s *Store) GetWebhook(id int64) (Webhook, error) {
	row := s.db.QueryRow(`
		SELECT id, project_id, name, url, secret, events_json, enabled, created_at, updated_at, last_status, last_error, last_delivered_at
		FROM webhooks
		WHERE id = ?`, id)
	hook, err := scanWebhook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Webhook{}, ErrNotFound
	}
	return hook, err
}

func (s *Store) ListWebhooksForEvent(event string, projectID int64) []Webhook {
	rows, err := s.db.Query(`
		SELECT id, project_id, name, url, secret, events_json, enabled, created_at, updated_at, last_status, last_error, last_delivered_at
		FROM webhooks
		WHERE enabled = 1 AND (project_id IS NULL OR project_id = ?)
		ORDER BY project_id IS NOT NULL DESC, id`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	hooks := make([]Webhook, 0)
	for _, hook := range scanWebhooks(rows) {
		if eventMatches(hook.Events, event) {
			hooks = append(hooks, hook)
		}
	}
	return hooks
}

func (s *Store) UpdateWebhook(id int64, patch UpdateWebhook) (Webhook, error) {
	hook, err := s.GetWebhook(id)
	if err != nil {
		return Webhook{}, err
	}
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
	eventsJSON, _ := json.Marshal(hook.Events)
	_, err = s.db.Exec(`
		UPDATE webhooks
		SET name = ?, url = ?, secret = ?, events_json = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		hook.Name, hook.URL, hook.Secret, string(eventsJSON), boolInt(hook.Enabled), formatTime(hook.UpdatedAt), hook.ID,
	)
	if err != nil {
		return Webhook{}, err
	}
	return s.GetWebhook(id)
}

func (s *Store) DeleteWebhook(id int64) error {
	result, err := s.db.Exec(`DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RecordDelivery(delivery WebhookDelivery) error {
	now := time.Now().UTC()
	result, err := s.db.Exec(`
		INSERT INTO webhook_deliveries (webhook_id, project_id, event, issue_id, status_code, error, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		delivery.WebhookID, nullableInt64(delivery.ProjectID), delivery.Event, nullZero(delivery.IssueID),
		nullZero(int64(delivery.StatusCode)), delivery.Error, delivery.DurationMS, formatTime(now),
	)
	if err != nil {
		return err
	}
	delivery.ID, _ = result.LastInsertId()
	delivery.CreatedAt = now
	_, _ = s.db.Exec(`
		UPDATE webhooks
		SET last_status = ?, last_error = ?, last_delivered_at = ?
		WHERE id = ?`,
		delivery.StatusCode, delivery.Error, formatTime(now), delivery.WebhookID,
	)
	_, _ = s.db.Exec(`
		DELETE FROM webhook_deliveries
		WHERE id NOT IN (SELECT id FROM webhook_deliveries ORDER BY created_at DESC LIMIT 200)`)
	return nil
}

func (s *Store) ListDeliveries(limit int) []WebhookDelivery {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, webhook_id, project_id, event, issue_id, status_code, error, duration_ms, created_at
		FROM webhook_deliveries
		ORDER BY created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var deliveries []WebhookDelivery
	for rows.Next() {
		var delivery WebhookDelivery
		var projectID, issueID, status sql.NullInt64
		var created string
		if err := rows.Scan(&delivery.ID, &delivery.WebhookID, &projectID, &delivery.Event, &issueID, &status, &delivery.Error, &delivery.DurationMS, &created); err == nil {
			if projectID.Valid {
				v := projectID.Int64
				delivery.ProjectID = &v
			}
			if issueID.Valid {
				delivery.IssueID = issueID.Int64
			}
			if status.Valid {
				delivery.StatusCode = int(status.Int64)
			}
			delivery.CreatedAt = parseTime(created)
			deliveries = append(deliveries, delivery)
		}
	}
	return deliveries
}

func (s *Store) IssueEmailRecipients(event string, issue Issue, actor User) []EmailRecipient {
	recipients := make(map[int64]EmailRecipient)
	add := func(recipient EmailRecipient) {
		if recipient.UserID == 0 || recipient.UserID == actor.ID || strings.TrimSpace(recipient.Email) == "" {
			return
		}
		recipients[recipient.UserID] = recipient
	}

	switch event {
	case "ticket.created":
		for _, recipient := range s.projectOwnerEmailRecipients(issue.ProjectID) {
			add(recipient)
		}
	case "ticket.updated", "ticket.commented":
		if recipient, ok := s.emailRecipientByUsername(issue.Reporter); ok {
			add(recipient)
		}
		if recipient, ok := s.emailRecipientByUsername(issue.Assignee); ok {
			add(recipient)
		}
	case "ticket.assigned":
		if recipient, ok := s.emailRecipientByUsername(issue.Assignee); ok {
			add(recipient)
		}
	}

	result := make([]EmailRecipient, 0, len(recipients))
	for _, recipient := range recipients {
		result = append(result, recipient)
	}
	return result
}

func (s *Store) EnqueueEmailNotifications(inputs []CreateEmailNotification) ([]EmailNotification, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	created := make([]EmailNotification, 0, len(inputs))
	for _, input := range inputs {
		email, err := normalizeEmail(input.RecipientEmail)
		if err != nil {
			return nil, err
		}
		subject := strings.TrimSpace(input.Subject)
		bodyText := strings.TrimSpace(input.BodyText)
		if email == "" || subject == "" || bodyText == "" {
			return nil, fmt.Errorf("%w: email, subject, and body are required", ErrValidation)
		}
		notification := EmailNotification{
			ProjectID:      input.ProjectID,
			IssueID:        input.IssueID,
			UserID:         input.UserID,
			RecipientEmail: email,
			RecipientName:  strings.TrimSpace(input.RecipientName),
			Event:          strings.TrimSpace(input.Event),
			Subject:        subject,
			BodyText:       bodyText,
			BodyHTML:       strings.TrimSpace(input.BodyHTML),
			Status:         "pending",
			NextAttemptAt:  now,
			CreatedAt:      now,
		}
		if notification.RecipientName == "" {
			notification.RecipientName = email
		}
		if notification.Event == "" {
			return nil, fmt.Errorf("%w: event is required", ErrValidation)
		}
		if !isValid(validEvents, notification.Event) {
			return nil, fmt.Errorf("%w: invalid notification event %q", ErrValidation, notification.Event)
		}
		result, err := tx.Exec(`
			INSERT INTO email_notifications (
				project_id, issue_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
				status, attempts, next_attempt_at, created_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
			nullZero(notification.ProjectID), nullZero(notification.IssueID), nullZero(notification.UserID), notification.RecipientEmail,
			notification.RecipientName, notification.Event, notification.Subject, notification.BodyText, notification.BodyHTML,
			formatTime(notification.NextAttemptAt), formatTime(notification.CreatedAt),
		)
		if err != nil {
			return nil, normalizeSQLError(err)
		}
		notification.ID, _ = result.LastInsertId()
		created = append(created, notification)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Store) ClaimEmailNotifications(limit int, leaseFor time.Duration) ([]EmailNotification, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	if leaseFor <= 0 {
		leaseFor = time.Minute
	}
	now := time.Now().UTC()
	nowText := formatTime(now)
	lockedUntil := formatTime(now.Add(leaseFor))

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT id
		FROM email_notifications
		WHERE (status = 'pending' AND next_attempt_at <= ?)
		   OR (status = 'sending' AND locked_until IS NOT NULL AND locked_until <= ?)
		ORDER BY created_at
		LIMIT ?`, nowText, nowText, limit)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	claimed := make([]EmailNotification, 0, len(ids))
	for _, id := range ids {
		result, err := tx.Exec(`
			UPDATE email_notifications
			SET status = 'sending', locked_until = ?
			WHERE id = ?
			  AND ((status = 'pending' AND next_attempt_at <= ?)
			    OR (status = 'sending' AND locked_until IS NOT NULL AND locked_until <= ?))`,
			lockedUntil, id, nowText, nowText,
		)
		if err != nil {
			return nil, err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			continue
		}
		notification, err := getEmailNotificationTx(tx, id)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, notification)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) MarkEmailSent(id int64) error {
	now := time.Now().UTC()
	result, err := s.db.Exec(`
		UPDATE email_notifications
		SET status = 'sent', sent_at = ?, locked_until = NULL, last_error = ''
		WHERE id = ?`,
		formatTime(now), id,
	)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkEmailFailed(id int64, sendErr error, maxAttempts int) error {
	if maxAttempts < 1 {
		maxAttempts = 5
	}
	notification, err := s.GetEmailNotification(id)
	if err != nil {
		return err
	}
	attempts := notification.Attempts + 1
	status := "pending"
	delay := emailRetryDelay(attempts)
	nextAttempt := time.Now().UTC().Add(delay)
	if attempts >= maxAttempts {
		status = "failed"
		nextAttempt = time.Now().UTC()
	}
	message := "send failed"
	if sendErr != nil {
		message = sendErr.Error()
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	result, err := s.db.Exec(`
		UPDATE email_notifications
		SET status = ?, attempts = ?, next_attempt_at = ?, locked_until = NULL, last_error = ?
		WHERE id = ?`,
		status, attempts, formatTime(nextAttempt), message, id,
	)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetEmailNotification(id int64) (EmailNotification, error) {
	row := s.db.QueryRow(`
		SELECT id, project_id, issue_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
		       status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		FROM email_notifications
		WHERE id = ?`, id)
	notification, err := scanEmailNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailNotification{}, ErrNotFound
	}
	return notification, err
}

func (s *Store) ListEmailNotifications(limit int) []EmailNotification {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, project_id, issue_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
		       status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		FROM email_notifications
		ORDER BY created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	notifications := make([]EmailNotification, 0)
	for rows.Next() {
		notification, err := scanEmailNotification(rows)
		if err == nil {
			notifications = append(notifications, notification)
		}
	}
	return notifications
}

func (s *Store) IssueIDByProjectNumber(projectID, number int64) (int64, bool) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM issues WHERE project_id = ? AND number = ?`, projectID, number).Scan(&id)
	return id, err == nil
}

func Statuses() []string {
	return []string{"new", "open", "pending", "assigned", "resolved", "closed"}
}

func Priorities() []string {
	return []string{"low", "normal", "high", "urgent"}
}

func Roles() []string {
	return []string{"admin", "user", "client"}
}

func ProjectRoles() []string {
	return []string{"owner", "agent", "customer", "viewer"}
}

func Events() []string {
	return []string{"ticket.created", "ticket.updated", "ticket.commented", "ticket.assigned"}
}

func ToPublicUser(user User) PublicUser {
	return PublicUser{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Role:        user.Role,
		Disabled:    user.Disabled,
		CreatedAt:   user.CreatedAt,
		UpdatedAt:   user.UpdatedAt,
	}
}

func ToPublicWebhook(hook Webhook) PublicWebhook {
	return PublicWebhook{
		ID:              hook.ID,
		ProjectID:       hook.ProjectID,
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

func createUserTx(tx *sql.Tx, input CreateUser) (User, error) {
	username := normalizeUsername(input.Username)
	if !usernamePattern.MatchString(username) {
		return User{}, fmt.Errorf("%w: username must be 3-48 lowercase letters, numbers, dot, dash, or underscore", ErrValidation)
	}
	role := defaultString(input.Role, "user")
	if !isValid(validGlobalRoles, role) {
		return User{}, fmt.Errorf("%w: invalid role %q", ErrValidation, role)
	}
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return User{}, err
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	now := time.Now().UTC()
	displayName := defaultString(input.DisplayName, username)
	result, err := tx.Exec(
		`INSERT INTO users (username, display_name, email, role, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		username, displayName, nullEmptyString(email), role, hash, formatTime(now), formatTime(now),
	)
	if err != nil {
		return User{}, normalizeSQLError(err)
	}
	id, _ := result.LastInsertId()
	return User{
		ID:           id,
		Username:     username,
		DisplayName:  displayName,
		Email:        email,
		Role:         role,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func createProjectTx(tx *sql.Tx, input CreateProject) (Project, error) {
	key := strings.ToUpper(strings.TrimSpace(input.Key))
	if !projectKeyPattern.MatchString(key) {
		return Project{}, fmt.Errorf("%w: project key must be 2-16 uppercase letters or numbers", ErrValidation)
	}
	name := defaultString(input.Name, key)
	description := strings.TrimSpace(input.Description)
	now := time.Now().UTC()
	result, err := tx.Exec(
		`INSERT INTO projects (key, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		key, name, description, formatTime(now), formatTime(now),
	)
	if err != nil {
		return Project{}, normalizeSQLError(err)
	}
	id, _ := result.LastInsertId()
	return Project{
		ID:          id,
		Key:         key,
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func getUserTx(tx *sql.Tx, id int64) (User, error) {
	row := tx.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, created_at, updated_at FROM users WHERE id = ?`, id)
	var user User
	var disabled int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	return user, nil
}

func getProjectTx(tx *sql.Tx, id int64) (Project, error) {
	row := tx.QueryRow(`
		SELECT id, key, name, description, '', created_at, updated_at
		FROM projects WHERE id = ?`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

func hasActiveAdminTx(tx *sql.Tx) bool {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func replaceTagsTx(tx *sql.Tx, issueID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM issue_tags WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := tx.Exec(`INSERT INTO issue_tags (issue_id, tag) VALUES (?, ?)`, issueID, tag); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) userByUsername(username string, includeHash bool) (User, error) {
	row := s.db.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, created_at, updated_at FROM users WHERE username = ?`, username)
	var user User
	var disabled int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	if !includeHash {
		user.PasswordHash = ""
	}
	return user, nil
}

func (s *Store) projectOwnerEmailRecipients(projectID int64) []EmailRecipient {
	rows, err := s.db.Query(`
		SELECT DISTINCT u.id, u.username, u.display_name, u.email, u.role
		FROM users u
		LEFT JOIN project_members pm ON pm.user_id = u.id AND pm.project_id = ?
		WHERE u.disabled = 0
		  AND u.email IS NOT NULL
		  AND trim(u.email) <> ''
		  AND (u.role = 'admin' OR pm.role = 'owner')
		ORDER BY u.username`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	recipients := make([]EmailRecipient, 0)
	for rows.Next() {
		recipient, err := scanEmailRecipient(rows)
		if err == nil {
			recipients = append(recipients, recipient)
		}
	}
	return recipients
}

func (s *Store) emailRecipientByUsername(username string) (EmailRecipient, bool) {
	username = normalizeUsername(username)
	if username == "" {
		return EmailRecipient{}, false
	}
	row := s.db.QueryRow(`
		SELECT id, username, display_name, email, role
		FROM users
		WHERE username = ?
		  AND disabled = 0
		  AND email IS NOT NULL
		  AND trim(email) <> ''`, username)
	recipient, err := scanEmailRecipient(row)
	return recipient, err == nil
}

func (s *Store) hydrateIssue(issue *Issue) error {
	issue.Key = fmt.Sprintf("%s-%d", issue.ProjectKey, issue.Number)
	issue.Project = issue.ProjectKey

	tagRows, err := s.db.Query(`SELECT tag FROM issue_tags WHERE issue_id = ? ORDER BY tag COLLATE NOCASE`, issue.ID)
	if err != nil {
		return err
	}
	for tagRows.Next() {
		var tag string
		if err := tagRows.Scan(&tag); err == nil {
			issue.Tags = append(issue.Tags, tag)
		}
	}
	tagRows.Close()

	commentRows, err := s.db.Query(`SELECT id, author, body, visibility, created_at FROM comments WHERE issue_id = ? ORDER BY created_at`, issue.ID)
	if err != nil {
		return err
	}
	for commentRows.Next() {
		var comment Comment
		var created string
		if err := commentRows.Scan(&comment.ID, &comment.Author, &comment.Body, &comment.Visibility, &created); err == nil {
			if comment.Visibility == "" {
				comment.Visibility = "public"
			}
			comment.CreatedAt = parseTime(created)
			issue.Comments = append(issue.Comments, comment)
		}
	}
	commentRows.Close()

	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(rows scanner) (User, error) {
	var user User
	var disabled int
	var email sql.NullString
	var created, updated string
	if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &created, &updated); err != nil {
		return User{}, err
	}
	user.Email = nullString(email)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	return user, nil
}

func scanProject(rows scanner) (Project, error) {
	var project Project
	var created, updated string
	if err := rows.Scan(
		&project.ID, &project.Key, &project.Name, &project.Description, &project.Role,
		&created, &updated,
	); err != nil {
		return Project{}, err
	}
	project.Role = normalizeProjectRole(project.Role)
	project.CreatedAt = parseTime(created)
	project.UpdatedAt = parseTime(updated)
	return project, nil
}

func scanIssue(rows scanner) (Issue, error) {
	var issue Issue
	var closed, customerToken sql.NullString
	var created, updated string
	if err := rows.Scan(
		&issue.ID, &issue.ProjectID, &issue.ProjectKey, &issue.Number, &issue.Title, &issue.Description,
		&issue.Status, &issue.Severity, &issue.Priority, &issue.Assignee, &issue.Reporter,
		&issue.Source, &issue.RequesterName, &issue.RequesterEmail, &customerToken, &created, &updated, &closed,
	); err != nil {
		return Issue{}, err
	}
	if issue.Source == "" {
		issue.Source = "staff"
	}
	issue.CustomerToken = nullString(customerToken)
	issue.CreatedAt = parseTime(created)
	issue.UpdatedAt = parseTime(updated)
	issue.ClosedAt = parseNullTime(closed)
	issue.Key = fmt.Sprintf("%s-%d", issue.ProjectKey, issue.Number)
	issue.Project = issue.ProjectKey
	return issue, nil
}

func scanWebhooks(rows *sql.Rows) []Webhook {
	var hooks []Webhook
	for rows.Next() {
		hook, err := scanWebhook(rows)
		if err == nil {
			hooks = append(hooks, hook)
		}
	}
	return hooks
}

func scanWebhook(rows scanner) (Webhook, error) {
	var hook Webhook
	var projectID sql.NullInt64
	var enabled int
	var eventsJSON string
	var created, updated string
	var lastError, lastDelivered sql.NullString
	if err := rows.Scan(
		&hook.ID, &projectID, &hook.Name, &hook.URL, &hook.Secret, &eventsJSON, &enabled,
		&created, &updated, &hook.LastStatus, &lastError, &lastDelivered,
	); err != nil {
		return Webhook{}, err
	}
	if projectID.Valid {
		v := projectID.Int64
		hook.ProjectID = &v
	}
	_ = json.Unmarshal([]byte(eventsJSON), &hook.Events)
	hook.Enabled = enabled != 0
	hook.CreatedAt = parseTime(created)
	hook.UpdatedAt = parseTime(updated)
	if lastError.Valid {
		hook.LastError = lastError.String
	}
	hook.LastDeliveredAt = parseNullTime(lastDelivered)
	return hook, nil
}

func scanEmailRecipient(rows scanner) (EmailRecipient, error) {
	var recipient EmailRecipient
	if err := rows.Scan(&recipient.UserID, &recipient.Username, &recipient.DisplayName, &recipient.Email, &recipient.Role); err != nil {
		return EmailRecipient{}, err
	}
	return recipient, nil
}

func getEmailNotificationTx(tx *sql.Tx, id int64) (EmailNotification, error) {
	row := tx.QueryRow(`
		SELECT id, project_id, issue_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
		       status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		FROM email_notifications
		WHERE id = ?`, id)
	notification, err := scanEmailNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailNotification{}, ErrNotFound
	}
	return notification, err
}

func scanEmailNotification(rows scanner) (EmailNotification, error) {
	var notification EmailNotification
	var projectID, issueID, userID sql.NullInt64
	var nextAttempt, created string
	var lockedUntil, sentAt sql.NullString
	if err := rows.Scan(
		&notification.ID, &projectID, &issueID, &userID, &notification.RecipientEmail,
		&notification.RecipientName, &notification.Event, &notification.Subject, &notification.BodyText, &notification.BodyHTML,
		&notification.Status, &notification.Attempts, &nextAttempt, &lockedUntil, &notification.LastError, &created, &sentAt,
	); err != nil {
		return EmailNotification{}, err
	}
	if projectID.Valid {
		notification.ProjectID = projectID.Int64
	}
	if issueID.Valid {
		notification.IssueID = issueID.Int64
	}
	if userID.Valid {
		notification.UserID = userID.Int64
	}
	notification.NextAttemptAt = parseTime(nextAttempt)
	notification.LockedUntil = parseNullTime(lockedUntil)
	notification.CreatedAt = parseTime(created)
	notification.SentAt = parseNullTime(sentAt)
	return notification, nil
}

func normalizeSQLError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique constraint") {
		return fmt.Errorf("%w: already exists", ErrConflict)
	}
	if strings.Contains(message, "foreign key constraint") {
		return ErrNotFound
	}
	return err
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

func normalizeProjectRole(role string) string {
	switch strings.TrimSpace(role) {
	case "developer":
		return "agent"
	case "reporter":
		return "customer"
	default:
		return strings.TrimSpace(role)
	}
}

func normalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	address, err := mail.ParseAddress(value)
	if err != nil || strings.TrimSpace(address.Address) == "" {
		return "", fmt.Errorf("%w: invalid email address", ErrValidation)
	}
	return strings.ToLower(address.Address), nil
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
		return []string{"ticket.created", "ticket.updated", "ticket.commented"}, nil
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
		return []string{"ticket.created", "ticket.updated", "ticket.commented"}, nil
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

func emailRetryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 6 {
		attempts = 6
	}
	return time.Duration(1<<(attempts-1)) * time.Minute
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

func publicComments(comments []Comment) []Comment {
	result := make([]Comment, 0, len(comments))
	for _, comment := range comments {
		if comment.Visibility == "" || comment.Visibility == "public" {
			comment.Visibility = "public"
			result = append(result, comment)
		}
	}
	return result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullEmptyString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullZero(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func parseNullTime(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	parsed := parseTime(value.String)
	return &parsed
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL,
	email TEXT,
	role TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	disabled INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	csrf_token TEXT NOT NULL,
	user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS api_tokens (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	prefix TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	last_used_at TEXT
);

CREATE TABLE IF NOT EXISTS projects (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	key TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS project_members (
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (project_id, user_id)
);

CREATE TABLE IF NOT EXISTS issues (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	number INTEGER NOT NULL,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	severity TEXT NOT NULL,
	priority TEXT NOT NULL,
	assignee TEXT NOT NULL DEFAULT '',
	reporter TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT 'staff',
	requester_name TEXT NOT NULL DEFAULT '',
	requester_email TEXT NOT NULL DEFAULT '',
	customer_token TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	closed_at TEXT,
	UNIQUE (project_id, number)
);

CREATE TABLE IF NOT EXISTS issue_tags (
	issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
	tag TEXT NOT NULL,
	PRIMARY KEY (issue_id, tag)
);

CREATE TABLE IF NOT EXISTS comments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
	author TEXT NOT NULL,
	body TEXT NOT NULL,
	visibility TEXT NOT NULL DEFAULT 'public',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS webhooks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	url TEXT NOT NULL,
	secret TEXT NOT NULL,
	events_json TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	last_status INTEGER NOT NULL DEFAULT 0,
	last_error TEXT,
	last_delivered_at TEXT
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	webhook_id INTEGER NOT NULL,
	project_id INTEGER,
	event TEXT NOT NULL,
	issue_id INTEGER,
	status_code INTEGER,
	error TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS email_notifications (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
	issue_id INTEGER REFERENCES issues(id) ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	recipient_email TEXT NOT NULL,
	recipient_name TEXT NOT NULL,
	event TEXT NOT NULL,
	subject TEXT NOT NULL,
	body_text TEXT NOT NULL,
	body_html TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL CHECK (status IN ('pending', 'sending', 'sent', 'failed')) DEFAULT 'pending',
	attempts INTEGER NOT NULL DEFAULT 0,
	next_attempt_at TEXT NOT NULL,
	locked_until TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	sent_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_issues_project_updated ON issues(project_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_project_members_user ON project_members(user_id);
CREATE INDEX IF NOT EXISTS idx_comments_issue ON comments(issue_id);
CREATE INDEX IF NOT EXISTS idx_webhooks_project ON webhooks(project_id);
CREATE INDEX IF NOT EXISTS idx_email_notifications_pending ON email_notifications(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_email_notifications_issue ON email_notifications(issue_id);
CREATE INDEX IF NOT EXISTS idx_email_notifications_user ON email_notifications(user_id);
`
