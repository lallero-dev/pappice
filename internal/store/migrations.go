package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type MigrationInfo struct {
	Version int
	Name    string
}

type MigrationStatus struct {
	Path           string
	Empty          bool
	CurrentVersion int
	TargetVersion  int
	Pending        []MigrationInfo
}

type MigrationOptions struct {
	DryRun bool
}

type MigrationResult struct {
	Status      MigrationStatus
	Applied     []MigrationInfo
	DryRun      bool
	Initialized bool
}

type migration struct {
	Version int
	Name    string
	Up      func(*sql.Tx) error
}

var orderedMigrations = []migration{
	{Version: 1, Name: "baseline_schema", Up: migrateBaselineSchema},
	{Version: 2, Name: "rename_product_roles", Up: migrateRenameProductRoles},
	{Version: 3, Name: "normalize_relational_data", Up: migrateRelationalData},
	{Version: 4, Name: "require_ticket_participants", Up: migrateTicketParticipants},
	{Version: 5, Name: "ticket_status_history", Up: migrateTicketStatusHistory},
	{Version: 6, Name: "schedule_domain_event_retries", Up: migrateDomainEventRetries},
	{Version: 7, Name: "simplify_ticket_statuses", Up: migrateTicketStatuses},
	{Version: 8, Name: "deduplicate_integration_requests", Up: migrateWebhookDeliveryIDs},
}

func CurrentSchemaVersion() int {
	if len(orderedMigrations) == 0 {
		return 0
	}
	return orderedMigrations[len(orderedMigrations)-1].Version
}

func InspectMigration(path string) (MigrationStatus, error) {
	path = defaultDBPath(path)
	status := MigrationStatus{Path: path, TargetVersion: CurrentSchemaVersion()}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			status.Empty = true
			return status, nil
		}
		if err != nil {
			return status, err
		}
		if info.IsDir() {
			return status, fmt.Errorf("%w: database path points to a directory", ErrValidation)
		}
	}

	db, err := openSQLite(path)
	if err != nil {
		return status, err
	}
	defer db.Close()
	if err := configureSQLiteConnection(db); err != nil {
		return status, err
	}
	return inspectMigrationStatus(db, path)
}

func Migrate(path string, opts MigrationOptions) (MigrationResult, error) {
	path = defaultDBPath(path)
	targetPath := path
	cleanup := func() {}
	if opts.DryRun {
		tempPath, err := dryRunDatabase(path)
		if err != nil {
			return MigrationResult{DryRun: true}, err
		}
		targetPath = tempPath
		cleanup = func() { _ = os.Remove(tempPath) }
	}
	defer cleanup()

	db, err := openSQLite(targetPath)
	if err != nil {
		return MigrationResult{DryRun: opts.DryRun}, err
	}
	defer db.Close()
	if err := configureSQLite(db); err != nil {
		return MigrationResult{DryRun: opts.DryRun}, err
	}

	result, err := migrateDB(db, targetPath)
	result.DryRun = opts.DryRun
	result.Status.Path = path
	return result, err
}

func defaultDBPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "pappice.db"
	}
	return path
}

func dryRunDatabase(sourcePath string) (string, error) {
	file, err := os.CreateTemp("", "pappice-migrate-*.db")
	if err != nil {
		return "", err
	}
	tempPath := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := os.Remove(tempPath); err != nil {
		return "", err
	}

	if sourcePath == ":memory:" || strings.HasPrefix(sourcePath, "file:") {
		return "", fmt.Errorf("%w: dry-run requires a filesystem database path", ErrValidation)
	}
	info, err := os.Stat(sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return tempPath, nil
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: database path points to a directory", ErrValidation)
	}

	source, err := openSQLite(sourcePath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	if err := configureSQLiteConnection(source); err != nil {
		return "", err
	}
	if _, err := source.Exec(`VACUUM INTO ?`, tempPath); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("copy database for dry-run: %w", err)
	}
	return tempPath, nil
}

