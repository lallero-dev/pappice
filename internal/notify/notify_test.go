package notify

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"pappice/internal/store"
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

func TestWorkerProcessesEmail(t *testing.T) {
	for _, test := range []struct {
		name        string
		from        string
		sendErr     error
		maxAttempts int
		status      string
		attempts    int
	}{
		{"sent", "noreply@example.test", nil, 3, "sent", 0},
		{"retry", "", errors.New("smtp offline"), 3, "pending", 1},
		{"exhausted", "", errors.New("smtp offline"), 1, "failed", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker, err := store.Open(filepath.Join(t.TempDir(), "tracker.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer tracker.Close()
			user, err := tracker.CreateFirstAdmin(store.CreateUser{Password: "correct horse", Email: "admin@example.test"})
			if err != nil {
				t.Fatal(err)
			}
			queued, err := tracker.EnqueueEmailNotifications([]store.CreateEmailNotification{{
				UserID: user.ID, RecipientEmail: user.Email, RecipientName: user.DisplayName,
				Event: "ticket.created", Subject: "Subject", BodyText: "Body",
			}})
			if err != nil {
				t.Fatal(err)
			}
			mailer := &fakeMailer{err: test.sendErr}
			worker := Worker{Store: tracker, Mailer: mailer, From: test.from, BatchSize: 10, MaxAttempts: test.maxAttempts}
			if err := worker.ProcessOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			notification, err := tracker.GetEmailNotification(queued[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if notification.Status != test.status || notification.Attempts != test.attempts {
				t.Fatalf("notification = %#v, want status %s, attempts %d", notification, test.status, test.attempts)
			}
			if test.sendErr == nil {
				if len(mailer.messages) != 1 || mailer.messages[0].To != user.Email || mailer.messages[0].Subject != "Subject" {
					t.Fatalf("messages = %#v", mailer.messages)
				}
			} else if notification.LastError != test.sendErr.Error() {
				t.Fatalf("last error = %q, want %v", notification.LastError, test.sendErr)
			}
			if test.status == "pending" {
				if !notification.NextAttemptAt.After(time.Now()) {
					t.Fatal("retry was not deferred")
				}
				if claimed, err := tracker.ClaimEmailNotifications(1, time.Minute); err != nil || len(claimed) != 0 {
					t.Fatalf("deferred notification reclaimed: %#v, %v", claimed, err)
				}
			}
		})
	}
}

func TestWorkerRunReturnsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		Worker{Interval: time.Millisecond}.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not return after context cancellation")
	}
}
