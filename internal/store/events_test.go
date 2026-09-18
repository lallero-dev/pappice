package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestTicketMutationsWriteDomainEventsTransactionally(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))

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
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
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

	user, _, token, err := tracker.CreateUserWithSetupLink(CreateUser{
		Email: "pending@example.test",
		Role:  "staff",
		Event: ctx,
	}, time.Hour)
	if err != nil {
		t.Fatalf("create setup link user: %v", err)
	}
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
