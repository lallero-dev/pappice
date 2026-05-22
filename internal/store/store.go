package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound              = errors.New("not found")
	ErrValidation            = errors.New("validation failed")
	ErrConflict              = errors.New("conflict")
	ErrPasswordResetRequired = errors.New("password setup or reset is required")
)

type Issue struct {
	ID             int64        `json:"id"`
	ProjectID      int64        `json:"project_id"`
	ProjectKey     string       `json:"project_key"`
	Number         int64        `json:"number"`
	Key            string       `json:"key"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	Project        string       `json:"project"`
	Status         string       `json:"status"`
	Severity       string       `json:"-"`
	Priority       string       `json:"priority"`
	Assignee       string       `json:"assignee"`
	Reporter       string       `json:"requester"`
	Source         string       `json:"source"`
	RequesterName  string       `json:"requester_name,omitempty"`
	RequesterEmail string       `json:"requester_email,omitempty"`
	CustomerToken  string       `json:"-"`
	Attachments    []Attachment `json:"attachments,omitempty"`
	Comments       []Comment    `json:"comments"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	ClosedAt       *time.Time   `json:"closed_at,omitempty"`
}

type Comment struct {
	ID          int64        `json:"id"`
	Author      string       `json:"author"`
	Body        string       `json:"body"`
	Visibility  string       `json:"visibility"`
	Attachments []Attachment `json:"attachments,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
}

type CreateIssue struct {
	ProjectID      int64  `json:"project_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Project        string `json:"project"`
	Severity       string `json:"-"`
	Priority       string `json:"priority"`
	Assignee       string `json:"assignee"`
	Reporter       string `json:"requester"`
	Source         string `json:"source"`
	RequesterName  string `json:"requester_name"`
	RequesterEmail string `json:"requester_email"`
}

type UpdateIssue struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Severity    *string `json:"-"`
	Priority    *string `json:"priority"`
	Assignee    *string `json:"assignee"`
}

type AddComment struct {
	Author     string `json:"author"`
	Body       string `json:"body"`
	Visibility string `json:"visibility"`
}

type SaveIssueInput struct {
	IssueID          int64
	Patch            UpdateIssue
	Comment          *AddComment
	Attachments      []CreateAttachment
	AttachmentUserID int64
}

type SaveIssueResult struct {
	Previous          Issue
	Issue             Issue
	HasPatch          bool
	HasComment        bool
	PublicComment     bool
	AssignmentChanged bool
	CommentID         int64
}

type Filter struct {
	Query     string
	Status    string
	Statuses  []string
	ProjectID int64
	Assignee  string
}

