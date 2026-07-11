package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pappice/internal/store"
)

func TestAccountLinkProjectionResolvesUserByID(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "pappice.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	admin, err := tracker.CreateFirstAdmin(store.CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	user, _, _, err := tracker.CreateUserWithSetupLink(store.CreateUser{
		Email: "old@example.test",
		Role:  "staff",
		Event: store.EventContext{Enabled: true, Actor: store.EventActorFromUser(admin)},
	}, time.Hour)
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}
	newEmail := "new@example.test"
	newName := "Current Name"
	if _, err := tracker.UpdateUser(user.ID, store.UpdateUser{Email: &newEmail, DisplayName: &newName}); err != nil {
		t.Fatalf("update pending user: %v", err)
	}
	events := mustDomainEvents(t, tracker, 10)
	if len(events) != 1 {
		t.Fatalf("domain events = %#v", events)
	}

	server := &Server{store: tracker, options: Options{EmailNotifications: true}}
	projection, err := server.domainEventProjection(events[0])
	if err != nil {
		t.Fatalf("project account event: %v", err)
	}
	if len(projection.EmailNotifications) != 1 || projection.EmailNotifications[0].UserID != user.ID ||
		projection.EmailNotifications[0].RecipientEmail != newEmail || projection.EmailNotifications[0].RecipientName != newName {
		t.Fatalf("email notifications = %#v", projection.EmailNotifications)
	}
	if err := tracker.DeleteUser(user.ID); err != nil {
		t.Fatalf("delete pending user: %v", err)
	}
	projection, err = server.domainEventProjection(events[0])
	if err != nil {
		t.Fatalf("project deleted account event: %v", err)
	}
	if len(projection.EmailNotifications) != 0 {
		t.Fatalf("deleted account notifications = %#v", projection.EmailNotifications)
	}
}

func TestRequesterEmailContentUsesReadableLayout(t *testing.T) {
	server := &Server{options: Options{PublicURL: "https://tracker.example.test"}}
	ticket := store.Ticket{
		Key:        "PME-1",
		ProductKey: "PME",
		Title:      "Need <help>",
		Status:     "resolved",
		Comments: []store.Comment{{
			Author:     "Alice",
			Body:       "Please try the updated setup.\nIt should work now.",
			Visibility: "public",
		}},
	}

	subject, textBody, htmlBody := server.requesterEmailContent("ticket.commented", ticket, "Alice")

	if subject != "[PME-1] Ticket update: Need <help>" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"Alice replied to your ticket.",
		"Ticket: PME-1",
		"Current status: Resolved",
		"Latest public reply from Alice:",
		"Open your ticket:\nhttps://tracker.example.test/",
		"Replies to this email are not read.",
	} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("text body missing %q:\n%s", want, textBody)
		}
	}
	for _, want := range []string{
		"Pappice customer support",
		"Need &lt;help&gt;",
		"Latest public reply",
		"from Alice",
		`<table role="presentation"`,
		"Please try the updated setup.<br>It should work now.",
	} {
		if !strings.Contains(htmlBody, want) {
			t.Fatalf("html body missing %q:\n%s", want, htmlBody)
		}
	}
	if strings.Contains(htmlBody, "Need <help>") {
		t.Fatalf("html body did not escape title:\n%s", htmlBody)
	}
	if strings.Contains(htmlBody, `width:34%;">Latest public reply`) {
		t.Fatalf("latest public reply block is still split into a detached label column:\n%s", htmlBody)
	}
}

func TestTicketEmailContentUsesReadableLayout(t *testing.T) {
	server := &Server{options: Options{PublicURL: "https://tracker.example.test"}}
	ticket := store.Ticket{
		Key:           "PME-2",
		ProductKey:    "PME",
		ProductName:   "Pappice",
		Title:         "Cannot sign in",
		Description:   "Login fails after password reset.",
		Status:        "assigned",
		Priority:      "urgent",
		AssigneeEmail: "dev@example.test",
		RequesterName: "Customer",
	}
	actor := store.EventActor{DisplayName: "Paolo", Email: "paolo@example.test"}

	subject, textBody, htmlBody := server.ticketEmailContent("ticket.assigned", ticket, actor)

	if subject != "[PME-2] Ticket update: Cannot sign in" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"Paolo assigned PME-2.",
		"Product: Pappice",
		"Priority: urgent",
		"Description:\nLogin fails after password reset.",
		"Open in Pappice:\nhttps://tracker.example.test/",
	} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("text body missing %q:\n%s", want, textBody)
		}
	}
	for _, want := range []string{
		"Pappice staff notification",
		"Pappice",
		"Login fails after password reset.",
		`href="https://tracker.example.test/"`,
	} {
		if !strings.Contains(htmlBody, want) {
			t.Fatalf("html body missing %q:\n%s", want, htmlBody)
		}
	}
}
