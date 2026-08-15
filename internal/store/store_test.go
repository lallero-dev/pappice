package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"pappice/internal/security"
)

func TestStoreCreateUpdateCommentAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracker.json")
	tracker, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{
		DisplayName: "Alice Admin",
		Email:       "admin@example.test",
		Password:    "correct horse",
	})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	products := mustListProducts(t, tracker, admin)
	if len(products) != 1 {
		t.Fatalf("products = %d, want 1", len(products))
	}

	ticket, err := tracker.CreateTicket(CreateTicket{
		ProductID:   products[0].ID,
		Title:       "Cannot import invoice",
		Priority:    "urgent",
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if ticket.ID != 1 {
		t.Fatalf("ticket ID = %d, want 1", ticket.ID)
	}
	if ticket.ProductKey != products[0].Key || ticket.ProductName != products[0].Name {
		t.Fatalf("ticket product labels = key %q name %q", ticket.ProductKey, ticket.ProductName)
	}
	if ticket.RequesterUserID != admin.ID || ticket.CreatedByUserID != admin.ID ||
		ticket.RequesterName != "Alice Admin" || ticket.CreatedByName != "Alice Admin" {
		t.Fatalf("ticket identity = %#v", ticket)
	}
	for _, column := range []string{"requester_user_id", "created_by_user_id"} {
		if _, err := tracker.db.Exec(`UPDATE tickets SET `+column+` = NULL WHERE id = ?`, ticket.ID); err == nil {
			t.Fatalf("%s accepted NULL", column)
		}
	}
	byKey, err := tracker.GetTicketByKey(ticket.Key)
	if err != nil {
		t.Fatalf("get ticket by key: %v", err)
	}
	if byKey.ID != ticket.ID {
		t.Fatalf("ticket by key ID = %d, want %d", byKey.ID, ticket.ID)
	}
	byLowerKey, err := tracker.GetTicketByKey("pme-1")
	if err != nil {
		t.Fatalf("get ticket by lowercase key: %v", err)
	}
	if byLowerKey.ID != ticket.ID {
		t.Fatalf("ticket by lowercase key ID = %d, want %d", byLowerKey.ID, ticket.ID)
	}
	readAt := time.Now().UTC()
	if err := tracker.MarkTicketRead(ticket.ID, admin.ID, readAt); err != nil {
		t.Fatalf("mark ticket read: %v", err)
	}
	summary, err := tracker.TicketSummaryForUser(admin, ticket.ID)
	if err != nil {
		t.Fatalf("ticket summary: %v", err)
	}
	if summary.LastReadAt == nil {
		t.Fatal("ticket summary is missing read time")
	}
	if got := *summary.LastReadAt; got.IsZero() || got.Sub(readAt).Abs() > time.Second {
		t.Fatalf("read time = %v, want near %v", got, readAt)
	}

	status := "closed"
	assigneeUserID := admin.ID
	updatedResult, err := tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Patch: UpdateTicket{
			Status:         &status,
			AssigneeUserID: &assigneeUserID,
		},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("update ticket: %v", err)
	}
	if updated := updatedResult.Ticket; updated.Status != "closed" || updated.ClosedAt == nil || updated.AssigneeUserID != admin.ID || updated.AssigneeEmail != admin.Email {
		t.Fatalf("updated ticket = %#v", updated)
	}
	if got := updatedResult.Ticket.StatusChanges; len(got) != 1 ||
		got[0].ActorUserID != admin.ID || got[0].ActorName != "Alice Admin" ||
		got[0].PreviousStatus != "open" || got[0].CurrentStatus != "closed" {
		t.Fatalf("status changes = %#v", got)
	}
	sameStatus, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       UpdateTicket{Status: &status},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("save unchanged status: %v", err)
	}
	if len(sameStatus.Ticket.StatusChanges) != 1 {
		t.Fatalf("unchanged status changes = %#v, want one", sameStatus.Ticket.StatusChanges)
	}
	summary, err = tracker.TicketSummaryForUser(admin, ticket.ID)
	if err != nil || summary.LastReadAt == nil || !summary.LastReadAt.After(readAt) {
		t.Fatalf("read time after save = %v err=%v, want after %v", summary.LastReadAt, err, readAt)
	}
	unassignedUserID := int64(0)
	unassigned, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       UpdateTicket{AssigneeUserID: &unassignedUserID},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("unassign ticket: %v", err)
	}
	if !unassigned.AssignmentChanged || unassigned.Ticket.AssigneeUserID != 0 || unassigned.Ticket.AssigneeEmail != "" {
		t.Fatalf("unassigned ticket = %#v", unassigned)
	}

	commented, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Comment:     &AddComment{Body: "Reproduced on Linux."},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("add comment: %v", err)
	}
	withComment := commented.Ticket
	if got := len(withComment.Comments); got != 1 {
		t.Fatalf("comments = %d, want 1", got)
	}
	if withComment.Comments[0].Author != "Alice Admin" {
		t.Fatalf("email comment author = %q, want display name", withComment.Comments[0].Author)
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	page, err := reloaded.ListTicketSummariesPage(admin, TicketSummaryFilter{})
	if err != nil {
		t.Fatalf("list ticket summaries: %v", err)
	}
	if len(page.Tickets) != 1 {
		t.Fatalf("ticket summaries = %d, want 1", len(page.Tickets))
	}
	if page.Tickets[0].RequesterName != "Alice Admin" {
		t.Fatalf("summary requester name = %q, want display name", page.Tickets[0].RequesterName)
	}
	reloadedTicket, err := reloaded.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("get reloaded ticket: %v", err)
	}
	if reloadedTicket.Status != "open" || reloadedTicket.ClosedAt != nil ||
		len(reloadedTicket.Comments) != 1 || reloadedTicket.Comments[0].Author != "Alice Admin" ||
		len(reloadedTicket.StatusChanges) != 2 || reloadedTicket.StatusChanges[1].CurrentStatus != "open" ||
		reloadedTicket.StatusChanges[1].ActorName != "Alice Admin" {
		t.Fatalf("reloaded ticket history = %#v", reloadedTicket)
	}
}

func TestClosedTicketReplyLifecycle(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	ticket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Lifecycle", ActorUserID: admin.ID})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	closed := "closed"
	result, err := tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID, ActorUserID: admin.ID, Patch: UpdateTicket{Status: &closed},
	})
	if err != nil {
		t.Fatalf("close ticket: %v", err)
	}
	if result.Ticket.Status != "closed" || result.Ticket.ClosedAt == nil {
		t.Fatalf("closed ticket = %#v", result.Ticket)
	}
	result, err = tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID, ActorUserID: admin.ID, Comment: &AddComment{Body: "Private", Visibility: "internal"},
	})
	if err != nil {
		t.Fatalf("add internal note: %v", err)
	}
	if result.Ticket.Status != "closed" || len(result.Ticket.StatusChanges) != 1 {
		t.Fatalf("internal note changed lifecycle: %#v", result.Ticket)
	}
	result, err = tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID, ActorUserID: admin.ID, Patch: UpdateTicket{Status: &closed},
		Comment: &AddComment{Body: "One more thing", Visibility: "public"},
	})
	if err != nil {
		t.Fatalf("add public reply: %v", err)
	}
	if !result.HasPatch || !result.PublicComment || result.Ticket.Status != "open" || result.Ticket.ClosedAt != nil ||
		len(result.Ticket.StatusChanges) != 2 || result.Ticket.StatusChanges[1].CurrentStatus != "open" {
		t.Fatalf("public reply did not reopen ticket: %#v", result)
	}
}

func TestStoreRejectsMalformedTimestamps(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := tracker.db.Exec(`UPDATE users SET created_at = 'invalid' WHERE id = ?`, admin.ID); err != nil {
		t.Fatalf("corrupt timestamp: %v", err)
	}
	if _, err := tracker.GetUser(admin.ID); err == nil || !strings.Contains(err.Error(), "invalid database timestamp") {
		t.Fatalf("get user error = %v, want invalid database timestamp", err)
	}
}

func TestStoreSurfacesAuthenticationQueryErrors(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	session, _, _, err := tracker.CreateSession(admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, token, err := tracker.CreateAPIToken(admin.ID, CreateAPIToken{Name: "test"})
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := tracker.SetupRequired(); err == nil {
		t.Fatal("setup lookup on closed store returned no error")
	}
	if _, _, err := tracker.UserBySession(session); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("session lookup error = %v, want database error", err)
	}
	if _, err := tracker.UserByAPIToken(token); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("API token lookup error = %v, want database error", err)
	}
}