func migrateDB(db *sql.DB, path string) (MigrationResult, error) {
	status, err := inspectMigrationStatus(db, path)
	if err != nil {
		return MigrationResult{Status: status}, err
	}
	result := MigrationResult{Status: status}
	if status.Empty {
		if err := installCurrentSchema(db); err != nil {
			return result, err
		}
		result.Initialized = true
		result.Applied = migrationInfos(orderedMigrations)
		result.Status, err = inspectMigrationStatus(db, path)
		if err != nil {
			return result, err
		}
		return result, validateCurrentSchema(db)
	}
	if status.CurrentVersion > CurrentSchemaVersion() {
		return result, fmt.Errorf("%w: database is at version %d, app supports %d", ErrSchemaTooNew, status.CurrentVersion, CurrentSchemaVersion())
	}
	if len(status.Pending) == 0 {
		return result, validateCurrentSchema(db)
	}
	applied, err := applyPendingMigrations(db, status.Pending)
	result.Applied = applied
	if err != nil {
		return result, err
	}
	result.Status, err = inspectMigrationStatus(db, path)
	if err != nil {
		return result, err
	}
	return result, validateCurrentSchema(db)
}

func inspectMigrationStatus(db *sql.DB, path string) (MigrationStatus, error) {
	status := MigrationStatus{Path: path, TargetVersion: CurrentSchemaVersion()}
	empty, err := databaseIsEmpty(db)
	if err != nil {
		return status, err
	}
	status.Empty = empty
	if empty {
		return status, nil
	}
	hasMigrations, err := tableExists(db, "schema_migrations")
	if err != nil {
		return status, err
	}
	applied := map[int]struct{}{}
	if hasMigrations {
		rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
		if err != nil {
			return status, err
		}
		defer rows.Close()
		for rows.Next() {
			var version int
			if err := rows.Scan(&version); err != nil {
				return status, err
			}
			applied[version] = struct{}{}
			if version > status.CurrentVersion {
				status.CurrentVersion = version
			}
		}
		if err := rows.Err(); err != nil {
			return status, err
		}
	}
	for _, item := range orderedMigrations {
		if _, ok := applied[item.Version]; !ok {
			status.Pending = append(status.Pending, MigrationInfo{Version: item.Version, Name: item.Name})
		}
	}
	return status, nil
}

func installCurrentSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schemaSQL); err != nil {
		return err
	}
	now := nowString()
	for _, item := range orderedMigrations {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			item.Version, item.Name, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func applyPendingMigrations(db *sql.DB, pending []MigrationInfo) ([]MigrationInfo, error) {
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return nil, err
	}
	defer db.Exec(`PRAGMA foreign_keys = ON`)

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return nil, err
	}

	pendingByVersion := map[int]struct{}{}
	for _, item := range pending {
		pendingByVersion[item.Version] = struct{}{}
	}
	var applied []MigrationInfo
	for _, item := range orderedMigrations {
		if _, ok := pendingByVersion[item.Version]; !ok {
			continue
		}
		if err := item.Up(tx); err != nil {
			return nil, fmt.Errorf("migration %03d %s: %w", item.Version, item.Name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			item.Version, item.Name, nowString(),
		); err != nil {
			return nil, err
		}
		applied = append(applied, MigrationInfo{Version: item.Version, Name: item.Name})
	}
	if _, err := tx.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema sync: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return applied, nil
}

func migrateBaselineSchema(tx *sql.Tx) error {
	hasUsers, err := tableExists(tx, "users")
	if err != nil {
		return err
	}
	if !hasUsers {
		return nil
	}
	hasUsername, err := tableHasColumn(tx, "users", "username")
	if err != nil {
		return err
	}
	if hasUsername {
		return fmt.Errorf("%w: unsupported pre-v0.6 username schema", ErrMigrationRequired)
	}
	return nil
}

