package notify

import (
	"context"
	"errors"
	"net/mail"
	"path/filepath"
	"strings"
	"testing"

	"pemmece/internal/store"
)

type fakeMailer struct {
	messages []Message
	err      error
}

func (m *fakeMailer) Send(_ context.Context, message Message) error {
	if m.err != nil {
		return m.err
	}
	m.messages = append(m.messages, message)
	return nil
}

func TestWorkerSendsClaimedEmail(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	user, err := tracker.CreateFirstAdmin(store.CreateUser{
		Username: "Admin",
		Password: "correct horse",
		Email:    "admin@example.test",
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	queued, err := tracker.EnqueueEmailNotifications([]store.CreateEmailNotification{{
		UserID:         user.ID,
		RecipientEmail: user.Email,
		RecipientName:  user.DisplayName,
		Event:          "ticket.created",
		Subject:        "Subject",
		BodyText:       "Body",
	}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	mailer := &fakeMailer{}
	worker := Worker{
		Store:       tracker,
		Mailer:      mailer,
		From:        "noreply@example.test",
		BatchSize:   10,
		MaxAttempts: 3,
	}
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process once: %v", err)
	}
	if len(mailer.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(mailer.messages))
	}
	if mailer.messages[0].To != user.Email || mailer.messages[0].Subject != "Subject" {
		t.Fatalf("message = %#v", mailer.messages[0])
	}
	notification, err := tracker.GetEmailNotification(queued[0].ID)
	if err != nil {
		t.Fatalf("get notification: %v", err)
	}
	if notification.Status != "sent" {
		t.Fatalf("status = %q, want sent", notification.Status)
	}
}

func TestWorkerMarksFailedEmail(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	user, err := tracker.CreateFirstAdmin(store.CreateUser{
		Username: "Admin",
		Password: "correct horse",
		Email:    "admin@example.test",
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	queued, err := tracker.EnqueueEmailNotifications([]store.CreateEmailNotification{{
		UserID:         user.ID,
		RecipientEmail: user.Email,
		RecipientName:  user.DisplayName,
		Event:          "ticket.created",
		Subject:        "Subject",
		BodyText:       "Body",
	}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	worker := Worker{
		Store:       tracker,
		Mailer:      &fakeMailer{err: errors.New("smtp offline")},
		BatchSize:   10,
		MaxAttempts: 1,
	}
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process once: %v", err)
	}
	notification, err := tracker.GetEmailNotification(queued[0].ID)
	if err != nil {
		t.Fatalf("get notification: %v", err)
	}
	if notification.Status != "failed" || notification.LastError != "smtp offline" || notification.Attempts != 1 {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestSMTPConfigAndMessageRendering(t *testing.T) {
	if (SMTPConfig{}).Enabled() {
		t.Fatal("empty smtp config should not be enabled")
	}
	if _, err := NewSMTPMailer(SMTPConfig{Host: "smtp.example.test", From: "bad address"}); err == nil {
		t.Fatal("invalid from address should fail")
	}
	if _, err := NewSMTPMailer(SMTPConfig{Host: "smtp.example.test", From: "noreply@example.test", TLSMode: "invalid"}); err == nil {
		t.Fatal("invalid tls mode should fail")
	}
	mailer, err := NewSMTPMailer(SMTPConfig{Host: "smtp.example.test", From: "noreply@example.test", TLSMode: "none"})
	if err != nil {
		t.Fatalf("new smtp mailer: %v", err)
	}
	if mailer.config.Port != 587 || mailer.config.Timeout <= 0 || mailer.config.TLSMode != "none" {
		t.Fatalf("smtp defaults = %#v", mailer.config)
	}

	rendered := string(renderMessage(
		mustAddress(t, "Pemmece <noreply@example.test>"),
		mustAddress(t, "client@example.test"),
		Message{
			ToName:   "Client",
			Subject:  "Line one\nline two",
			BodyText: "Hello\nplain",
			BodyHTML: "<p>Hello</p>",
		},
	))
	for _, want := range []string{
		"Subject: Line one line two",
		"multipart/alternative",
		"text/plain",
		"text/html",
		"Hello\r\nplain",
		"<p>Hello</p>",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered message missing %q:\n%s", want, rendered)
		}
	}
}

func mustAddress(t *testing.T, raw string) *mail.Address {
	t.Helper()
	address, err := mail.ParseAddress(raw)
	if err != nil {
		t.Fatalf("parse address: %v", err)
	}
	return address
}