func TestStoreValidation(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	if _, err := tracker.CreateUser(CreateUser{Password: "correct horse"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing user email error = %v, want ErrValidation", err)
	}
	if _, _, _, err := tracker.CreateUserWithSetupLink(CreateUser{Role: "customer"}, time.Hour); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing setup email error = %v, want ErrValidation", err)
	}

	actorUserID := admin.ID
	_, err = tracker.CreateTicket(CreateTicket{ProductID: productID, Priority: "normal", ActorUserID: actorUserID})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("empty title error = %v, want ErrValidation", err)
	}

	ticket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Bad status", ActorUserID: actorUserID})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	status := "triaged"
	_, err = tracker.SaveTicket(SaveTicketInput{TicketID: ticket.ID, Patch: UpdateTicket{Status: &status}, ActorUserID: actorUserID})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("bad status error = %v, want ErrValidation", err)
	}

	wantStatuses := []string{"open", "closed"}
	if got := Statuses(); !slices.Equal(got, wantStatuses) {
		t.Fatalf("statuses = %#v, want %#v", got, wantStatuses)
	}

	status = "closed"
	closedResult, err := tracker.SaveTicket(SaveTicketInput{TicketID: ticket.ID, Patch: UpdateTicket{Status: &status}, ActorUserID: actorUserID})
	if err != nil {
		t.Fatalf("close ticket: %v", err)
	}
	if closed := closedResult.Ticket; closed.Status != "closed" || closed.ClosedAt == nil {
		t.Fatalf("closed ticket = %#v, want closed_at", closed)
	}
}

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
	if got, want := migrationNames(status.Pending), []string{"baseline_schema", "rename_product_roles", "normalize_relational_data", "require_ticket_participants", "ticket_status_history", "schedule_domain_event_retries", "simplify_ticket_statuses"}; status.CurrentVersion != 0 || !slices.Equal(got, want) {
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
	if got, want := migrationNames(status.Pending), []string{"rename_product_roles", "normalize_relational_data", "require_ticket_participants", "ticket_status_history", "schedule_domain_event_retries", "simplify_ticket_statuses"}; status.CurrentVersion != 1 || !slices.Equal(got, want) {
		t.Fatalf("before migration status = %#v", status)
	}
	result, err := Migrate(path, MigrationOptions{})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got, want := migrationNames(result.Applied), []string{"rename_product_roles", "normalize_relational_data", "require_ticket_participants", "ticket_status_history", "schedule_domain_event_retries", "simplify_ticket_statuses"}; !slices.Equal(got, want) {
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
	if got, want := migrationNames(result.Applied), []string{"normalize_relational_data", "require_ticket_participants", "ticket_status_history", "schedule_domain_event_retries", "simplify_ticket_statuses"}; !slices.Equal(got, want) {
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
	tracker, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
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
	tracker, err = Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer tracker.Close()
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
	tracker, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
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
	tracker, err = Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer tracker.Close()
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

func TestMetadataAndPublicViews(t *testing.T) {
	if got := truncateString("èèè", 5); got != "èè" {
		t.Fatalf("UTF-8 truncation = %q, want %q", got, "èè")
	}
	if got, want := Priorities(), []string{"low", "normal", "high", "urgent"}; !slices.Equal(got, want) {
		t.Fatalf("priorities = %#v, want %#v", got, want)
	}
	if got, want := Roles(), []string{"admin", "staff", "customer"}; !slices.Equal(got, want) {
		t.Fatalf("roles = %#v, want %#v", got, want)
	}
	if got, want := ProductRoles(), []string{"manager", "staff", "customer", "viewer"}; !slices.Equal(got, want) {
		t.Fatalf("product roles = %#v, want %#v", got, want)
	}
	if got, want := Events(), []string{"ticket.created", "ticket.updated", "ticket.commented", "ticket.assigned"}; !slices.Equal(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	mutatedStatuses := Statuses()
	mutatedStatuses[0] = "mutated"
	if got := Statuses()[0]; got != "open" {
		t.Fatalf("statuses leaked mutable backing array, first status = %q", got)
	}

	publicUser := ToPublicUser(User{ID: 7, DisplayName: "Bob", Email: "bob@example.test", Role: "staff", Disabled: true})
	if publicUser.Role != "staff" || publicUser.Email != "bob@example.test" || !publicUser.Disabled {
		t.Fatalf("public user = %#v", publicUser)
	}
	productID := int64(3)
	hook := Webhook{ID: 9, ProductID: &productID, Name: "hook", URL: "https://example.test/hook", Secret: "secret", Events: []string{"ticket.created"}}
	publicHook := ToPublicWebhook(hook)
	hook.Events[0] = "ticket.updated"
	if !publicHook.HasSecret || publicHook.Events[0] != "ticket.created" || publicHook.ProductID == nil || *publicHook.ProductID != productID {
		t.Fatalf("public hook = %#v", publicHook)
	}
}

func TestSaveTicketIsTransactional(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	actorUserID := admin.ID
	ticket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Original", Priority: "normal", ActorUserID: actorUserID})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	title := "Changed"
	status := "closed"
	_, err = tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       UpdateTicket{Title: &title, Status: &status},
		Comment:     &AddComment{Body: "This should not be stored", Visibility: "private"},
		ActorUserID: actorUserID,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("save ticket error = %v, want ErrValidation", err)
	}
	unchanged, err := tracker.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("get ticket after failed save: %v", err)
	}
	if unchanged.Title != "Original" || unchanged.Status != "open" || len(unchanged.Comments) != 0 || len(unchanged.StatusChanges) != 0 {
		t.Fatalf("failed save was not rolled back: %#v", unchanged)
	}

	comment := AddComment{Body: "Closing this ticket", Visibility: "public"}
	assigneeUserID := admin.ID
	saved, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       UpdateTicket{Status: &status, AssigneeUserID: &assigneeUserID},
		Comment:     &comment,
		ActorUserID: actorUserID,
	})
	if err != nil {
		t.Fatalf("save ticket: %v", err)
	}
	if !saved.HasPatch || !saved.HasComment || !saved.PublicComment || !saved.AssignmentChanged {
		t.Fatalf("save metadata = %#v", saved)
	}
	if saved.Previous.Status != "open" || saved.Ticket.Status != "closed" ||
		len(saved.Ticket.Comments) != 1 || len(saved.Ticket.StatusChanges) != 1 {
		t.Fatalf("saved ticket = %#v", saved)
	}
}