func migrateRenameProductRoles(tx *sql.Tx) error {
	hasMembers, err := tableExists(tx, "product_members")
	if err != nil {
		return err
	}
	if !hasMembers {
		return nil
	}
	if _, err := tx.Exec(`UPDATE product_members SET role = 'manager' WHERE role = 'owner'`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE product_members SET role = 'staff' WHERE role = 'agent'`); err != nil {
		return err
	}
	return nil
}

func migrateRelationalData(tx *sql.Tx) error {
	hasTickets, err := tableExists(tx, "tickets")
	if err != nil {
		return err
	}
	if hasTickets {
		hasLegacyAssignee, err := tableHasColumn(tx, "tickets", "assignee")
		if err != nil {
			return err
		}
		if hasLegacyAssignee {
			if _, err := tx.Exec(`
		CREATE TABLE tickets_with_user_ids (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			number INTEGER NOT NULL,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			assignee_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			requester_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			source TEXT NOT NULL DEFAULT 'staff',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			closed_at TEXT,
			UNIQUE (product_id, number)
		);
		INSERT INTO tickets_with_user_ids (
			id, product_id, number, title, description, status, priority, assignee_user_id,
			requester_user_id, source, created_at, updated_at, closed_at
		)
		SELECT i.id, i.product_id, i.number, i.title, i.description, i.status, i.priority,
			(SELECT u.id
			 FROM users u
			 JOIN product_members pm ON pm.user_id = u.id AND pm.product_id = i.product_id
			 WHERE lower(u.email) = lower(trim(i.assignee))
			   AND u.role IN ('admin', 'staff')
			   AND u.disabled = 0
			   AND pm.role IN ('manager', 'staff')
			 LIMIT 1),
			(SELECT u.id
			 FROM users u
			 WHERE lower(u.email) = lower(COALESCE(NULLIF(trim(i.requester_email), ''), trim(i.reporter)))
			 LIMIT 1),
			i.source,
			i.created_at, i.updated_at, i.closed_at
		FROM tickets i;
		DROP TABLE tickets;
		ALTER TABLE tickets_with_user_ids RENAME TO tickets;
	`); err != nil {
				return err
			}
		}
	}
	if hasAuditEvents, err := tableExists(tx, "audit_events"); err != nil {
		return err
	} else if hasAuditEvents {
		if legacy, err := tableHasColumn(tx, "audit_events", "actor_username"); err != nil {
			return err
		} else if legacy {
			if _, err := tx.Exec(`ALTER TABLE audit_events RENAME COLUMN actor_username TO actor_email`); err != nil {
				return err
			}
		}
	}
	if hasDomainEvents, err := tableExists(tx, "domain_events"); err != nil {
		return err
	} else if hasDomainEvents {
		if legacy, err := tableHasColumn(tx, "domain_events", "actor_username"); err != nil {
			return err
		} else if legacy {
			if _, err := tx.Exec(`ALTER TABLE domain_events DROP COLUMN actor_username`); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateTicketParticipants(tx *sql.Tx) error {
	hasTickets, err := tableExists(tx, "tickets")
	if err != nil || !hasTickets {
		return err
	}
	hasCreator, err := tableHasColumn(tx, "tickets", "created_by_user_id")
	if err != nil {
		return err
	}
	if hasCreator {
		return nil
	}
	var missingRequesters int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tickets WHERE requester_user_id IS NULL`).Scan(&missingRequesters); err != nil {
		return err
	}
	if missingRequesters != 0 {
		return fmt.Errorf("%w: tickets without requesters: %d; assign one before migrating", ErrMigrationRequired, missingRequesters)
	}
	_, err = tx.Exec(`
		CREATE TABLE tickets_with_participants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			number INTEGER NOT NULL,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			assignee_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			requester_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
			created_by_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			closed_at TEXT,
			UNIQUE (product_id, number)
		);
		INSERT INTO tickets_with_participants (
			id, product_id, number, title, description, status, priority, assignee_user_id,
			requester_user_id, created_by_user_id, created_at, updated_at, closed_at
		)
		SELECT id, product_id, number, title, description, status, priority, assignee_user_id,
			requester_user_id, requester_user_id, created_at, updated_at, closed_at
		FROM tickets;
		DROP TABLE tickets;
		ALTER TABLE tickets_with_participants RENAME TO tickets;
	`)
	return err
}

