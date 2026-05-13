package notify

import (
	"context"
	"path/filepath"
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
		Event:          "issue.created",
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