func TestTicketMutationsWriteDomainEventsTransactionally(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{DisplayName: "Alice Admin", Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	ticket, err := tracker.CreateTicket(CreateTicket{
		ProductID:   productID,
		Title:       "Evented ticket",
		Priority:    "normal",
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	events := mustListDomainEvents(t, tracker, 10)
	if len(events) != 1 || events[0].Type != "ticket.created" || events[0].TicketID != ticket.ID || events[0].ActorEmail != "admin@example.test" {
		t.Fatalf("created events = %#v", events)
	}

	status := "closed"
	assigneeUserID := admin.ID
	_, err = tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       UpdateTicket{Status: &status, AssigneeUserID: &assigneeUserID},
		Comment:     &AddComment{Body: "This should not be stored", Visibility: "private"},
		ActorUserID: admin.ID,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("save ticket error = %v, want ErrValidation", err)
	}
	if events := mustListDomainEvents(t, tracker, 10); len(events) != 1 {
		t.Fatalf("failed save wrote events: %#v", events)
	}

	_, err = tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       UpdateTicket{Status: &status, AssigneeUserID: &assigneeUserID},
		Comment:     &AddComment{Body: "Taking this now", Visibility: "public"},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("save ticket: %v", err)
	}
	claimed, err := tracker.ClaimDomainEvents(10, time.Minute)
	if err != nil {
		t.Fatalf("claim domain events: %v", err)
	}
	gotTypes := make([]string, 0, len(claimed))
	for _, event := range claimed {
		gotTypes = append(gotTypes, event.Type)
	}
	wantTypes := []string{"ticket.created", "ticket.updated", "ticket.assigned", "ticket.commented"}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Fatalf("claimed event types = %#v, want %#v", gotTypes, wantTypes)
	}
	var payload TicketEventPayload
	if err := json.Unmarshal([]byte(claimed[1].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if !payload.HasPatch || !payload.PublicComment || !payload.AssignmentChanged || payload.PreviousStatus != "open" || payload.CurrentStatus != "closed" {
		t.Fatalf("event payload = %#v", payload)
	}
	if err := tracker.ApplyDomainEventProjection(claimed[0].ID, DomainEventProjection{}); err != nil {
		t.Fatalf("mark processed: %v", err)
	}
	processed, err := tracker.GetDomainEvent(claimed[0].ID)
	if err != nil {
		t.Fatalf("get processed event: %v", err)
	}
	if processed.Status != "processed" || processed.ProcessedAt == nil || processed.LockedUntil != nil {
		t.Fatalf("processed event = %#v", processed)
	}
	pruned, err := tracker.PruneProcessedDomainEvents(time.Now().UTC().Add(time.Second), 100)
	if err != nil {
		t.Fatalf("prune processed domain events: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned events = %d, want 1", pruned)
	}
	if _, err := tracker.GetDomainEvent(claimed[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned event error = %v, want ErrNotFound", err)
	}
	if events := mustListDomainEvents(t, tracker, 10); len(events) != 3 {
		t.Fatalf("prune removed non-processed events: %#v", events)
	}
}

func TestDomainEventFailuresAreDeferredAndBounded(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	failed, err := tracker.CreateDomainEvent(CreateDomainEvent{Type: "user.updated"})
	if err != nil {
		t.Fatalf("create failing event: %v", err)
	}
	claimed, err := tracker.ClaimDomainEvents(1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failed.ID {
		t.Fatalf("claimed failing event = %#v err=%v", claimed, err)
	}
	if err := tracker.MarkDomainEventFailed(failed.ID, errors.New("projection failed"), 2); err != nil {
		t.Fatalf("defer failing event: %v", err)
	}
	deferred, err := tracker.GetDomainEvent(failed.ID)
	if err != nil {
		t.Fatalf("get deferred event: %v", err)
	}
	if deferred.Status != "pending" || deferred.Attempts != 1 || !deferred.NextAttemptAt.After(time.Now().UTC()) {
		t.Fatalf("deferred event = %#v", deferred)
	}

	fresh, err := tracker.CreateDomainEvent(CreateDomainEvent{Type: "user.updated"})
	if err != nil {
		t.Fatalf("create fresh event: %v", err)
	}
	claimed, err = tracker.ClaimDomainEvents(1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != fresh.ID {
		t.Fatalf("claimed fresh event = %#v err=%v", claimed, err)
	}
	if err := tracker.ApplyDomainEventProjection(fresh.ID, DomainEventProjection{}); err != nil {
		t.Fatalf("process fresh event: %v", err)
	}
	if _, err := tracker.db.Exec(`UPDATE domain_events SET next_attempt_at = ? WHERE id = ?`, formatTime(time.Now().UTC().Add(-time.Second)), failed.ID); err != nil {
		t.Fatalf("make deferred event due: %v", err)
	}
	claimed, err = tracker.ClaimDomainEvents(1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failed.ID {
		t.Fatalf("reclaimed failing event = %#v err=%v", claimed, err)
	}
	if err := tracker.MarkDomainEventFailed(failed.ID, errors.New("still broken"), 2); err != nil {
		t.Fatalf("fail event permanently: %v", err)
	}
	terminal, err := tracker.GetDomainEvent(failed.ID)
	if err != nil {
		t.Fatalf("get terminal event: %v", err)
	}
	if terminal.Status != "failed" || terminal.Attempts != 2 || terminal.LastError != "still broken" {
		t.Fatalf("terminal event = %#v", terminal)
	}
	claimed, err = tracker.ClaimDomainEvents(1, time.Minute)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claimed terminal event = %#v err=%v", claimed, err)
	}
}

func TestApplyDomainEventProjectionIsTransactional(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{DisplayName: "Alice Admin", Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	hook, err := tracker.CreateWebhook(CreateWebhook{
		ProductID: &productID,
		Name:      "Ticket receiver",
		URL:       "https://hooks.example.test/tickets",
		Events:    []string{"ticket.created"},
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	event, err := tracker.CreateDomainEvent(CreateDomainEvent{
		Type:      "ticket.created",
		ProductID: productID,
		TicketID:  77,
		Actor:     EventActorFromUser(admin),
	})
	if err != nil {
		t.Fatalf("create domain event: %v", err)
	}
	projection := DomainEventProjection{
		Audit: &CreateAuditEvent{
			ActorUserID: admin.ID,
			ActorEmail:  admin.Email,
			Action:      "ticket.created",
			TargetType:  "ticket",
			TargetID:    77,
			TargetName:  "PME-77",
			DetailsJSON: `{"kind":"test"}`,
		},
		EmailNotifications: []CreateEmailNotification{{
			ProductID:      productID,
			RecipientEmail: "staff@example.test",
			RecipientName:  "Staff",
			Event:          "ticket.created",
			Subject:        "Ticket created",
			BodyText:       "A ticket was created.",
			SendAfter:      time.Now().UTC().Add(-time.Second),
		}},
		WebhookNotifications: []CreateWebhookNotification{{
			WebhookID:   hook.ID,
			ProductID:   &productID,
			Event:       "ticket.created",
			PayloadJSON: `{"event":"ticket.created"}`,
			SendAfter:   time.Now().UTC().Add(-time.Second),
		}},
	}
	if err := tracker.ApplyDomainEventProjection(event.ID, projection); err != nil {
		t.Fatalf("apply projection: %v", err)
	}
	processed, err := tracker.GetDomainEvent(event.ID)
	if err != nil {
		t.Fatalf("get processed event: %v", err)
	}
	if processed.Status != "processed" || processed.ProcessedAt == nil {
		t.Fatalf("processed event = %#v", processed)
	}
	audits := mustListAuditEvents(t, tracker, 10)
	if len(audits) != 1 || audits[0].DomainEventID != event.ID || audits[0].DetailsJSON != `{"kind":"test"}` {
		t.Fatalf("audits = %#v", audits)
	}
	emails := mustListEmailNotifications(t, tracker, 10)
	if len(emails) != 1 || emails[0].Event != "ticket.created" {
		t.Fatalf("emails = %#v", emails)
	}
	webhooks, err := tracker.ClaimWebhookNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim webhooks: %v", err)
	}
	if len(webhooks) != 1 || webhooks[0].WebhookID != hook.ID {
		t.Fatalf("webhooks = %#v", webhooks)
	}
	if err := tracker.ApplyDomainEventProjection(event.ID, projection); err != nil {
		t.Fatalf("reapply processed projection: %v", err)
	}
	if audits := mustListAuditEvents(t, tracker, 10); len(audits) != 1 {
		t.Fatalf("reapply duplicated audit events: %#v", audits)
	}
	if emails := mustListEmailNotifications(t, tracker, 10); len(emails) != 1 {
		t.Fatalf("reapply duplicated email notifications: %#v", emails)
	}
	if webhooks, err := tracker.ClaimWebhookNotifications(10, time.Minute); err != nil || len(webhooks) != 0 {
		t.Fatalf("reapply duplicated webhook notifications: %#v err=%v", webhooks, err)
	}

	failed, err := tracker.CreateDomainEvent(CreateDomainEvent{Type: "ticket.created", ProductID: productID, Actor: EventActorFromUser(admin)})
	if err != nil {
		t.Fatalf("create failed projection event: %v", err)
	}
	err = tracker.ApplyDomainEventProjection(failed.ID, DomainEventProjection{
		Audit: &CreateAuditEvent{
			ActorUserID: admin.ID,
			ActorEmail:  admin.Email,
			Action:      "ticket.created",
			TargetType:  "ticket",
			TargetName:  "bad projection",
		},
		EmailNotifications: []CreateEmailNotification{{
			RecipientEmail: "staff@example.test",
			Event:          "ticket.created",
			BodyText:       "Missing subject should fail.",
		}},
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("bad projection error = %v, want ErrValidation", err)
	}
	unprocessed, err := tracker.GetDomainEvent(failed.ID)
	if err != nil {
		t.Fatalf("get failed projection event: %v", err)
	}
	if unprocessed.Status == "processed" {
		t.Fatalf("failed projection was processed: %#v", unprocessed)
	}
	if audits := mustListAuditEvents(t, tracker, 10); len(audits) != 1 {
		t.Fatalf("failed projection left audit row: %#v", audits)
	}
}

func TestAppMutationsWriteDomainEventsTransactionally(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{DisplayName: "Alice Admin", Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	ctx := EventContext{Enabled: true, Actor: EventActorFromUser(admin), IP: "127.0.0.1"}
	product, err := tracker.CreateProduct(CreateProduct{Key: "OPS", Name: "Operations", Event: ctx})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	events := mustListDomainEvents(t, tracker, 10)
	if len(events) != 1 || events[0].Type != "product.created" || events[0].ActorEmail != "admin@example.test" {
		t.Fatalf("product events = %#v", events)
	}
	var payload AppEventPayload
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode app payload: %v", err)
	}
	if payload.TargetType != "product" || payload.TargetID != product.ID || payload.TargetName != product.Key || payload.IP != "127.0.0.1" {
		t.Fatalf("app payload = %#v", payload)
	}

	if _, err := tracker.CreateProduct(CreateProduct{Key: "OPS", Name: "Duplicate", Event: ctx}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate product error = %v, want ErrConflict", err)
	}
	if events := mustListDomainEvents(t, tracker, 10); len(events) != 1 {
		t.Fatalf("failed product write created events: %#v", events)
	}

	user, link, token, err := tracker.CreateUserWithSetupLink(CreateUser{
		Email: "pending@example.test",
		Role:  "staff",
		Event: ctx,
	}, time.Hour)
	if err != nil {
		t.Fatalf("create setup link user: %v", err)
	}
	_ = link
	events = mustListDomainEvents(t, tracker, 10)
	if len(events) != 2 || events[0].Type != "user.created" {
		t.Fatalf("user events = %#v", events)
	}
	payload = AppEventPayload{}
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode user payload: %v", err)
	}
	if payload.TargetID != user.ID || payload.AccountLink == nil || payload.AccountLink.UserID != user.ID || payload.AccountLink.Token != token {
		t.Fatalf("user payload = %#v", payload)
	}
}

func TestTicketAttachmentsHydrateWithTicketAndComments(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID

	ticket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:   productID,
		Title:       "Attached ticket",
		ActorUserID: admin.ID,
	}, []CreateAttachment{{
		Filename:    "request.txt",
		ContentType: "text/plain",
		SizeBytes:   12,
		SHA256:      "ticket-hash",
		StorageKey:  "ti/ck/ticket-hash",
	}})
	if err != nil {
		t.Fatalf("create ticket with attachment: %v", err)
	}
	if len(ticket.Attachments) != 1 || ticket.Attachments[0].Filename != "request.txt" || ticket.Attachments[0].CreatedByUserID != admin.ID {
		t.Fatalf("ticket attachments = %#v", ticket.Attachments)
	}

	saved, err := tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Comment: &AddComment{
			Visibility: "internal",
		},
		Attachments: []CreateAttachment{{
			Filename:    "diagnosis.txt",
			ContentType: "text/plain",
			SizeBytes:   10,
			SHA256:      "comment-hash",
			StorageKey:  "co/mm/comment-hash",
		}},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("save file-only comment: %v", err)
	}
	if !saved.HasComment || saved.CommentID == 0 || len(saved.Ticket.Comments) != 1 {
		t.Fatalf("save result = %#v", saved)
	}
	comment := saved.Ticket.Comments[0]
	if comment.Body != "" || comment.Visibility != "internal" || len(comment.Attachments) != 1 || comment.Attachments[0].Filename != "diagnosis.txt" {
		t.Fatalf("comment attachments = %#v", comment)
	}
	attachment, err := tracker.GetAttachment(comment.Attachments[0].ID)
	if err != nil {
		t.Fatalf("get attachment: %v", err)
	}
	if attachment.TicketID != ticket.ID || attachment.CommentID == nil || *attachment.CommentID != saved.CommentID {
		t.Fatalf("attachment = %#v", attachment)
	}
}

func TestDeleteTicketCascadesAndReportsOrphanedStorageKeys(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	shared := CreateAttachment{
		Filename:    "shared.txt",
		ContentType: "text/plain",
		SizeBytes:   6,
		SHA256:      "shared-hash",
		StorageKey:  "sh/ar/shared-hash",
	}
	ticket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:   productID,
		Title:       "Delete me",
		ActorUserID: admin.ID,
	}, []CreateAttachment{shared})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	other, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:   productID,
		Title:       "Keep me",
		ActorUserID: admin.ID,
	}, []CreateAttachment{shared})
	if err != nil {
		t.Fatalf("create other ticket: %v", err)
	}
	saved, err := tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Comment: &AddComment{
			Visibility: "public",
		},
		Attachments: []CreateAttachment{{
			Filename:    "orphan.txt",
			ContentType: "text/plain",
			SizeBytes:   6,
			SHA256:      "orphan-hash",
			StorageKey:  "or/ph/orphan-hash",
		}},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("save comment attachment: %v", err)
	}
	commentAttachmentID := saved.Ticket.Comments[0].Attachments[0].ID

	orphaned, err := tracker.DeleteTicket(ticket.ID, EventContext{})
	if err != nil {
		t.Fatalf("delete ticket: %v", err)
	}
	if slices.Contains(orphaned, shared.StorageKey) || !slices.Contains(orphaned, "or/ph/orphan-hash") {
		t.Fatalf("orphaned storage keys = %#v", orphaned)
	}
	if _, err := tracker.GetTicket(ticket.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted ticket err = %v, want not found", err)
	}
	if _, err := tracker.GetAttachment(commentAttachmentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted attachment err = %v, want not found", err)
	}
	kept, err := tracker.GetTicket(other.ID)
	if err != nil {
		t.Fatalf("get kept ticket: %v", err)
	}
	if len(kept.Attachments) != 1 || kept.Attachments[0].StorageKey != shared.StorageKey {
		t.Fatalf("kept ticket attachments = %#v", kept.Attachments)
	}
	if _, err := tracker.DeleteTicket(ticket.ID, EventContext{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing ticket err = %v, want not found", err)
	}
}