func migrateTicketStatusHistory(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS ticket_status_changes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
			actor_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
			previous_status TEXT NOT NULL,
			current_status TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_ticket_status_changes_ticket
			ON ticket_status_changes(ticket_id, created_at, id);
	`)
	return err
}

func migrateDomainEventRetries(tx *sql.Tx) error {
	hasEvents, err := tableExists(tx, "domain_events")
	if err != nil || !hasEvents {
		return err
	}
	hasNextAttempt, err := tableHasColumn(tx, "domain_events", "next_attempt_at")
	if err != nil {
		return err
	}
	if !hasNextAttempt {
		if _, err := tx.Exec(`ALTER TABLE domain_events ADD COLUMN next_attempt_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00Z'`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`
		UPDATE domain_events
		SET status = CASE WHEN status IN ('processing', 'failed') THEN 'pending' ELSE status END,
		    attempts = 0,
		    next_attempt_at = updated_at,
		    locked_until = NULL;
		DROP INDEX IF EXISTS idx_domain_events_pending;
		CREATE INDEX idx_domain_events_pending
			ON domain_events(status, next_attempt_at, locked_until);
	`)
	return err
}

func migrateWebhookDeliveryIDs(tx *sql.Tx) error {
	hasNotifications, err := tableExists(tx, "webhook_notifications")
	if err != nil || !hasNotifications {
		return err
	}
	hasDeliveryID, err := tableHasColumn(tx, "webhook_notifications", "delivery_id")
	if err != nil || hasDeliveryID {
		return err
	}
	_, err = tx.Exec(`
		ALTER TABLE webhook_notifications ADD COLUMN delivery_id TEXT NOT NULL DEFAULT '';
		UPDATE webhook_notifications SET delivery_id = lower(hex(randomblob(16)));
	`)
	return err
}

func migrateTicketStatuses(tx *sql.Tx) error {
	hasTickets, err := tableExists(tx, "tickets")
	if err != nil || !hasTickets {
		return err
	}
	var invalid int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tickets WHERE status NOT IN ('new', 'assigned', 'resolved', 'rejected', 'open', 'closed')`).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("%w: tickets with unknown statuses: %d", ErrMigrationRequired, invalid)
	}
	if _, err := tx.Exec(`
		UPDATE tickets
		SET closed_at = CASE
				WHEN status IN ('resolved', 'rejected', 'closed') THEN COALESCE(closed_at, updated_at)
				ELSE NULL
			END,
			status = CASE
				WHEN status IN ('new', 'assigned', 'open') THEN 'open'
				ELSE 'closed'
			END
	`); err != nil {
		return err
	}

	hasHistory, err := tableExists(tx, "ticket_status_changes")
	if err != nil || !hasHistory {
		return err
	}
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM ticket_status_changes
		WHERE previous_status NOT IN ('new', 'assigned', 'resolved', 'rejected', 'open', 'closed')
		   OR current_status NOT IN ('new', 'assigned', 'resolved', 'rejected', 'open', 'closed')
	`).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("%w: ticket status changes with unknown statuses: %d", ErrMigrationRequired, invalid)
	}
	_, err = tx.Exec(`
		UPDATE ticket_status_changes
		SET previous_status = CASE WHEN previous_status IN ('new', 'assigned', 'open') THEN 'open' ELSE 'closed' END,
		    current_status = CASE WHEN current_status IN ('new', 'assigned', 'open') THEN 'open' ELSE 'closed' END;
		DELETE FROM ticket_status_changes WHERE previous_status = current_status;
	`)
	return err
}

type tableQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func databaseIsEmpty(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type IN ('table', 'index', 'trigger', 'view')
			AND name NOT LIKE 'sqlite_%'`).Scan(&count)
	return count == 0, err
}

func tableExists(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, table string) (bool, error) {
	var count int
	err := q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	return count > 0, err
}

func tableHasColumn(q tableQueryer, table, column string) (bool, error) {
	quoted, err := quoteIdentifier(table)
	if err != nil {
		return false, err
	}
	rows, err := q.Query(`PRAGMA table_info(` + quoted + `)`)
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
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func quoteIdentifier(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%w: empty identifier", ErrValidation)
	}
	for _, char := range value {
		if char == '_' || char >= '0' && char <= '9' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' {
			continue
		}
		return "", fmt.Errorf("%w: unsafe identifier", ErrValidation)
	}
	return `"` + value + `"`, nil
}

func validateCurrentSchema(db *sql.DB) error {
	hasUsers, err := tableExists(db, "users")
	if err != nil {
		return err
	}
	if !hasUsers {
		return fmt.Errorf("%w: users table is missing", ErrValidation)
	}
	hasUsername, err := tableHasColumn(db, "users", "username")
	if err != nil {
		return err
	}
	if hasUsername {
		return fmt.Errorf("%w: run \"pappice db migrate\"", ErrMigrationRequired)
	}
	var integrity string
	rows, err := db.Query(`PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&integrity); err != nil {
			return err
		}
		if integrity != "ok" {
			return fmt.Errorf("%w: integrity check failed: %s", ErrValidation, integrity)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	fkRows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer fkRows.Close()
	if fkRows.Next() {
		return fmt.Errorf("%w: foreign key check failed", ErrValidation)
	}
	return fkRows.Err()
}

func migrationInfos(items []migration) []MigrationInfo {
	infos := make([]MigrationInfo, 0, len(items))
	for _, item := range items {
		infos = append(infos, MigrationInfo{Version: item.Version, Name: item.Name})
	}
	return infos
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