type User struct {
	ID                    int64     `json:"id"`
	Username              string    `json:"username"`
	DisplayName           string    `json:"display_name"`
	Email                 string    `json:"email"`
	Role                  string    `json:"role"`
	PasswordHash          string    `json:"password_hash,omitempty"`
	Disabled              bool      `json:"disabled"`
	PasswordResetRequired bool      `json:"password_reset_required"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type PublicUser struct {
	ID                    int64     `json:"id"`
	Username              string    `json:"username"`
	DisplayName           string    `json:"display_name"`
	Email                 string    `json:"email"`
	Role                  string    `json:"role"`
	Disabled              bool      `json:"disabled"`
	PasswordResetRequired bool      `json:"password_reset_required"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type Attachment struct {
	ID              int64     `json:"id"`
	IssueID         int64     `json:"issue_id"`
	CommentID       *int64    `json:"comment_id,omitempty"`
	Filename        string    `json:"filename"`
	ContentType     string    `json:"content_type"`
	SizeBytes       int64     `json:"size_bytes"`
	SHA256          string    `json:"sha256"`
	StorageKey      string    `json:"-"`
	CreatedByUserID int64     `json:"created_by_user_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type CreateAttachment struct {
	Filename    string
	ContentType string
	SizeBytes   int64
	SHA256      string
	StorageKey  string
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

type AccountLink struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	Purpose   string     `json:"purpose"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type AccountLinkStatus struct {
	Purpose      string
	ExpiresAt    time.Time
	UsedAt       *time.Time
	UserDisabled bool
}

type AuditEvent struct {
	ID            int64     `json:"id"`
	ActorUserID   int64     `json:"actor_user_id"`
	ActorUsername string    `json:"actor_username"`
	Action        string    `json:"action"`
	TargetType    string    `json:"target_type"`
	TargetID      int64     `json:"target_id"`
	TargetName    string    `json:"target_name"`
	IP            string    `json:"ip"`
	DetailsJSON   string    `json:"details_json,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type CreateAuditEvent struct {
	ActorUserID   int64
	ActorUsername string
	Action        string
	TargetType    string
	TargetID      int64
	TargetName    string
	IP            string
	DetailsJSON   string
}

type AuditEventFilter struct {
	Query  string
	Limit  int
	Offset int
}

type AuditEventPage struct {
	Events []AuditEvent
	Total  int
	Limit  int
	Offset int
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

type EmailNotificationFilter struct {
	Status string
	Query  string
	Limit  int
	Offset int
}

type EmailNotificationPage struct {
	Notifications []EmailNotification
	Total         int
	Limit         int
	Offset        int
}

type EmailNotificationStats struct {
	Total      int        `json:"total"`
	Pending    int        `json:"pending"`
	Sending    int        `json:"sending"`
	Sent       int        `json:"sent"`
	Failed     int        `json:"failed"`
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
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
	SendAfter      time.Time
	Coalesce       bool
}

type Store struct {
	db   *sql.DB
	path string
}

var validStatuses = map[string]struct{}{
	"new":      {},
	"assigned": {},
	"resolved": {},
	"rejected": {},
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
	"admin":    {},
	"staff":    {},
	"customer": {},
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

var validEmailEvents = map[string]struct{}{
	"ticket.created":   {},
	"ticket.updated":   {},
	"ticket.commented": {},
	"ticket.assigned":  {},
	"account.setup":    {},
	"account.reset":    {},
	"email.test":       {},
}

var validEmailNotificationStatuses = map[string]struct{}{
	"pending": {},
	"sending": {},
	"sent":    {},
	"failed":  {},
}

func normalizePage(limit, offset, defaultLimit, maxLimit int) (int, int) {
	if defaultLimit < 1 {
		defaultLimit = 25
	}
	if maxLimit < defaultLimit {
		maxLimit = defaultLimit
	}
	if limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

var validAccountLinkPurposes = map[string]struct{}{
	"setup": {},
	"reset": {},
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{1,46}[a-z0-9]$`)
var projectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,15}$`)

func Open(path string) (*Store, error) {
	if path == "" {
		path = "pappice.db"
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

	s := &Store{db: db, path: path}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
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
	if _, err := s.db.Exec(`PRAGMA synchronous = NORMAL;`); err != nil {
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
	if ok, err := s.columnExists("users", "password_reset_required"); err != nil {
		return err
	} else if !ok {
		if _, err := s.db.Exec(`ALTER TABLE users ADD COLUMN password_reset_required INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if ok, err := s.usersRolesAreCurrent(); err != nil {
		return err
	} else if !ok {
		if err := s.rebuildUsersRoleConstraint(); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE users SET role = 'staff' WHERE role = 'user'`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE users SET role = 'customer' WHERE role = 'client'`); err != nil {
		return err
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
	if _, err := s.db.Exec(`UPDATE issues SET status = 'assigned' WHERE status IN ('acknowledged', 'confirmed', 'open', 'pending')`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE issues SET status = 'resolved' WHERE status = 'closed'`); err != nil {
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

func (s *Store) usersRolesAreCurrent() (bool, error) {
	var sqlText string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&sqlText); err != nil {
		return false, err
	}
	if !strings.Contains(sqlText, "CHECK") {
		return true, nil
	}
	return (strings.Contains(sqlText, "'staff'") || strings.Contains(sqlText, `"staff"`)) &&
		(strings.Contains(sqlText, "'customer'") || strings.Contains(sqlText, `"customer"`)), nil
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
			password_reset_required INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO users_new (id, username, display_name, email, role, password_hash, disabled, password_reset_required, created_at, updated_at)
		SELECT id, username, display_name, email, role, password_hash, disabled, password_reset_required, created_at, updated_at
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

type scanner interface {
	Scan(dest ...any) error
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

func normalizeGlobalRole(role string) string {
	switch strings.TrimSpace(role) {
	case "user":
		return "staff"
	case "client":
		return "customer"
	default:
		return strings.TrimSpace(role)
	}
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

func normalizeFilterStatuses(single string, values []string) []string {
	seen := make(map[string]struct{}, len(values)+1)
	result := make([]string, 0, len(values)+1)
	appendStatus := func(value string) {
		for _, part := range strings.Split(value, ",") {
			status := strings.TrimSpace(part)
			if status == "" {
				continue
			}
			if _, ok := seen[status]; ok {
				continue
			}
			seen[status] = struct{}{}
			result = append(result, status)
		}
	}
	appendStatus(single)
	for _, value := range values {
		appendStatus(value)
	}
	return result
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
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
	password_reset_required INTEGER NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS account_links (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	purpose TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	expires_at TEXT NOT NULL,
	used_at TEXT,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
	actor_username TEXT NOT NULL,
	action TEXT NOT NULL,
	target_type TEXT NOT NULL,
	target_id INTEGER,
	target_name TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	details_json TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
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

CREATE TABLE IF NOT EXISTS comments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
	author TEXT NOT NULL,
	body TEXT NOT NULL,
	visibility TEXT NOT NULL DEFAULT 'public',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS attachments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
	comment_id INTEGER REFERENCES comments(id) ON DELETE CASCADE,
	filename TEXT NOT NULL,
	content_type TEXT NOT NULL,
	size_bytes INTEGER NOT NULL,
	sha256 TEXT NOT NULL,
	storage_key TEXT NOT NULL,
	created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
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
CREATE INDEX IF NOT EXISTS idx_attachments_issue ON attachments(issue_id);
CREATE INDEX IF NOT EXISTS idx_attachments_comment ON attachments(comment_id);
CREATE INDEX IF NOT EXISTS idx_attachments_storage ON attachments(storage_key);
CREATE INDEX IF NOT EXISTS idx_webhooks_project ON webhooks(project_id);
CREATE INDEX IF NOT EXISTS idx_account_links_user_purpose ON account_links(user_id, purpose, used_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events(actor_user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_email_notifications_pending ON email_notifications(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_email_notifications_issue ON email_notifications(issue_id);
CREATE INDEX IF NOT EXISTS idx_email_notifications_user ON email_notifications(user_id);
`