func TestDeleteProductCascadesAndReportsOrphanedStorageKeys(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	deleteProductID := mustListProducts(t, tracker, admin)[0].ID
	keepProduct, err := tracker.CreateProduct(CreateProduct{Key: "KEEP", Name: "Keep"})
	if err != nil {
		t.Fatalf("create keep product: %v", err)
	}
	shared := CreateAttachment{
		Filename:    "shared.txt",
		ContentType: "text/plain",
		SizeBytes:   6,
		SHA256:      "shared-hash",
		StorageKey:  "sh/ar/shared-hash",
	}
	orphan := CreateAttachment{
		Filename:    "orphan.txt",
		ContentType: "text/plain",
		SizeBytes:   6,
		SHA256:      "orphan-hash",
		StorageKey:  "or/ph/orphan-hash",
	}
	deletedTicket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:   deleteProductID,
		Title:       "Delete product ticket",
		ActorUserID: admin.ID,
	}, []CreateAttachment{shared, orphan})
	if err != nil {
		t.Fatalf("create deleted product ticket: %v", err)
	}
	keptTicket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:   keepProduct.ID,
		Title:       "Keep product ticket",
		ActorUserID: admin.ID,
	}, []CreateAttachment{shared})
	if err != nil {
		t.Fatalf("create kept product ticket: %v", err)
	}

	orphaned, err := tracker.DeleteProduct(deleteProductID, EventContext{})
	if err != nil {
		t.Fatalf("delete product: %v", err)
	}
	if slices.Contains(orphaned, shared.StorageKey) || !slices.Contains(orphaned, orphan.StorageKey) {
		t.Fatalf("orphaned storage keys = %#v", orphaned)
	}
	if _, err := tracker.GetTicket(deletedTicket.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted product ticket err = %v, want not found", err)
	}
	kept, err := tracker.GetTicket(keptTicket.ID)
	if err != nil {
		t.Fatalf("get kept ticket: %v", err)
	}
	if len(kept.Attachments) != 1 || kept.Attachments[0].StorageKey != shared.StorageKey {
		t.Fatalf("kept ticket attachments = %#v", kept.Attachments)
	}
}

