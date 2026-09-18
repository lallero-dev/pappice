package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"pappice/internal/dblock"
)

func TestFailedOpenReleasesDatabaseLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.db")
	if err := os.WriteFile(path, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tracker, err := Open(path); err == nil {
		_ = tracker.Close()
		t.Fatal("invalid database opened")
	}
	lock, err := dblock.Acquire(path, dblock.Exclusive)
	if err != nil {
		t.Fatalf("failed open retained its lock: %v", err)
	}
	defer lock.Close()
}

func TestStoreCreateUpdateCommentAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracker.db")
	tracker := openTestStore(t, path)
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
	if !updatedResult.Ticket.UpdatedAt.After(ticket.UpdatedAt) {
		t.Fatalf("status change updated_at = %v, want after %v", updatedResult.Ticket.UpdatedAt, ticket.UpdatedAt)
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
	if !sameStatus.Ticket.UpdatedAt.Equal(updatedResult.Ticket.UpdatedAt) ||
		sameStatus.Ticket.ClosedAt == nil || !sameStatus.Ticket.ClosedAt.Equal(*updatedResult.Ticket.ClosedAt) {
		t.Fatalf("unchanged status changed activity timestamps: before=%#v after=%#v", updatedResult.Ticket, sameStatus.Ticket)
	}
	summary, err = tracker.TicketSummaryForUser(admin, ticket.ID)
	if err != nil || summary.LastReadAt == nil || !summary.LastReadAt.After(readAt) {
		t.Fatalf("read time after save = %v err=%v, want after %v", summary.LastReadAt, err, readAt)
	}
	title := "Cannot import updated invoice"
	description := "Metadata only"
	priority := "low"
	unassignedUserID := int64(0)
	metadataOnly, err := tracker.SaveTicket(SaveTicketInput{
		TicketID: ticket.ID,
		Patch: UpdateTicket{
			Title:          &title,
			Description:    &description,
			Priority:       &priority,
			AssigneeUserID: &unassignedUserID,
		},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("update ticket metadata: %v", err)
	}
	if !metadataOnly.AssignmentChanged || metadataOnly.Ticket.Title != title || metadataOnly.Ticket.Description != description ||
		metadataOnly.Ticket.Priority != priority || metadataOnly.Ticket.AssigneeUserID != 0 || metadataOnly.Ticket.AssigneeEmail != "" {
		t.Fatalf("metadata-only update = %#v", metadataOnly)
	}
	if !metadataOnly.Ticket.UpdatedAt.Equal(updatedResult.Ticket.UpdatedAt) {
		t.Fatalf("metadata-only updated_at = %v, want %v", metadataOnly.Ticket.UpdatedAt, updatedResult.Ticket.UpdatedAt)
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
	if !withComment.UpdatedAt.After(metadataOnly.Ticket.UpdatedAt) {
		t.Fatalf("reply updated_at = %v, want after %v", withComment.UpdatedAt, metadataOnly.Ticket.UpdatedAt)
	}
	if got := len(withComment.Comments); got != 1 {
		t.Fatalf("comments = %d, want 1", got)
	}
	if withComment.Comments[0].Author != "Alice Admin" {
		t.Fatalf("email comment author = %q, want display name", withComment.Comments[0].Author)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded := openTestStore(t, path)
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
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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

func TestStoreValidation(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))

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
	if got, want := ProductRoles(), []string{"manager", "staff", "internal_contributor", "viewer", "customer"}; !slices.Equal(got, want) {
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
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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

func TestTicketAttachmentsHydrateWithTicketAndComments(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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

func TestCustomerTicketRequesterUsesUserRelation(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	tracker, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tracker.Close(); err != nil {
			t.Error(err)
		}
	})
	return tracker
}
