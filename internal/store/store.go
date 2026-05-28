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

type Ticket struct {
	ID             int64        `json:"id"`
	ProductID      int64        `json:"product_id"`
	ProductKey     string       `json:"product_key"`
	ProductName    string       `json:"product_name"`
	Number         int64        `json:"number"`
	Key            string       `json:"key"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	Product        string       `json:"product"`
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
	UnreadCount    int          `json:"unread_count"`
	HasUnread      bool         `json:"has_unread"`
	LastReadAt     *time.Time   `json:"last_read_at,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	ClosedAt       *time.Time   `json:"closed_at,omitempty"`
}

type Comment struct {
	ID           int64        `json:"id"`
	Author       string       `json:"author"`
	AuthorUserID int64        `json:"-"`
	Body         string       `json:"body"`
	Visibility   string       `json:"visibility"`
	Attachments  []Attachment `json:"attachments,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
}

type CreateTicket struct {
	ProductID      int64  `json:"product_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Product        string `json:"product"`
	Severity       string `json:"-"`
	Priority       string `json:"priority"`
	Assignee       string `json:"assignee"`
	Reporter       string `json:"requester"`
	Source         string `json:"source"`
	RequesterName  string `json:"requester_name"`
	RequesterEmail string `json:"requester_email"`
}

type UpdateTicket struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	Severity    *string `json:"-"`
	Priority    *string `json:"priority"`
	Assignee    *string `json:"assignee"`
}

type AddComment struct {
	Author       string `json:"author"`
	AuthorUserID int64  `json:"-"`
	Body         string `json:"body"`
	Visibility   string `json:"visibility"`
}

type SaveTicketInput struct {
	TicketID         int64
	Patch            UpdateTicket
	Comment          *AddComment
	Attachments      []CreateAttachment
	AttachmentUserID int64
}

type SaveTicketResult struct {
	Previous          Ticket
	Ticket            Ticket
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
	ProductID int64
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
	TicketID        int64     `json:"ticket_id"`
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

type Product struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Role        string    `json:"role,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateProduct struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UpdateProduct struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

type ProductMember struct {
	ProductID   int64     `json:"product_id"`
	UserID      int64     `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

type UpsertProductMember struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

type Webhook struct {
	ID              int64      `json:"id"`
	ProductID       *int64     `json:"product_id,omitempty"`
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
	ProductID       *int64     `json:"product_id,omitempty"`
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
	ProductID *int64   `json:"product_id"`
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
	ProductID  *int64    `json:"product_id,omitempty"`
	Event      string    `json:"event"`
	TicketID   int64     `json:"ticket_id,omitempty"`
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
	ProductID      int64      `json:"product_id,omitempty"`
	TicketID       int64      `json:"ticket_id,omitempty"`
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
	ProductID      int64
	TicketID       int64
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

var validProductRoles = map[string]struct{}{
	"owner":    {},
	"agent":    {},
	"customer": {},
	"viewer":   {},
}

var validTicketSources = map[string]struct{}{
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
var productKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,15}$`)

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
	// Fresh alpha databases are created directly from schemaSQL. Keep this hook
	// for forward-only migrations once a released schema exists.
	return nil
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

func normalizeProductRole(role string) string {
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

CREATE TABLE IF NOT EXISTS products (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	key TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS product_members (
	product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (product_id, user_id)
);

CREATE TABLE IF NOT EXISTS tickets (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
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
	UNIQUE (product_id, number)
);

CREATE TABLE IF NOT EXISTS comments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
	author TEXT NOT NULL,
	author_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
	body TEXT NOT NULL,
	visibility TEXT NOT NULL DEFAULT 'public',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ticket_reads (
	ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	last_read_at TEXT NOT NULL,
	PRIMARY KEY (ticket_id, user_id)
);

CREATE TABLE IF NOT EXISTS attachments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
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
	product_id INTEGER REFERENCES products(id) ON DELETE CASCADE,
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
	product_id INTEGER,
	event TEXT NOT NULL,
	ticket_id INTEGER,
	status_code INTEGER,
	error TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS email_notifications (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	product_id INTEGER REFERENCES products(id) ON DELETE CASCADE,
	ticket_id INTEGER REFERENCES tickets(id) ON DELETE CASCADE,
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email IS NOT NULL AND email <> '';
CREATE INDEX IF NOT EXISTS idx_tickets_product_updated ON tickets(product_id, updated_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tickets_customer_token ON tickets(customer_token) WHERE customer_token IS NOT NULL AND customer_token <> '';
CREATE INDEX IF NOT EXISTS idx_tickets_requester_email ON tickets(requester_email);
CREATE INDEX IF NOT EXISTS idx_product_members_user ON product_members(user_id);
CREATE INDEX IF NOT EXISTS idx_comments_ticket ON comments(ticket_id);
CREATE INDEX IF NOT EXISTS idx_comments_visibility ON comments(ticket_id, visibility);
CREATE INDEX IF NOT EXISTS idx_ticket_reads_user ON ticket_reads(user_id, ticket_id);
CREATE INDEX IF NOT EXISTS idx_attachments_ticket ON attachments(ticket_id);
CREATE INDEX IF NOT EXISTS idx_attachments_comment ON attachments(comment_id);
CREATE INDEX IF NOT EXISTS idx_attachments_storage ON attachments(storage_key);
CREATE INDEX IF NOT EXISTS idx_webhooks_product ON webhooks(product_id);
CREATE INDEX IF NOT EXISTS idx_account_links_user_purpose ON account_links(user_id, purpose, used_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events(actor_user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_email_notifications_pending ON email_notifications(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_email_notifications_ticket ON email_notifications(ticket_id);
CREATE INDEX IF NOT EXISTS idx_email_notifications_user ON email_notifications(user_id);
`