func TestUsersSessionsTokensAndWebhooks(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if required, err := tracker.SetupRequired(); err != nil || !required {
		t.Fatalf("new store setup required = %v err=%v", required, err)
	}

	admin, err := tracker.CreateFirstAdmin(CreateUser{
		Email:    "admin@example.test",
		Password: "correct horse",
	})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	if required, err := tracker.SetupRequired(); err != nil || required {
		t.Fatalf("setup required after first admin = %v err=%v", required, err)
	}

	authenticated, err := tracker.Authenticate("admin@example.test", "correct horse")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if authenticated.PasswordHash != "" {
		t.Fatal("authenticated user leaked password hash")
	}

	legacyUser, err := tracker.CreateUser(CreateUser{Email: "legacy@example.test", Password: "legacy horse"})
	if err != nil {
		t.Fatalf("create legacy hash user: %v", err)
	}
	legacyHash := "pbkdf2-sha256$60000$MDEyMzQ1Njc4OWFiY2RlZg$R5q4Ncg29rBEEUjjeFQAxCjvocVZrvSI1dBok5+gOyk"
	if _, err := tracker.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, legacyHash, legacyUser.ID); err != nil {
		t.Fatalf("install legacy hash: %v", err)
	}
	if _, err := tracker.Authenticate("legacy@example.test", "legacy horse"); err != nil {
		t.Fatalf("authenticate legacy hash: %v", err)
	}
	upgraded, err := tracker.GetUser(legacyUser.ID)
	if err != nil {
		t.Fatalf("get upgraded legacy user: %v", err)
	}
	if upgraded.PasswordHash == legacyHash || !strings.Contains(upgraded.PasswordHash, "$120000$") {
		t.Fatalf("legacy hash was not upgraded: %q", upgraded.PasswordHash)
	}

	session, csrf, _, err := tracker.CreateSession(admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if csrf == "" {
		t.Fatal("session should include csrf token")
	}
	if user, gotCSRF, err := tracker.UserBySession(session); err != nil || user.ID != admin.ID || gotCSRF != csrf {
		t.Fatalf("session user = %#v err=%v", user, err)
	}
	if err := tracker.DeleteSession(session); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, _, err := tracker.UserBySession(session); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session error = %v, want ErrNotFound", err)
	}
	shortSession, _, expires, err := tracker.CreateSessionFor(admin.ID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("create short session: %v", err)
	}
	if time.Until(expires) > time.Second {
		t.Fatalf("short session expires too late: %s", expires)
	}
	if _, err := tracker.db.Exec(`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, formatTime(time.Now().UTC().Add(-time.Hour)), security.HashToken(shortSession)); err != nil {
		t.Fatalf("expire short session: %v", err)
	}
	if _, _, err := tracker.UserBySession(shortSession); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session error = %v, want ErrNotFound", err)
	}

	token, raw, err := tracker.CreateAPIToken(admin.ID, CreateAPIToken{Name: "cli"})
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if token.Prefix == "" || raw == "" {
		t.Fatalf("token = %#v raw=%q", token, raw)
	}
	if user, err := tracker.UserByAPIToken(raw); err != nil || user.ID != admin.ID {
		t.Fatalf("API token user = %#v err=%v", user, err)
	}
	tokens := mustListAPITokens(t, tracker, admin.ID)
	if len(tokens) != 1 || tokens[0].ID != token.ID || tokens[0].Prefix != token.Prefix {
		t.Fatalf("API tokens = %#v", tokens)
	}
	if err := tracker.DeleteAPIToken(admin.ID, token.ID+100, EventContext{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing API token error = %v, want ErrNotFound", err)
	}
	if err := tracker.DeleteAPIToken(admin.ID, token.ID, EventContext{}); err != nil {
		t.Fatalf("delete API token: %v", err)
	}
	if tokens := mustListAPITokens(t, tracker, admin.ID); len(tokens) != 0 {
		t.Fatalf("API tokens after delete = %#v", tokens)
	}
	if user, err := tracker.UserByAPIToken(raw); !errors.Is(err, ErrNotFound) || user.ID != 0 {
		t.Fatalf("deleted API token user = %#v err=%v", user, err)
	}

	enabled := true
	hook, err := tracker.CreateWebhook(CreateWebhook{
		Name:    "local",
		URL:     "http://127.0.0.1/hook",
		Events:  []string{"ticket.created"},
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	hooks, err := tracker.ListWebhooksForEvent("ticket.created", productID)
	if err != nil {
		t.Fatalf("list event hooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("event hooks = %#v", hooks)
	}
}

func TestAccountLinksAndPasswordResetLifecycle(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	_ = admin

	user, link, setupToken, err := tracker.CreateUserWithSetupLink(CreateUser{
		Email: "pending@example.test",
		Role:  "staff",
	}, time.Hour)
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}
	if !user.PasswordResetRequired || link.Purpose != "setup" || setupToken == "" {
		t.Fatalf("pending account = %#v link=%#v token=%q", user, link, setupToken)
	}
	if _, err := tracker.Authenticate("pending@example.test", "correct horse"); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("pending authenticate error = %v, want ErrPasswordResetRequired", err)
	}
	if _, _, _, err := tracker.CreateSession(user.ID); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("pending create session error = %v, want ErrPasswordResetRequired", err)
	}

	foundLink, foundUser, err := tracker.GetAccountLink(setupToken)
	if err != nil {
		t.Fatalf("get setup link: %v", err)
	}
	if foundLink.ID != link.ID || foundUser.ID != user.ID || foundUser.PasswordHash != "" {
		t.Fatalf("setup link lookup = %#v user=%#v", foundLink, foundUser)
	}
	activated, err := tracker.ConsumeAccountLink(setupToken, "correct horse", EventContext{})
	if err != nil {
		t.Fatalf("consume setup link: %v", err)
	}
	if activated.PasswordResetRequired {
		t.Fatalf("activated user still requires password reset: %#v", activated)
	}
	if _, _, err := tracker.GetAccountLink(setupToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("used setup link error = %v, want ErrNotFound", err)
	}
	if _, err := tracker.Authenticate("pending@example.test", "correct horse"); err != nil {
		t.Fatalf("authenticate activated user: %v", err)
	}
	session, _, _, err := tracker.CreateSession(user.ID)
	if err != nil {
		t.Fatalf("create activated session: %v", err)
	}
	extraSession, _, _, err := tracker.CreateSession(user.ID)
	if err != nil {
		t.Fatalf("create extra activated session: %v", err)
	}
	if _, err := tracker.ChangePassword(user.ID, "wrong password", "changed password", session, EventContext{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("wrong current password error = %v, want ErrValidation", err)
	}
	changedUser, err := tracker.ChangePassword(user.ID, "correct horse", "changed password", session, EventContext{})
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if changedUser.PasswordResetRequired {
		t.Fatalf("changed user requires reset: %#v", changedUser)
	}
	if _, err := tracker.Authenticate("pending@example.test", "changed password"); err != nil {
		t.Fatalf("authenticate changed password: %v", err)
	}
	if _, _, err := tracker.UserBySession(session); err != nil {
		t.Fatalf("kept session error = %v", err)
	}
	if _, _, err := tracker.UserBySession(extraSession); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed session error = %v, want ErrNotFound", err)
	}
	resetUser, resetLink, resetToken, err := tracker.CreatePasswordResetLink(user.ID, time.Hour, EventContext{})
	if err != nil {
		t.Fatalf("create reset link: %v", err)
	}
	if !resetUser.PasswordResetRequired || resetLink.Purpose != "reset" || resetToken == "" {
		t.Fatalf("reset link = %#v user=%#v token=%q", resetLink, resetUser, resetToken)
	}
	if _, _, err := tracker.UserBySession(session); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset-invalidated session error = %v, want ErrNotFound", err)
	}
	if _, err := tracker.Authenticate("pending@example.test", "changed password"); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("reset-required authenticate error = %v, want ErrPasswordResetRequired", err)
	}
	if _, newerLink, newerToken, err := tracker.CreatePasswordResetLink(user.ID, time.Hour, EventContext{}); err != nil {
		t.Fatalf("create newer reset link: %v", err)
	} else {
		if newerLink.ID == resetLink.ID || newerToken == resetToken {
			t.Fatalf("new reset link did not rotate: %#v %q", newerLink, newerToken)
		}
		if _, _, err := tracker.GetAccountLink(resetToken); !errors.Is(err, ErrNotFound) {
			t.Fatalf("old reset link error = %v, want ErrNotFound", err)
		}
		resetToken = newerToken
	}

	resetComplete, err := tracker.ConsumeAccountLink(resetToken, "better password", EventContext{})
	if err != nil {
		t.Fatalf("consume reset link: %v", err)
	}
	if resetComplete.PasswordResetRequired {
		t.Fatalf("reset user still requires password reset: %#v", resetComplete)
	}
	if _, err := tracker.Authenticate("pending@example.test", "better password"); err != nil {
		t.Fatalf("authenticate reset user: %v", err)
	}

	_, _, expiringToken, err := tracker.CreatePasswordResetLink(user.ID, time.Nanosecond, EventContext{})
	if err != nil {
		t.Fatalf("create expiring reset link: %v", err)
	}
	if _, err := tracker.db.Exec(`UPDATE account_links SET expires_at = ? WHERE token_hash = ?`, formatTime(time.Now().UTC().Add(-time.Hour)), security.HashToken(expiringToken)); err != nil {
		t.Fatalf("expire reset link: %v", err)
	}
	if _, _, err := tracker.GetAccountLink(expiringToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired reset link error = %v, want ErrNotFound", err)
	}
}

func TestStoreAdminProductWebhookAndFailureLifecycle(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := tracker.UpdateUser(admin.ID, UpdateUser{Disabled: new(true)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("disable sole admin error = %v, want ErrValidation", err)
	}
	user, err := tracker.CreateUser(CreateUser{Password: "correct horse", Email: "bob@example.test"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	updatedUser, err := tracker.UpdateUser(user.ID, UpdateUser{
		DisplayName: new("Customer Bob"),
		Email:       new("customer-bob@example.test"),
		Role:        new("customer"),
		Password:    new("new password"),
	})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	if updatedUser.Role != "customer" || updatedUser.Email != "customer-bob@example.test" {
		t.Fatalf("updated user = %#v", updatedUser)
	}
	users := mustListUsers(t, tracker)
	if len(users) != 2 {
		t.Fatalf("users = %#v", users)
	}

	product, err := tracker.CreateProduct(CreateProduct{Key: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	product, err = tracker.UpdateProduct(product.ID, UpdateProduct{Name: new("Operations Desk")})
	if err != nil {
		t.Fatalf("update product: %v", err)
	}
	if product.Name != "Operations Desk" {
		t.Fatalf("product = %#v", product)
	}
	if _, err := tracker.UpsertProductMember(product.ID, UpsertProductMember{UserID: user.ID, Role: "customer"}); err != nil {
		t.Fatalf("upsert member: %v", err)
	}
	if role, err := tracker.ProductRole(user.ID, product.ID); err != nil || role != "customer" {
		t.Fatalf("product role = %q err=%v", role, err)
	}
	if err := tracker.DeleteProductMember(product.ID, user.ID, EventContext{}); err != nil {
		t.Fatalf("delete member: %v", err)
	}
	if _, err := tracker.ProductRole(user.ID, product.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted product member error = %v, want ErrNotFound", err)
	}

	enabled := false
	hook, err := tracker.CreateWebhook(CreateWebhook{
		ProductID: &product.ID,
		Name:      "product hook",
		URL:       "https://hooks.example.test/incoming",
		Events:    []string{"*"},
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	hooks, err := tracker.ListWebhooks(&product.ID)
	if err != nil {
		t.Fatalf("list product hooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("product hooks = %#v", hooks)
	}
	enabled = true
	hook, err = tracker.UpdateWebhook(hook.ID, UpdateWebhook{
		Name:    new("renamed hook"),
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("update webhook: %v", err)
	}
	if hook.Name != "renamed hook" || !hook.Enabled {
		t.Fatalf("updated hook = %#v", hook)
	}
	events := []string{"ticket.updated"}
	secret := "manual-secret"
	hook, err = tracker.UpdateWebhook(hook.ID, UpdateWebhook{
		URL:    new("https://hooks.example.test/renamed"),
		Secret: &secret,
		Events: &events,
	})
	if err != nil {
		t.Fatalf("update webhook details: %v", err)
	}
	if hook.URL != "https://hooks.example.test/renamed" || hook.Secret != secret || !slices.Equal(hook.Events, events) {
		t.Fatalf("updated hook details = %#v", hook)
	}
	rotated, rotatedSecret, err := tracker.RotateWebhookSecret(hook.ID, EventContext{})
	if err != nil {
		t.Fatalf("rotate webhook secret: %v", err)
	}
	if rotatedSecret == "" || rotatedSecret == secret || rotated.Secret != rotatedSecret {
		t.Fatalf("rotated hook = %#v secret=%q", rotated, rotatedSecret)
	}
	if err := tracker.RecordDelivery(WebhookDelivery{WebhookID: hook.ID, ProductID: &product.ID, Event: "ticket.created", StatusCode: 204}); err != nil {
		t.Fatalf("record delivery: %v", err)
	}
	deliveries := mustListDeliveries(t, tracker, 10)
	if len(deliveries) != 1 || deliveries[0].StatusCode != 204 {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	delayedWebhook, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{{
		WebhookID:   hook.ID,
		ProductID:   &product.ID,
		Event:       "ticket.created",
		PayloadJSON: `{"event":"ticket.created"}`,
		SendAfter:   time.Now().UTC().Add(time.Hour),
	}})
	if err != nil {
		t.Fatalf("enqueue delayed webhook notification: %v", err)
	}
	if len(delayedWebhook) != 1 || delayedWebhook[0].Status != "pending" {
		t.Fatalf("delayed webhook notification = %#v", delayedWebhook)
	}
	claimedWebhooks, err := tracker.ClaimWebhookNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim delayed webhook notification: %v", err)
	}
	if len(claimedWebhooks) != 0 {
		t.Fatalf("claimed delayed webhook too early: %#v", claimedWebhooks)
	}
	dueWebhook, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{{
		WebhookID:   hook.ID,
		ProductID:   &product.ID,
		Event:       "ticket.created",
		PayloadJSON: `{"event":"ticket.created"}`,
		SendAfter:   time.Now().UTC().Add(-time.Second),
	}})
	if err != nil {
		t.Fatalf("enqueue due webhook notification: %v", err)
	}
	claimedWebhooks, err = tracker.ClaimWebhookNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim due webhook notification: %v", err)
	}
	if len(claimedWebhooks) != 1 || claimedWebhooks[0].ID != dueWebhook[0].ID || claimedWebhooks[0].Status != "sending" || claimedWebhooks[0].Attempts != 1 {
		t.Fatalf("claimed due webhook = %#v", claimedWebhooks)
	}
	if err := tracker.MarkWebhookNotificationFailed(claimedWebhooks[0].ID, errors.New("temporary webhook failure"), 1); err != nil {
		t.Fatalf("mark webhook notification failed: %v", err)
	}
	failedWebhook, err := tracker.GetWebhookNotification(claimedWebhooks[0].ID)
	if err != nil {
		t.Fatalf("get failed webhook notification: %v", err)
	}
	if failedWebhook.Status != "failed" || failedWebhook.LastError != "temporary webhook failure" {
		t.Fatalf("failed webhook notification = %#v", failedWebhook)
	}

	ticketForWebhook, err := tracker.CreateTicket(CreateTicket{ProductID: product.ID, Title: "Coalesced webhook ticket", ActorUserID: admin.ID})
	if err != nil {
		t.Fatalf("create webhook ticket: %v", err)
	}
	firstSendAfter := time.Now().UTC().Add(30 * time.Second)
	firstWebhook, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{{
		WebhookID:   hook.ID,
		ProductID:   &product.ID,
		TicketID:    ticketForWebhook.ID,
		Event:       "ticket.updated",
		PayloadJSON: `{"version":1}`,
		SendAfter:   firstSendAfter,
		Coalesce:    true,
	}})
	if err != nil {
		t.Fatalf("enqueue first coalesced webhook: %v", err)
	}
	secondSendAfter := time.Now().UTC().Add(45 * time.Second)
	secondWebhook, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{{
		WebhookID:   hook.ID,
		ProductID:   &product.ID,
		TicketID:    ticketForWebhook.ID,
		Event:       "ticket.commented",
		PayloadJSON: `{"version":2}`,
		SendAfter:   secondSendAfter,
		Coalesce:    true,
	}})
	if err != nil {
		t.Fatalf("enqueue second coalesced webhook: %v", err)
	}
	if len(firstWebhook) != 1 || len(secondWebhook) != 1 || secondWebhook[0].ID != firstWebhook[0].ID {
		t.Fatalf("coalesced webhook IDs = first %#v second %#v", firstWebhook, secondWebhook)
	}
	coalescedWebhook, err := tracker.GetWebhookNotification(firstWebhook[0].ID)
	if err != nil {
		t.Fatalf("get coalesced webhook notification: %v", err)
	}
	if coalescedWebhook.Event != "ticket.commented" || coalescedWebhook.PayloadJSON != `{"version":2}` {
		t.Fatalf("coalesced webhook payload = %#v", coalescedWebhook)
	}
	if coalescedWebhook.NextAttemptAt.Sub(secondSendAfter).Abs() > time.Second {
		t.Fatalf("coalesced webhook next attempt = %s, want near %s", coalescedWebhook.NextAttemptAt, secondSendAfter)
	}
	claimedWebhooks, err = tracker.ClaimWebhookNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim coalesced delayed webhook: %v", err)
	}
	if len(claimedWebhooks) != 0 {
		t.Fatalf("claimed coalesced delayed webhook too early: %#v", claimedWebhooks)
	}

	queued, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		UserID:         user.ID,
		RecipientEmail: updatedUser.Email,
		RecipientName:  updatedUser.DisplayName,
		Event:          "ticket.updated",
		Subject:        "Updated",
		BodyText:       "Body",
	}})
	if err != nil {
		t.Fatalf("enqueue email: %v", err)
	}
	if err := tracker.MarkEmailFailed(queued[0].ID, errors.New("temporary failure"), 1); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	notifications := mustListEmailNotifications(t, tracker, 10)
	if len(notifications) != 1 || notifications[0].Status != "failed" || notifications[0].LastError != "temporary failure" {
		t.Fatalf("notifications = %#v", notifications)
	}
	stats, err := tracker.EmailNotificationStats()
	if err != nil {
		t.Fatalf("email notification stats: %v", err)
	}
	if stats.Total != 1 || stats.Failed != 1 || stats.LastError != "temporary failure" {
		t.Fatalf("email stats = %#v", stats)
	}
	retried, err := tracker.RetryEmailNotification(queued[0].ID, EventContext{})
	if err != nil {
		t.Fatalf("retry email: %v", err)
	}
	if retried.Status != "pending" || retried.Attempts != 0 || retried.LastError != "" {
		t.Fatalf("retried email = %#v", retried)
	}
	if err := tracker.DeleteWebhook(hook.ID, EventContext{}); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if err := tracker.DeleteUser(user.ID, EventContext{}); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := tracker.DeleteProduct(product.ID, EventContext{}); err != nil {
		t.Fatalf("delete product: %v", err)
	}
}

func TestDeleteUserPreservesTicketHistory(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	staff, err := tracker.CreateUser(CreateUser{Email: "staff@example.test", Password: "correct horse", Role: "staff"})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	statusActor, err := tracker.CreateUser(CreateUser{Email: "status-actor@example.test", Password: "correct horse", Role: "staff"})
	if err != nil {
		t.Fatalf("create status actor: %v", err)
	}
	customer, err := tracker.CreateUser(CreateUser{Email: "customer@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	unused, err := tracker.CreateUser(CreateUser{Email: "unused@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatalf("create unused account: %v", err)
	}
	for _, member := range []UpsertProductMember{
		{UserID: staff.ID, Role: "staff"},
		{UserID: statusActor.ID, Role: "staff"},
		{UserID: customer.ID, Role: "customer"},
	} {
		if _, err := tracker.UpsertProductMember(productID, member); err != nil {
			t.Fatalf("add %s member: %v", member.Role, err)
		}
	}
	ticket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:      productID,
		Title:          "Preserved history",
		AssigneeUserID: staff.ID,
		ActorUserID:    customer.ID,
	}, []CreateAttachment{{Filename: "context.txt", ContentType: "text/plain", StorageKey: "context.txt"}})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if _, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		ActorUserID: staff.ID,
		Comment:     &AddComment{Body: "Investigating", Visibility: "public"},
	}); err != nil {
		t.Fatalf("add staff comment: %v", err)
	}
	closed := "closed"
	if _, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		ActorUserID: statusActor.ID,
		Patch:       UpdateTicket{Status: &closed},
	}); err != nil {
		t.Fatalf("change status: %v", err)
	}

	disabled := true
	if _, err := tracker.UpdateUser(customer.ID, UpdateUser{Disabled: &disabled}); err != nil {
		t.Fatalf("disable customer: %v", err)
	}
	for _, userID := range []int64{customer.ID, staff.ID, statusActor.ID} {
		if err := tracker.DeleteUser(userID, EventContext{}); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "disable it instead") {
			t.Fatalf("delete historical user %d error = %v, want conflict", userID, err)
		}
		if _, err := tracker.GetUser(userID); err != nil {
			t.Fatalf("historical user %d was removed: %v", userID, err)
		}
	}
	if err := tracker.DeleteUser(unused.ID, EventContext{}); err != nil {
		t.Fatalf("delete unused account: %v", err)
	}
	if _, err := tracker.GetUser(unused.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted unused account error = %v, want not found", err)
	}
}

func TestProductMembershipFiltersTickets(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	visibleProduct := mustListProducts(t, tracker, admin)[0]
	hiddenProduct, err := tracker.CreateProduct(CreateProduct{Key: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	user, err := tracker.CreateUser(CreateUser{Email: "bob@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := tracker.UpsertProductMember(visibleProduct.ID, UpsertProductMember{UserID: user.ID, Role: "viewer"}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := tracker.CreateTicket(CreateTicket{ProductID: visibleProduct.ID, Title: "Forbidden", ActorUserID: user.ID}); !errors.Is(err, ErrValidation) {
		t.Fatalf("viewer ticket error = %v, want ErrValidation", err)
	}
	if _, err := tracker.CreateTicket(CreateTicket{ProductID: visibleProduct.ID, Title: "Visible", ActorUserID: admin.ID}); err != nil {
		t.Fatalf("create visible ticket: %v", err)
	}
	if _, err := tracker.CreateTicket(CreateTicket{ProductID: hiddenProduct.ID, Title: "Hidden", ActorUserID: admin.ID}); err != nil {
		t.Fatalf("create hidden ticket: %v", err)
	}

	page, err := tracker.ListTicketSummariesPage(user, TicketSummaryFilter{})
	if err != nil {
		t.Fatalf("list visible tickets: %v", err)
	}
	if len(page.Tickets) != 1 || page.Tickets[0].Title != "Visible" {
		t.Fatalf("visible tickets = %#v", page.Tickets)
	}
	products := mustListProducts(t, tracker, user)
	if len(products) != 1 || products[0].ID != visibleProduct.ID {
		t.Fatalf("visible products = %#v", products)
	}
}

func TestEmailRecipientsAndOutbox(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	customer, err := tracker.CreateUser(CreateUser{Password: "correct horse", Email: "bob@example.test", Role: "customer"})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	assignee, err := tracker.CreateUser(CreateUser{Password: "correct horse", Email: "alice@example.test"})
	if err != nil {
		t.Fatalf("create assignee: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: customer.ID, Role: "customer"}); err != nil {
		t.Fatalf("add customer member: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: assignee.ID, Role: "staff"}); err != nil {
		t.Fatalf("add assignee member: %v", err)
	}
	ticket, err := tracker.CreateTicket(CreateTicket{
		ProductID:      productID,
		Title:          "Notify operators",
		AssigneeUserID: assignee.ID,
		ActorUserID:    customer.ID,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	assignedUserID := assignee.ID
	assigned, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       UpdateTicket{AssigneeUserID: &assignedUserID},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("assign ticket: %v", err)
	}
	ticket = assigned.Ticket

	createdRecipients, err := tracker.TicketEmailRecipients("ticket.created", ticket, customer.ID)
	if err != nil {
		t.Fatalf("created recipients: %v", err)
	}
	if !hasRecipient(createdRecipients, admin.Email) || hasRecipient(createdRecipients, customer.Email) {
		t.Fatalf("created recipients = %#v, want admin only excluding actor customer", createdRecipients)
	}
	commentRecipients, err := tracker.TicketEmailRecipients("ticket.commented", ticket, customer.ID)
	if err != nil {
		t.Fatalf("comment recipients: %v", err)
	}
	if !hasRecipient(commentRecipients, assignee.Email) || hasRecipient(commentRecipients, customer.Email) {
		t.Fatalf("comment recipients = %#v, want assignee excluding actor customer", commentRecipients)
	}
	updateRecipients, err := tracker.TicketEmailRecipients("ticket.updated", ticket, admin.ID)
	if err != nil {
		t.Fatalf("update recipients: %v", err)
	}
	if !hasRecipient(updateRecipients, assignee.Email) || hasRecipient(updateRecipients, customer.Email) {
		t.Fatalf("update recipients = %#v, want assignee excluding customer reporter", updateRecipients)
	}
	assignedRecipients, err := tracker.TicketEmailRecipients("ticket.assigned", ticket, admin.ID)
	if err != nil {
		t.Fatalf("assigned recipients: %v", err)
	}
	if !hasRecipient(assignedRecipients, assignee.Email) {
		t.Fatalf("assigned recipients = %#v, want assignee", assignedRecipients)
	}

	queued, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProductID:      ticket.ProductID,
		TicketID:       ticket.ID,
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		RecipientName:  assignee.DisplayName,
		Event:          "ticket.assigned",
		Subject:        "[PME-1] Assigned",
		BodyText:       "assigned",
	}})
	if err != nil {
		t.Fatalf("enqueue email: %v", err)
	}
	if len(queued) != 1 || queued[0].Status != "pending" {
		t.Fatalf("queued = %#v", queued)
	}
	claimed, err := tracker.ClaimEmailNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim email: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != queued[0].ID || claimed[0].Status != "sending" {
		t.Fatalf("claimed = %#v", claimed)
	}
	if err := tracker.MarkEmailSent(claimed[0].ID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	sent, err := tracker.GetEmailNotification(claimed[0].ID)
	if err != nil {
		t.Fatalf("get sent email: %v", err)
	}
	if sent.Status != "sent" || sent.SentAt == nil {
		t.Fatalf("sent = %#v", sent)
	}

	sendAfter := time.Now().UTC().Add(time.Hour)
	first, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProductID:      ticket.ProductID,
		TicketID:       ticket.ID,
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		Event:          "ticket.updated",
		Subject:        "[PME-1] Ticket update",
		BodyText:       "first update",
		SendAfter:      sendAfter,
		Coalesce:       true,
	}})
	if err != nil {
		t.Fatalf("enqueue first coalesced email: %v", err)
	}
	renamedEmail, renamedName := "renamed@example.test", "Renamed Assignee"
	if _, err := tracker.UpdateUser(assignee.ID, UpdateUser{Email: &renamedEmail, DisplayName: &renamedName}); err != nil {
		t.Fatalf("rename assignee: %v", err)
	}
	second, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProductID:      ticket.ProductID,
		TicketID:       ticket.ID,
		UserID:         assignee.ID,
		RecipientEmail: "stale@example.test",
		RecipientName:  "Stale Assignee",
		Event:          "ticket.commented",
		Subject:        "[PME-1] Ticket update",
		BodyText:       "second update",
		SendAfter:      sendAfter.Add(time.Hour),
		Coalesce:       true,
	}})
	if err != nil {
		t.Fatalf("enqueue second coalesced email: %v", err)
	}
	if first[0].ID != second[0].ID || second[0].Event != "ticket.commented" || second[0].BodyText != "second update" ||
		second[0].RecipientEmail != "renamed@example.test" || second[0].RecipientName != "Renamed Assignee" {
		t.Fatalf("coalesced emails = first %#v second %#v", first[0], second[0])
	}
	if second[0].NextAttemptAt.Before(sendAfter.Add(59 * time.Minute)) {
		t.Fatalf("coalesced next attempt = %s, want delayed", second[0].NextAttemptAt)
	}
	claimed, err = tracker.ClaimEmailNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim delayed email: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed delayed email too early: %#v", claimed)
	}
	disabled := true
	if _, err := tracker.UpdateUser(assignee.ID, UpdateUser{Disabled: &disabled}); err != nil {
		t.Fatalf("disable assignee: %v", err)
	}
	if _, err := tracker.GetEmailNotification(second[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled user pending notification error = %v, want ErrNotFound", err)
	}
	queued, err = tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		Event:          "ticket.updated",
		Subject:        "Ignored",
		BodyText:       "Disabled account",
	}})
	if err != nil || len(queued) != 0 {
		t.Fatalf("disabled user queued notifications = %#v err=%v", queued, err)
	}
}

func TestCustomerTicketRequesterUsesUserRelation(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	customer, err := tracker.CreateUser(CreateUser{
		DisplayName: "Customer",
		Email:       "customer@example.test",
		Password:    "correct horse",
		Role:        "customer",
	})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: customer.ID, Role: "customer"}); err != nil {
		t.Fatalf("add customer member: %v", err)
	}
	ticket, err := tracker.CreateTicket(CreateTicket{
		ProductID:   productID,
		Title:       "Cannot sign in",
		Description: "Login fails",
		ActorUserID: customer.ID,
	})
	if err != nil {
		t.Fatalf("create customer ticket: %v", err)
	}
	if ticket.RequesterUserID != customer.ID || ticket.CreatedByUserID != customer.ID || ticket.CreatedByName != "Customer" || ticket.RequesterName != "Customer" || ticket.RequesterEmail != customer.Email {
		t.Fatalf("ticket fields = %#v", ticket)
	}
	newName, newEmail := "Renamed Customer", "renamed@example.test"
	if _, err := tracker.UpdateUser(customer.ID, UpdateUser{DisplayName: &newName, Email: &newEmail}); err != nil {
		t.Fatalf("update customer: %v", err)
	}
	updated, err := tracker.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("get updated ticket: %v", err)
	}
	if updated.RequesterUserID != customer.ID || updated.RequesterName != newName || updated.RequesterEmail != newEmail {
		t.Fatalf("updated requester = %#v", updated)
	}
}

func TestStaffCreatesTicketForProductCustomer(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	staff, err := tracker.CreateUser(CreateUser{DisplayName: "Support", Email: "staff@example.test", Password: "correct horse", Role: "staff"})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	customer, err := tracker.CreateUser(CreateUser{DisplayName: "Customer", Email: "customer@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	otherCustomer, err := tracker.CreateUser(CreateUser{Email: "other@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatalf("create other customer: %v", err)
	}
	outsideCustomer, err := tracker.CreateUser(CreateUser{Email: "outside@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatalf("create outside customer: %v", err)
	}
	disabledCustomer, err := tracker.CreateUser(CreateUser{Email: "disabled@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatalf("create disabled customer: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: staff.ID, Role: "staff"}); err != nil {
		t.Fatalf("add staff member: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: customer.ID, Role: "customer"}); err != nil {
		t.Fatalf("add customer member: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: otherCustomer.ID, Role: "customer"}); err != nil {
		t.Fatalf("add other customer member: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: disabledCustomer.ID, Role: "customer"}); err != nil {
		t.Fatalf("add disabled customer member: %v", err)
	}
	disabled := true
	if _, err := tracker.UpdateUser(disabledCustomer.ID, UpdateUser{Disabled: &disabled}); err != nil {
		t.Fatalf("disable customer: %v", err)
	}

	ticket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:       productID,
		Title:           "Customer cannot sign in",
		AssigneeUserID:  staff.ID,
		RequesterUserID: customer.ID,
		ActorUserID:     staff.ID,
	}, []CreateAttachment{{Filename: "details.txt", ContentType: "text/plain", StorageKey: "details.txt"}})
	if err != nil {
		t.Fatalf("create ticket for customer: %v", err)
	}
	if ticket.RequesterUserID != customer.ID || ticket.CreatedByUserID != staff.ID || ticket.CreatedByName != "Support" || ticket.AssigneeUserID != staff.ID {
		t.Fatalf("ticket = %#v", ticket)
	}
	if len(ticket.Attachments) != 1 || ticket.Attachments[0].CreatedByUserID != staff.ID {
		t.Fatalf("ticket attachments = %#v", ticket.Attachments)
	}
	events := mustListDomainEvents(t, tracker, 10)
	if len(events) != 1 || events[0].ActorUserID != staff.ID || events[0].ActorEmail != staff.Email {
		t.Fatalf("ticket events = %#v", events)
	}
	requesters, err := tracker.ListProductRequesters(staff)
	if err != nil {
		t.Fatalf("list product requesters: %v", err)
	}
	if len(requesters) != 2 || requesters[0].UserID != customer.ID || requesters[1].UserID != otherCustomer.ID {
		t.Fatalf("product requesters = %#v", requesters)
	}
	customerSummary, err := tracker.TicketSummaryForUser(customer, ticket.ID)
	if err != nil || !customerSummary.HasUnread {
		t.Fatalf("customer ticket summary = %#v err=%v, want unread", customerSummary, err)
	}
	staffSummary, err := tracker.TicketSummaryForUser(staff, ticket.ID)
	if err != nil || staffSummary.HasUnread {
		t.Fatalf("creator ticket summary = %#v err=%v, want read", staffSummary, err)
	}

	tests := []struct {
		name  string
		input CreateTicket
	}{
		{"customer chooses another requester", CreateTicket{ProductID: productID, Title: "Forged requester", RequesterUserID: otherCustomer.ID, ActorUserID: customer.ID}},
		{"requester is not a product member", CreateTicket{ProductID: productID, Title: "Wrong product", RequesterUserID: outsideCustomer.ID, ActorUserID: staff.ID}},
		{"requester is disabled", CreateTicket{ProductID: productID, Title: "Disabled requester", RequesterUserID: disabledCustomer.ID, ActorUserID: staff.ID}},
		{"requester is not a customer", CreateTicket{ProductID: productID, Title: "Wrong role", RequesterUserID: staff.ID, ActorUserID: admin.ID}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := tracker.CreateTicket(test.input); !errors.Is(err, ErrValidation) {
				t.Fatalf("create ticket error = %v, want ErrValidation", err)
			}
		})
	}
}

func hasRecipient(recipients []EmailRecipient, email string) bool {
	for _, recipient := range recipients {
		if recipient.Email == email {
			return true
		}
	}
	return false
}

func migrationNames(migrations []MigrationInfo) []string {
	names := make([]string, len(migrations))
	for i, migration := range migrations {
		names[i] = migration.Name
	}
	return names
}

func requireList[T any](t *testing.T, values []T, err error) []T {
	t.Helper()
	if err != nil {
		t.Fatalf("list values: %v", err)
	}
	return values
}

func mustListProducts(t *testing.T, tracker *Store, user User) []Product {
	values, err := tracker.ListProducts(user)
	return requireList(t, values, err)
}

func mustListDomainEvents(t *testing.T, tracker *Store, limit int) []DomainEvent {
	values, err := tracker.ListDomainEvents(limit)
	return requireList(t, values, err)
}

func mustListAuditEvents(t *testing.T, tracker *Store, limit int) []AuditEvent {
	page, err := tracker.ListAuditEventsPage(AuditEventFilter{Limit: limit})
	return requireList(t, page.Events, err)
}

func mustListEmailNotifications(t *testing.T, tracker *Store, limit int) []EmailNotification {
	page, err := tracker.ListEmailNotificationsPage(EmailNotificationFilter{Limit: limit})
	return requireList(t, page.Notifications, err)
}

func mustListAPITokens(t *testing.T, tracker *Store, userID int64) []PublicAPIToken {
	values, err := tracker.ListAPITokens(userID)
	return requireList(t, values, err)
}

func mustListUsers(t *testing.T, tracker *Store) []User {
	values, err := tracker.ListUsers()
	return requireList(t, values, err)
}

func mustListDeliveries(t *testing.T, tracker *Store, limit int) []WebhookDelivery {
	values, err := tracker.ListDeliveries(nil, limit)
	return requireList(t, values, err)
}
