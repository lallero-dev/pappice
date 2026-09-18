package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBaselineMigrationRejectsUnsupportedUsernameSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE users (
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
		INSERT INTO users (id, username, display_name, email, role, password_hash, created_at, updated_at)
		VALUES (1, 'alice', 'Alice Staff', 'alice@example.test', 'staff', 'hash', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
	`)
	if err != nil {
		t.Fatalf("seed legacy db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	if _, err := Open(path); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("open legacy store error = %v, want ErrMigrationRequired", err)
	}
	status, err := InspectMigration(path)
	if err != nil {
		t.Fatalf("inspect migration: %v", err)
	}
	if got, want := migrationNames(status.Pending), []string{"baseline_schema", "rename_product_roles", "normalize_relational_data", "require_ticket_participants", "ticket_status_history", "schedule_domain_event_retries", "simplify_ticket_statuses", "deduplicate_integration_requests"}; status.CurrentVersion != 0 || !slices.Equal(got, want) {
		t.Fatalf("migration status = %#v", status)
	}
	if _, err := Migrate(path, MigrationOptions{DryRun: true}); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("dry-run migration error = %v, want ErrMigrationRequired", err)
	}
	if _, err := Migrate(path, MigrationOptions{}); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("migration error = %v, want ErrMigrationRequired", err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen legacy db: %v", err)
	}
	defer db.Close()
	if exists, err := tableExists(db, "schema_migrations"); err != nil || exists {
		t.Fatalf("schema_migrations exists after rejected migration = %v err=%v, want false", exists, err)
	}
}

func TestMigrateRenamesProductRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		);
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			display_name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			disabled INTEGER NOT NULL DEFAULT 0,
			password_reset_required INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE products (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE product_members (
			product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (product_id, user_id)
		);
		INSERT INTO schema_migrations (version, name, applied_at) VALUES (1, 'baseline_schema', '2026-01-01T00:00:00Z');
		INSERT INTO users (id, display_name, email, role, password_hash, created_at, updated_at)
		VALUES
			(1, 'Alice Manager', 'manager@example.test', 'staff', 'hash', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			(2, 'Bob Staff', 'staff@example.test', 'staff', 'hash', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			(3, 'Client', 'client@example.test', 'customer', 'hash', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO products (id, key, name, created_at, updated_at)
		VALUES (1, 'SUP', 'Support', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO product_members (product_id, user_id, role, created_at)
		VALUES
			(1, 1, 'owner', '2026-01-01T00:00:00Z'),
			(1, 2, 'agent', '2026-01-01T00:00:00Z'),
			(1, 3, 'customer', '2026-01-01T00:00:00Z');
	`)
	if err != nil {
		t.Fatalf("seed db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	status, err := InspectMigration(path)
	if err != nil {
		t.Fatalf("inspect before migration: %v", err)
	}
	if got, want := migrationNames(status.Pending), []string{"rename_product_roles", "normalize_relational_data", "require_ticket_participants", "ticket_status_history", "schedule_domain_event_retries", "simplify_ticket_statuses", "deduplicate_integration_requests"}; status.CurrentVersion != 1 || !slices.Equal(got, want) {
		t.Fatalf("before migration status = %#v", status)
	}
	result, err := Migrate(path, MigrationOptions{})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got, want := migrationNames(result.Applied), []string{"rename_product_roles", "normalize_relational_data", "require_ticket_participants", "ticket_status_history", "schedule_domain_event_retries", "simplify_ticket_statuses", "deduplicate_integration_requests"}; !slices.Equal(got, want) {
		t.Fatalf("applied migrations = %#v", result.Applied)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT role FROM product_members ORDER BY user_id`)
	if err != nil {
		t.Fatalf("query product roles: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatalf("scan role: %v", err)
		}
		got = append(got, role)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("roles rows: %v", err)
	}
	if want := []string{"manager", "staff", "customer"}; !slices.Equal(got, want) {
		t.Fatalf("migrated product roles = %#v, want %#v", got, want)
	}
}

func TestMigrateRelationalData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relations.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL);
		CREATE TABLE users (
			id INTEGER PRIMARY KEY, display_name TEXT NOT NULL, email TEXT NOT NULL UNIQUE, role TEXT NOT NULL,
			password_hash TEXT NOT NULL, disabled INTEGER NOT NULL DEFAULT 0,
			password_reset_required INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		);
		CREATE TABLE products (
			id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL
		);
		CREATE TABLE product_members (
			product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY (product_id, user_id)
		);
		CREATE TABLE tickets (
			id INTEGER PRIMARY KEY, product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			number INTEGER NOT NULL, title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
			severity TEXT NOT NULL, priority TEXT NOT NULL, assignee TEXT NOT NULL DEFAULT '', reporter TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT 'staff', requester_name TEXT NOT NULL DEFAULT '', requester_email TEXT NOT NULL DEFAULT '',
			customer_token TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, closed_at TEXT,
			UNIQUE (product_id, number)
		);
		CREATE TABLE comments (
			id INTEGER PRIMARY KEY, ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
			author TEXT NOT NULL, author_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			body TEXT NOT NULL, visibility TEXT NOT NULL DEFAULT 'public', created_at TEXT NOT NULL
		);
		CREATE TABLE audit_events (
			id INTEGER PRIMARY KEY, domain_event_id INTEGER NOT NULL DEFAULT 0,
			actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, actor_username TEXT NOT NULL,
			action TEXT NOT NULL, target_type TEXT NOT NULL, target_id INTEGER, target_name TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '', details_json TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
		);
		CREATE TABLE domain_events (
			id INTEGER PRIMARY KEY, type TEXT NOT NULL, product_id INTEGER NOT NULL DEFAULT 0, ticket_id INTEGER NOT NULL DEFAULT 0,
			actor_user_id INTEGER NOT NULL DEFAULT 0, actor_username TEXT NOT NULL DEFAULT '', actor_display_name TEXT NOT NULL DEFAULT '',
			actor_email TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', payload_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0, locked_until TEXT,
			last_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL, processed_at TEXT
		);
		INSERT INTO schema_migrations VALUES (1, 'baseline_schema', '2026-01-01T00:00:00Z'), (2, 'rename_product_roles', '2026-01-01T00:00:00Z');
		INSERT INTO users VALUES
			(1, 'Valid Staff', 'staff@example.test', 'staff', 'hash', 0, 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			(2, 'Viewer', 'viewer@example.test', 'staff', 'hash', 0, 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO products VALUES (1, 'SUP', 'Support', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO product_members VALUES
			(1, 1, 'staff', '2026-01-01T00:00:00Z'), (1, 2, 'viewer', '2026-01-01T00:00:00Z');
		INSERT INTO tickets VALUES
			(1, 1, 1, 'Valid assignment', '', 'assigned', 'support', 'normal', 'STAFF@example.test', 'staff@example.test', 'staff', '', 'staff@example.test', NULL, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL),
			(2, 1, 2, 'Invalid assignment', '', 'assigned', 'support', 'normal', 'viewer@example.test', 'viewer@example.test', 'staff', '', 'viewer@example.test', NULL, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', NULL);
		INSERT INTO comments VALUES (1, 1, 'Valid Staff', 1, 'Preserved reply', 'public', '2026-01-01T00:00:00Z');
		INSERT INTO audit_events VALUES (1, 1, 1, 'staff@example.test', 'ticket.created', 'ticket', 1, 'SUP-1', '', '', '2026-01-01T00:00:00Z');
		INSERT INTO domain_events VALUES (1, 'ticket.created', 1, 1, 1, 'staff@example.test', 'Valid Staff', 'staff@example.test', 'staff', '{}', 'processed', 0, NULL, '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
	`)
	if err != nil {
		t.Fatalf("seed db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	result, err := Migrate(path, MigrationOptions{})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got, want := migrationNames(result.Applied), []string{"normalize_relational_data", "require_ticket_participants", "ticket_status_history", "schedule_domain_event_retries", "simplify_ticket_statuses", "deduplicate_integration_requests"}; !slices.Equal(got, want) {
		t.Fatalf("applied migrations = %#v", result.Applied)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	for _, column := range []string{"assignee", "severity", "reporter", "requester_name", "requester_email", "customer_token", "source", "customer_facing"} {
		if legacy, err := tableHasColumn(db, "tickets", column); err != nil || legacy {
			t.Fatalf("legacy ticket column %q exists = %v err=%v", column, legacy, err)
		}
	}
	for _, table := range []string{"audit_events", "domain_events"} {
		if legacy, err := tableHasColumn(db, table, "actor_username"); err != nil || legacy {
			t.Fatalf("legacy actor column in %q exists = %v err=%v", table, legacy, err)
		}
	}
	rows, err := db.Query(`
		SELECT COALESCE(assignee_user_id, 0), COALESCE(requester_user_id, 0),
		       COALESCE(created_by_user_id, 0)
		FROM tickets ORDER BY id`)
	if err != nil {
		t.Fatalf("query assignments: %v", err)
	}
	defer rows.Close()
	var assignees, requesters, creators []int64
	for rows.Next() {
		var assigneeID, requesterID, creatorID int64
		if err := rows.Scan(&assigneeID, &requesterID, &creatorID); err != nil {
			t.Fatalf("scan assignment: %v", err)
		}
		assignees = append(assignees, assigneeID)
		requesters = append(requesters, requesterID)
		creators = append(creators, creatorID)
	}
	if want := []int64{1, 0}; !slices.Equal(assignees, want) {
		t.Fatalf("migrated assignments = %#v, want %#v", assignees, want)
	}
	if want := []int64{1, 2}; !slices.Equal(requesters, want) {
		t.Fatalf("migrated requesters = %#v, want %#v", requesters, want)
	}
	if !slices.Equal(creators, requesters) {
		t.Fatalf("migrated creators = %#v, want %#v", creators, requesters)
	}
	var comments int
	if err := db.QueryRow(`SELECT COUNT(*) FROM comments WHERE ticket_id = 1`).Scan(&comments); err != nil || comments != 1 {
		t.Fatalf("preserved comments = %d err=%v, want 1", comments, err)
	}
	var auditEmail, eventEmail string
	if err := db.QueryRow(`SELECT actor_email FROM audit_events WHERE id = 1`).Scan(&auditEmail); err != nil {
		t.Fatalf("query migrated audit actor: %v", err)
	}
	if err := db.QueryRow(`SELECT actor_email FROM domain_events WHERE id = 1`).Scan(&eventEmail); err != nil {
		t.Fatalf("query migrated event actor: %v", err)
	}
	if auditEmail != "staff@example.test" || eventEmail != auditEmail {
		t.Fatalf("migrated actor emails = audit %q event %q", auditEmail, eventEmail)
	}
}

func TestMigrateSchedulesFailedDomainEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	tracker := openTestStore(t, path)
	event, err := tracker.CreateDomainEvent(CreateDomainEvent{Type: "user.updated"})
	if err != nil {
		t.Fatalf("create domain event: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	_, err = db.Exec(`
		DROP INDEX idx_domain_events_pending;
		ALTER TABLE domain_events DROP COLUMN next_attempt_at;
		UPDATE domain_events SET status = 'failed', attempts = 7, last_error = 'old failure' WHERE id = ?;
		DELETE FROM schema_migrations WHERE version IN (6, 7);
	`, event.ID)
	if err != nil {
		t.Fatalf("prepare version 5 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result, err := Migrate(path, MigrationOptions{})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got, want := migrationNames(result.Applied), []string{"schedule_domain_event_retries", "simplify_ticket_statuses"}; !slices.Equal(got, want) {
		t.Fatalf("applied migrations = %#v, want %#v", got, want)
	}
	tracker = openTestStore(t, path)
	migrated, err := tracker.GetDomainEvent(event.ID)
	if err != nil {
		t.Fatalf("get migrated event: %v", err)
	}
	if migrated.Status != "pending" || migrated.Attempts != 0 || migrated.NextAttemptAt.IsZero() {
		t.Fatalf("migrated event = %#v", migrated)
	}
}

func TestMigrateSimplifiesTicketStatuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statuses.db")
	tracker := openTestStore(t, path)
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	openTicket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Reopened", ActorUserID: admin.ID})
	if err != nil {
		t.Fatalf("create open ticket: %v", err)
	}
	closedTicket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Closed", ActorUserID: admin.ID})
	if err != nil {
		t.Fatalf("create closed ticket: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 7`); err != nil {
		t.Fatalf("remove current migration: %v", err)
	}
	if _, err := db.Exec(`UPDATE tickets SET status = 'assigned', closed_at = '2026-01-01T00:00:00Z' WHERE id = ?`, openTicket.ID); err != nil {
		t.Fatalf("prepare open ticket: %v", err)
	}
	if _, err := db.Exec(`UPDATE tickets SET status = 'resolved', closed_at = NULL WHERE id = ?`, closedTicket.ID); err != nil {
		t.Fatalf("prepare closed ticket: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO ticket_status_changes (ticket_id, actor_user_id, previous_status, current_status, created_at) VALUES
			(?, ?, 'new', 'assigned', '2026-01-01T00:00:00Z'),
			(?, ?, 'assigned', 'resolved', '2026-01-02T00:00:00Z'),
			(?, ?, 'resolved', 'rejected', '2026-01-03T00:00:00Z'),
			(?, ?, 'rejected', 'assigned', '2026-01-04T00:00:00Z');
	`, openTicket.ID, admin.ID, openTicket.ID, admin.ID,
		openTicket.ID, admin.ID, openTicket.ID, admin.ID)
	if err != nil {
		t.Fatalf("prepare version 6 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result, err := Migrate(path, MigrationOptions{})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got, want := migrationNames(result.Applied), []string{"simplify_ticket_statuses"}; !slices.Equal(got, want) {
		t.Fatalf("applied migrations = %#v, want %#v", got, want)
	}
	tracker = openTestStore(t, path)
	migratedOpen, err := tracker.GetTicket(openTicket.ID)
	if err != nil {
		t.Fatalf("get open ticket: %v", err)
	}
	if migratedOpen.Status != "open" || migratedOpen.ClosedAt != nil || len(migratedOpen.StatusChanges) != 2 ||
		migratedOpen.StatusChanges[0].PreviousStatus != "open" || migratedOpen.StatusChanges[0].CurrentStatus != "closed" ||
		migratedOpen.StatusChanges[1].PreviousStatus != "closed" || migratedOpen.StatusChanges[1].CurrentStatus != "open" {
		t.Fatalf("migrated open ticket = %#v", migratedOpen)
	}
	migratedClosed, err := tracker.GetTicket(closedTicket.ID)
	if err != nil {
		t.Fatalf("get closed ticket: %v", err)
	}
	if migratedClosed.Status != "closed" || migratedClosed.ClosedAt == nil {
		t.Fatalf("migrated closed ticket = %#v", migratedClosed)
	}
}

func TestTicketParticipantMigrationRejectsMissingRequester(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE tickets (
			id INTEGER PRIMARY KEY,
			requester_user_id INTEGER
		);
		INSERT INTO tickets (id) VALUES (1);
	`); err != nil {
		t.Fatalf("create legacy tickets: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration: %v", err)
	}
	defer tx.Rollback()
	err = migrateTicketParticipants(tx)
	if !errors.Is(err, ErrMigrationRequired) || !strings.Contains(err.Error(), "tickets without requesters: 1") {
		t.Fatalf("migration error = %v, want missing requester guidance", err)
	}
}

func TestMigrateRollsBackFailedPlan(t *testing.T) {
	originalMigrations := orderedMigrations
	orderedMigrations = []migration{
		{
			Version: 1,
			Name:    "create_marker",
			Up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TABLE migration_marker (id INTEGER PRIMARY KEY)`)
				return err
			},
		},
		{
			Version: 2,
			Name:    "fail_after_marker",
			Up: func(tx *sql.Tx) error {
				return errors.New("boom")
			},
		},
	}
	defer func() { orderedMigrations = originalMigrations }()

	path := filepath.Join(t.TempDir(), "failed.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatalf("seed db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	result, err := Migrate(path, MigrationOptions{})
	if err == nil {
		t.Fatal("migration should fail")
	}
	if len(result.Applied) != 0 {
		t.Fatalf("rolled-back migrations reported as applied: %#v", result.Applied)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	if exists, err := tableExists(db, "migration_marker"); err != nil || exists {
		t.Fatalf("marker table exists after failed plan = %v err=%v, want false", exists, err)
	}
	if exists, err := tableExists(db, "schema_migrations"); err != nil || exists {
		t.Fatalf("schema_migrations exists after failed plan = %v err=%v, want false", exists, err)
	}
}

func migrationNames(migrations []MigrationInfo) []string {
	names := make([]string, len(migrations))
	for i, migration := range migrations {
		names[i] = migration.Name
	}
	return names
}

func TestMigrateWebhookDeliveryIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	tracker := openTestStore(t, path)
	// Recreate the old queue shape, preserving rows in every delivery state.
	if _, err := tracker.CreateWebhook(CreateWebhook{Name: "test", URL: "https://example.test/hook", Events: []string{"ticket.created"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.db.Exec(`
		DELETE FROM schema_migrations WHERE version = 8;
		DROP TABLE ticket_requests;
		ALTER TABLE webhook_notifications DROP COLUMN delivery_id;
		INSERT INTO webhook_notifications (webhook_id, event, payload_json, status, attempts, next_attempt_at, created_at)
		VALUES (1, 'ticket.created', '{}', 'pending', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		       (1, 'ticket.created', '{}', 'sending', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		       (1, 'ticket.created', '{}', 'sent', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		       (1, 'ticket.created', '{}', 'failed', 5, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(path, MigrationOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if exists, err := tableHasColumn(tracker.db, "webhook_notifications", "delivery_id"); err != nil || exists {
		t.Fatalf("dry run changed source: exists = %v, err = %v", exists, err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(path, MigrationOptions{}); err != nil {
		t.Fatal(err)
	}
	tracker = openTestStore(t, path)
	ids := map[string]bool{}
	for i, status := range []string{"pending", "sending", "sent", "failed"} {
		notification, err := tracker.GetWebhookNotification(int64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
		if notification.DeliveryID == "" || ids[notification.DeliveryID] || notification.Status != status || notification.PayloadJSON != "{}" {
			t.Fatalf("migrated notification = %#v", notification)
		}
		ids[notification.DeliveryID] = true
	}
	if exists, err := tableExists(tracker.db, "ticket_requests"); err != nil || !exists {
		t.Fatalf("ticket_requests exists = %v, err = %v", exists, err)
	}
}
