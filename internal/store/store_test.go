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
	products := tracker.ListProducts(admin)
	if len(products) != 1 {
		t.Fatalf("products = %d, want 1", len(products))
	}

	ticket, err := tracker.CreateTicket(CreateTicket{
		ProductID: products[0].ID,
		Title:     "Cannot import invoice",
		Priority:  "urgent",
		Reporter:  admin.Email,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if ticket.ID != 1 {
		t.Fatalf("ticket ID = %d, want 1", ticket.ID)
	}
	if ticket.ProductKey != products[0].Key || ticket.ProductName != products[0].Name || ticket.Product != products[0].Name {
		t.Fatalf("ticket product labels = key %q name %q product %q", ticket.ProductKey, ticket.ProductName, ticket.Product)
	}
	if ticket.RequesterName != "Alice Admin" {
		t.Fatalf("requester name = %q, want account display name", ticket.RequesterName)
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
	readTimes, err := tracker.TicketReadTimes(admin.ID, []int64{ticket.ID})
	if err != nil {
		t.Fatalf("ticket read times: %v", err)
	}
	if got := readTimes[ticket.ID]; got.IsZero() || got.Sub(readAt).Abs() > time.Second {
		t.Fatalf("read time = %v, want near %v", got, readAt)
	}

	status := "assigned"
	assignee := "alice@example.test"
	updated, err := tracker.UpdateTicket(ticket.ID, UpdateTicket{
		Status:   &status,
		Assignee: &assignee,
	})
	if err != nil {
		t.Fatalf("update ticket: %v", err)
	}
	if updated.Status != "assigned" || updated.Assignee != "alice@example.test" {
		t.Fatalf("updated ticket = %#v", updated)
	}

	withComment, err := tracker.AddComment(ticket.ID, AddComment{
		Author: admin.Email,
		Body:   "Reproduced on Linux.",
	})
	if err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if got := len(withComment.Comments); got != 1 {
		t.Fatalf("comments = %d, want 1", got)
	}
	if withComment.Comments[0].Author != "Alice Admin" {
		t.Fatalf("email comment author = %q, want display name", withComment.Comments[0].Author)
	}
	withComment, err = tracker.AddComment(ticket.ID, AddComment{
		Author:       "stored identity",
		AuthorUserID: admin.ID,
		Body:         "Author ID maps to the display name.",
	})
	if err != nil {
		t.Fatalf("add identified comment: %v", err)
	}
	if got := withComment.Comments[1].Author; got != "Alice Admin" {
		t.Fatalf("identified comment author = %q, want display name", got)
	}

	reloaded, err := Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	tickets := reloaded.ListTickets(Filter{Query: "linux"})
	if len(tickets) != 0 {
		t.Fatalf("comments should not match ticket search, got %d", len(tickets))
	}
	tickets = reloaded.ListTickets(Filter{ProductID: products[0].ID})
	if len(tickets) != 1 {
		t.Fatalf("product tickets = %d, want 1", len(tickets))
	}
	if tickets[0].RequesterName != "Alice Admin" {
		t.Fatalf("reloaded requester name = %q, want display name", tickets[0].RequesterName)
	}
	if len(tickets[0].Comments) != 2 || tickets[0].Comments[0].Author != "Alice Admin" || tickets[0].Comments[1].Author != "Alice Admin" {
		t.Fatalf("reloaded comment authors = %#v, want display names", tickets[0].Comments)
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
	productID := tracker.ListProducts(admin)[0].ID

	_, err = tracker.CreateTicket(CreateTicket{ProductID: productID, Priority: "normal"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("empty title error = %v, want ErrValidation", err)
	}

	ticket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Bad status"})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	status := "triaged"
	_, err = tracker.UpdateTicket(ticket.ID, UpdateTicket{Status: &status})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("bad status error = %v, want ErrValidation", err)
	}

	wantStatuses := []string{"new", "assigned", "resolved", "rejected"}
	if got := Statuses(); !slices.Equal(got, wantStatuses) {
		t.Fatalf("statuses = %#v, want %#v", got, wantStatuses)
	}

	status = "rejected"
	rejected, err := tracker.UpdateTicket(ticket.ID, UpdateTicket{Status: &status})
	if err != nil {
		t.Fatalf("reject ticket: %v", err)
	}
	if rejected.Status != "rejected" || rejected.ClosedAt == nil {
		t.Fatalf("rejected ticket = %#v, want rejected with closed_at", rejected)
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
	if status.CurrentVersion != 0 || len(status.Pending) != 2 || status.Pending[0].Name != "baseline_schema" || status.Pending[1].Name != "rename_product_roles" {
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
	if status.CurrentVersion != 1 || len(status.Pending) != 1 || status.Pending[0].Name != "rename_product_roles" {
		t.Fatalf("before migration status = %#v", status)
	}
	result, err := Migrate(path, MigrationOptions{})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0].Name != "rename_product_roles" {
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

	if _, err := Migrate(path, MigrationOptions{}); err == nil {
		t.Fatal("migration should fail")
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
	if got := Statuses()[0]; got != "new" {
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
	productID := tracker.ListProducts(admin)[0].ID
	ticket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Original", Priority: "normal"})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	title := "Changed"
	status := "assigned"
	_, err = tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Patch:    UpdateTicket{Title: &title, Status: &status},
		Comment: &AddComment{
			Author:     "Admin",
			Body:       "This should not be stored",
			Visibility: "private",
		},
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("save ticket error = %v, want ErrValidation", err)
	}
	unchanged, err := tracker.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("get ticket after failed save: %v", err)
	}
	if unchanged.Title != "Original" || unchanged.Status != "new" || len(unchanged.Comments) != 0 {
		t.Fatalf("failed save was not rolled back: %#v", unchanged)
	}

	comment := AddComment{Author: "Admin", Body: "Now assigned", Visibility: "public"}
	assignee := "alice"
	saved, err := tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Patch:    UpdateTicket{Status: &status, Assignee: &assignee},
		Comment:  &comment,
	})
	if err != nil {
		t.Fatalf("save ticket: %v", err)
	}
	if !saved.HasPatch || !saved.HasComment || !saved.PublicComment || !saved.AssignmentChanged {
		t.Fatalf("save metadata = %#v", saved)
	}
	if saved.Previous.Status != "new" || saved.Ticket.Status != "assigned" || len(saved.Ticket.Comments) != 1 {
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
	productID := tracker.ListProducts(admin)[0].ID
	actor := EventActorFromUser(admin)
	ticket, err := tracker.CreateTicket(CreateTicket{
		ProductID: productID,
		Title:     "Evented ticket",
		Priority:  "normal",
		Actor:     actor,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	events := tracker.ListDomainEvents(10)
	if len(events) != 1 || events[0].Type != "ticket.created" || events[0].TicketID != ticket.ID || events[0].ActorUsername != "admin@example.test" {
		t.Fatalf("created events = %#v", events)
	}

	status := "assigned"
	assignee := "alice"
	_, err = tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Patch:    UpdateTicket{Status: &status, Assignee: &assignee},
		Comment: &AddComment{
			Author:     "Admin",
			Body:       "This should not be stored",
			Visibility: "private",
		},
		Actor: actor,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("save ticket error = %v, want ErrValidation", err)
	}
	if events := tracker.ListDomainEvents(10); len(events) != 1 {
		t.Fatalf("failed save wrote events: %#v", events)
	}

	_, err = tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Patch:    UpdateTicket{Status: &status, Assignee: &assignee},
		Comment: &AddComment{
			Author:     "Admin",
			Body:       "Taking this now",
			Visibility: "public",
		},
		Actor: actor,
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
	if !payload.HasPatch || !payload.PublicComment || !payload.AssignmentChanged || payload.PreviousStatus != "new" || payload.CurrentStatus != "assigned" {
		t.Fatalf("event payload = %#v", payload)
	}
	if err := tracker.MarkDomainEventProcessed(claimed[0].ID); err != nil {
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
	if events := tracker.ListDomainEvents(10); len(events) != 3 {
		t.Fatalf("prune removed non-processed events: %#v", events)
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
	productID := tracker.ListProducts(admin)[0].ID
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
			ActorUserID:   admin.ID,
			ActorUsername: admin.Email,
			Action:        "ticket.created",
			TargetType:    "ticket",
			TargetID:      77,
			TargetName:    "PME-77",
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
	audits := tracker.ListAuditEvents(10)
	if len(audits) != 1 || audits[0].DomainEventID != event.ID {
		t.Fatalf("audits = %#v", audits)
	}
	emails := tracker.ListEmailNotifications(10)
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
	if audits := tracker.ListAuditEvents(10); len(audits) != 1 {
		t.Fatalf("reapply duplicated audit events: %#v", audits)
	}
	if emails := tracker.ListEmailNotifications(10); len(emails) != 1 {
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
			ActorUserID:   admin.ID,
			ActorUsername: admin.Email,
			Action:        "ticket.created",
			TargetType:    "ticket",
			TargetName:    "bad projection",
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
	if audits := tracker.ListAuditEvents(10); len(audits) != 1 {
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
	events := tracker.ListDomainEvents(10)
	if len(events) != 1 || events[0].Type != "product.created" || events[0].ActorUsername != "admin@example.test" {
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
	if events := tracker.ListDomainEvents(10); len(events) != 1 {
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
	events = tracker.ListDomainEvents(10)
	if len(events) != 2 || events[0].Type != "user.created" {
		t.Fatalf("user events = %#v", events)
	}
	payload = AppEventPayload{}
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode user payload: %v", err)
	}
	if payload.TargetID != user.ID || payload.AccountLink == nil || payload.AccountLink.Event != "account.setup" || payload.AccountLink.Token != token {
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
	productID := tracker.ListProducts(admin)[0].ID

	ticket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID: productID,
		Title:     "Attached ticket",
	}, []CreateAttachment{{
		Filename:    "request.txt",
		ContentType: "text/plain",
		SizeBytes:   12,
		SHA256:      "ticket-hash",
		StorageKey:  "ti/ck/ticket-hash",
	}}, admin.ID)
	if err != nil {
		t.Fatalf("create ticket with attachment: %v", err)
	}
	if len(ticket.Attachments) != 1 || ticket.Attachments[0].Filename != "request.txt" || ticket.Attachments[0].CreatedByUserID != admin.ID {
		t.Fatalf("ticket attachments = %#v", ticket.Attachments)
	}

	saved, err := tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Comment: &AddComment{
			Author:     "Admin",
			Visibility: "internal",
		},
		Attachments: []CreateAttachment{{
			Filename:    "diagnosis.txt",
			ContentType: "text/plain",
			SizeBytes:   10,
			SHA256:      "comment-hash",
			StorageKey:  "co/mm/comment-hash",
		}},
		AttachmentUserID: admin.ID,
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
	productID := tracker.ListProducts(admin)[0].ID
	shared := CreateAttachment{
		Filename:    "shared.txt",
		ContentType: "text/plain",
		SizeBytes:   6,
		SHA256:      "shared-hash",
		StorageKey:  "sh/ar/shared-hash",
	}
	ticket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID: productID,
		Title:     "Delete me",
	}, []CreateAttachment{shared}, admin.ID)
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	other, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID: productID,
		Title:     "Keep me",
	}, []CreateAttachment{shared}, admin.ID)
	if err != nil {
		t.Fatalf("create other ticket: %v", err)
	}
	saved, err := tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Comment: &AddComment{
			Author:     "Admin",
			Visibility: "public",
		},
		Attachments: []CreateAttachment{{
			Filename:    "orphan.txt",
			ContentType: "text/plain",
			SizeBytes:   6,
			SHA256:      "orphan-hash",
			StorageKey:  "or/ph/orphan-hash",
		}},
		AttachmentUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("save comment attachment: %v", err)
	}
	commentAttachmentID := saved.Ticket.Comments[0].Attachments[0].ID

	orphaned, err := tracker.DeleteTicket(ticket.ID)
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
	if _, err := tracker.DeleteTicket(ticket.ID); !errors.Is(err, ErrNotFound) {
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
	deleteProductID := tracker.ListProducts(admin)[0].ID
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
		ProductID: deleteProductID,
		Title:     "Delete product ticket",
	}, []CreateAttachment{shared, orphan}, admin.ID)
	if err != nil {
		t.Fatalf("create deleted product ticket: %v", err)
	}
	keptTicket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID: keepProduct.ID,
		Title:     "Keep product ticket",
	}, []CreateAttachment{shared}, admin.ID)
	if err != nil {
		t.Fatalf("create kept product ticket: %v", err)
	}

	orphaned, err := tracker.DeleteProduct(deleteProductID)
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
	if !tracker.SetupRequired() {
		t.Fatal("new store should require setup")
	}

	admin, err := tracker.CreateFirstAdmin(CreateUser{
		Email:    "admin@example.test",
		Password: "correct horse",
	})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	if tracker.SetupRequired() {
		t.Fatal("store should not require setup after first admin")
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
	if user, gotCSRF, ok := tracker.UserBySession(session); !ok || user.ID != admin.ID || gotCSRF != csrf {
		t.Fatalf("session user = %#v, %v", user, ok)
	}
	if err := tracker.DeleteSession(session); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, _, ok := tracker.UserBySession(session); ok {
		t.Fatal("deleted session should not authenticate")
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
	if _, _, ok := tracker.UserBySession(shortSession); ok {
		t.Fatal("expired short session should not authenticate")
	}

	token, raw, err := tracker.CreateAPIToken(admin.ID, CreateAPIToken{Name: "cli"})
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if token.Prefix == "" || raw == "" {
		t.Fatalf("token = %#v raw=%q", token, raw)
	}
	if user, ok := tracker.UserByAPIToken(raw); !ok || user.ID != admin.ID {
		t.Fatalf("API token user = %#v, %v", user, ok)
	}
	tokens := tracker.ListAPITokens(admin.ID)
	if len(tokens) != 1 || tokens[0].ID != token.ID || tokens[0].Prefix != token.Prefix {
		t.Fatalf("API tokens = %#v", tokens)
	}
	if err := tracker.DeleteAPIToken(admin.ID, token.ID+100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing API token error = %v, want ErrNotFound", err)
	}
	if err := tracker.DeleteAPIToken(admin.ID, token.ID); err != nil {
		t.Fatalf("delete API token: %v", err)
	}
	if tokens := tracker.ListAPITokens(admin.ID); len(tokens) != 0 {
		t.Fatalf("API tokens after delete = %#v", tokens)
	}
	if user, ok := tracker.UserByAPIToken(raw); ok || user.ID != 0 {
		t.Fatalf("deleted API token user = %#v, %v", user, ok)
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
	productID := tracker.ListProducts(admin)[0].ID
	hooks := tracker.ListWebhooksForEvent("ticket.created", productID)
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("event hooks = %#v", hooks)
	}

	event, err := tracker.RecordAuditEvent(CreateAuditEvent{
		ActorUserID:   admin.ID,
		ActorUsername: admin.Email,
		Action:        "user.created",
		TargetType:    "user",
		TargetID:      admin.ID,
		TargetName:    admin.Email,
		IP:            "127.0.0.1",
		DetailsJSON:   `{"role":"admin"}`,
	})
	if err != nil {
		t.Fatalf("record audit event: %v", err)
	}
	events := tracker.ListAuditEvents(10)
	if len(events) != 1 || events[0].ID != event.ID || events[0].Action != "user.created" || events[0].DetailsJSON == "" {
		t.Fatalf("audit events = %#v", events)
	}
	_, err = tracker.RecordAuditEvent(CreateAuditEvent{
		DomainEventID: 42,
		ActorUserID:   admin.ID,
		ActorUsername: admin.Email,
		Action:        "product.created",
		TargetType:    "product",
		TargetID:      productID,
		TargetName:    "Inbox",
	})
	if err != nil {
		t.Fatalf("record domain audit event: %v", err)
	}
	_, err = tracker.RecordAuditEvent(CreateAuditEvent{
		DomainEventID: 42,
		ActorUserID:   admin.ID,
		ActorUsername: admin.Email,
		Action:        "product.created",
		TargetType:    "product",
		TargetID:      productID,
		TargetName:    "Inbox",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate domain audit error = %v, want ErrConflict", err)
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
	activated, err := tracker.ConsumeAccountLink(setupToken, "correct horse")
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
	if _, err := tracker.ChangePassword(user.ID, "wrong password", "changed password", session); !errors.Is(err, ErrValidation) {
		t.Fatalf("wrong current password error = %v, want ErrValidation", err)
	}
	changedUser, err := tracker.ChangePassword(user.ID, "correct horse", "changed password", session)
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if changedUser.PasswordResetRequired {
		t.Fatalf("changed user requires reset: %#v", changedUser)
	}
	if _, err := tracker.Authenticate("pending@example.test", "changed password"); err != nil {
		t.Fatalf("authenticate changed password: %v", err)
	}
	if _, _, ok := tracker.UserBySession(session); !ok {
		t.Fatal("change password should keep current session")
	}
	if _, _, ok := tracker.UserBySession(extraSession); ok {
		t.Fatal("change password should remove other sessions")
	}
	extraSession, _, _, err = tracker.CreateSession(user.ID)
	if err != nil {
		t.Fatalf("create session before delete user sessions: %v", err)
	}
	if err := tracker.DeleteUserSessions(user.ID, session); err != nil {
		t.Fatalf("delete user sessions: %v", err)
	}
	if _, _, ok := tracker.UserBySession(session); !ok {
		t.Fatal("delete user sessions should keep requested session")
	}
	if _, _, ok := tracker.UserBySession(extraSession); ok {
		t.Fatal("delete user sessions should remove other sessions")
	}

	resetUser, resetLink, resetToken, err := tracker.CreatePasswordResetLink(user.ID, time.Hour)
	if err != nil {
		t.Fatalf("create reset link: %v", err)
	}
	if !resetUser.PasswordResetRequired || resetLink.Purpose != "reset" || resetToken == "" {
		t.Fatalf("reset link = %#v user=%#v token=%q", resetLink, resetUser, resetToken)
	}
	if _, _, ok := tracker.UserBySession(session); ok {
		t.Fatal("password reset should invalidate existing sessions")
	}
	if _, err := tracker.Authenticate("pending@example.test", "changed password"); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("reset-required authenticate error = %v, want ErrPasswordResetRequired", err)
	}
	if _, newerLink, newerToken, err := tracker.CreatePasswordResetLink(user.ID, time.Hour); err != nil {
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

	resetComplete, err := tracker.ConsumeAccountLink(resetToken, "better password")
	if err != nil {
		t.Fatalf("consume reset link: %v", err)
	}
	if resetComplete.PasswordResetRequired {
		t.Fatalf("reset user still requires password reset: %#v", resetComplete)
	}
	if _, err := tracker.Authenticate("pending@example.test", "better password"); err != nil {
		t.Fatalf("authenticate reset user: %v", err)
	}

	_, _, expiringToken, err := tracker.CreatePasswordResetLink(user.ID, time.Nanosecond)
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
	users := tracker.ListUsers()
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
	if role, ok := tracker.ProductRole(user.ID, product.ID); !ok || role != "customer" {
		t.Fatalf("product role = %q %v", role, ok)
	}
	if err := tracker.DeleteProductMember(product.ID, user.ID); err != nil {
		t.Fatalf("delete member: %v", err)
	}
	if _, ok := tracker.ProductRole(user.ID, product.ID); ok {
		t.Fatal("product role should be removed")
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
	hooks := tracker.ListWebhooks(&product.ID)
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
	rotated, rotatedSecret, err := tracker.RotateWebhookSecret(hook.ID)
	if err != nil {
		t.Fatalf("rotate webhook secret: %v", err)
	}
	if rotatedSecret == "" || rotatedSecret == secret || rotated.Secret != rotatedSecret {
		t.Fatalf("rotated hook = %#v secret=%q", rotated, rotatedSecret)
	}
	if err := tracker.RecordDelivery(WebhookDelivery{WebhookID: hook.ID, ProductID: &product.ID, Event: "ticket.created", StatusCode: 204}); err != nil {
		t.Fatalf("record delivery: %v", err)
	}
	deliveries := tracker.ListDeliveries(10)
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

	ticketForWebhook, err := tracker.CreateTicket(CreateTicket{ProductID: product.ID, Title: "Coalesced webhook ticket"})
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

	ticket, err := tracker.CreateTicket(CreateTicket{ProductID: product.ID, Title: "Numbered support ticket"})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if gotID, ok := tracker.TicketIDByProductNumber(product.ID, ticket.Number); !ok || gotID != ticket.ID {
		t.Fatalf("ticket by product number = %d %v", gotID, ok)
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
	notifications := tracker.ListEmailNotifications(10)
	if len(notifications) != 1 || notifications[0].Status != "failed" || notifications[0].LastError != "temporary failure" {
		t.Fatalf("notifications = %#v", notifications)
	}
	stats := tracker.EmailNotificationStats()
	if stats.Total != 1 || stats.Failed != 1 || stats.LastError != "temporary failure" {
		t.Fatalf("email stats = %#v", stats)
	}
	retried, err := tracker.RetryEmailNotification(queued[0].ID)
	if err != nil {
		t.Fatalf("retry email: %v", err)
	}
	if retried.Status != "pending" || retried.Attempts != 0 || retried.LastError != "" {
		t.Fatalf("retried email = %#v", retried)
	}
	if err := tracker.DeleteWebhook(hook.ID); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if err := tracker.DeleteUser(user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := tracker.DeleteProduct(product.ID); err != nil {
		t.Fatalf("delete product: %v", err)
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
	visibleProduct := tracker.ListProducts(admin)[0]
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
	if _, err := tracker.CreateTicket(CreateTicket{ProductID: visibleProduct.ID, Title: "Visible"}); err != nil {
		t.Fatalf("create visible ticket: %v", err)
	}
	if _, err := tracker.CreateTicket(CreateTicket{ProductID: hiddenProduct.ID, Title: "Hidden"}); err != nil {
		t.Fatalf("create hidden ticket: %v", err)
	}

	tickets := tracker.ListTicketsForUser(Filter{}, user)
	if len(tickets) != 1 || tickets[0].Title != "Visible" {
		t.Fatalf("visible tickets = %#v", tickets)
	}
	products := tracker.ListProducts(user)
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
	productID := tracker.ListProducts(admin)[0].ID
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
		ProductID: productID,
		Title:     "Notify operators",
		Reporter:  customer.Email,
		Assignee:  assignee.Email,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	createdRecipients := tracker.TicketEmailRecipients("ticket.created", ticket, customer)
	if !hasRecipient(createdRecipients, admin.Email) || hasRecipient(createdRecipients, customer.Email) {
		t.Fatalf("created recipients = %#v, want admin only excluding actor customer", createdRecipients)
	}
	commentRecipients := tracker.TicketEmailRecipients("ticket.commented", ticket, customer)
	if !hasRecipient(commentRecipients, assignee.Email) || hasRecipient(commentRecipients, customer.Email) {
		t.Fatalf("comment recipients = %#v, want assignee excluding actor customer", commentRecipients)
	}
	updateRecipients := tracker.TicketEmailRecipients("ticket.updated", ticket, admin)
	if !hasRecipient(updateRecipients, assignee.Email) || hasRecipient(updateRecipients, customer.Email) {
		t.Fatalf("update recipients = %#v, want assignee excluding customer reporter", updateRecipients)
	}
	assignedRecipients := tracker.TicketEmailRecipients("ticket.assigned", ticket, admin)
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
	second, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProductID:      ticket.ProductID,
		TicketID:       ticket.ID,
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		Event:          "ticket.commented",
		Subject:        "[PME-1] Ticket update",
		BodyText:       "second update",
		SendAfter:      sendAfter.Add(time.Hour),
		Coalesce:       true,
	}})
	if err != nil {
		t.Fatalf("enqueue second coalesced email: %v", err)
	}
	if first[0].ID != second[0].ID || second[0].Event != "ticket.commented" || second[0].BodyText != "second update" {
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
}

func TestPortalTicketTokenAndPublicComments(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := tracker.ListProducts(admin)[0].ID
	ticket, err := tracker.CreateTicket(CreateTicket{
		ProductID:      productID,
		Title:          "Cannot sign in",
		Description:    "Login fails",
		Source:         "portal",
		RequesterName:  "Customer",
		RequesterEmail: "Customer@Example.Test",
	})
	if err != nil {
		t.Fatalf("create portal ticket: %v", err)
	}
	if ticket.Source != "portal" || ticket.CustomerToken == "" || ticket.RequesterEmail != "customer@example.test" {
		t.Fatalf("ticket fields = %#v", ticket)
	}
	if _, err := tracker.AddComment(ticket.ID, AddComment{Author: "Admin", Body: "Internal diagnosis", Visibility: "internal"}); err != nil {
		t.Fatalf("add internal note: %v", err)
	}
	if _, err := tracker.AddComment(ticket.ID, AddComment{Author: "Admin", Body: "Public reply", Visibility: "public"}); err != nil {
		t.Fatalf("add public reply: %v", err)
	}
	publicTicket, err := tracker.GetTicketByCustomerToken(ticket.CustomerToken)
	if err != nil {
		t.Fatalf("get ticket by token: %v", err)
	}
	if len(publicTicket.Comments) != 1 || publicTicket.Comments[0].Body != "Public reply" {
		t.Fatalf("public comments = %#v", publicTicket.Comments)
	}
	if _, err := tracker.GetTicketByCustomerToken("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing token error = %v, want ErrNotFound", err)
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
