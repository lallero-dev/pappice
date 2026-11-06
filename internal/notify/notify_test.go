package notify

import (
	"context"
	"errors"
	"net/mail"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pappice/internal/store"
)

func TestWorkerReplyAddress(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	requester, err := tracker.CreateFirstAdmin(store.CreateUser{Email: "requester@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	staff, err := tracker.CreateUser(store.CreateUser{Email: "staff@example.test", Password: "correct horse", Role: "staff"})
	if err != nil {
		t.Fatal(err)
	}
	products, err := tracker.ListProducts(requester)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := tracker.CreateTicket(store.CreateTicket{ProductID: products[0].ID, Title: "Email", ActorUserID: requester.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		_, err := tracker.EnqueueEmailNotifications([]store.CreateEmailNotification{
			{TicketID: ticket.ID, UserID: requester.ID, Event: "ticket.commented", Subject: "Reply", BodyText: "Body"},
			{TicketID: ticket.ID, UserID: staff.ID, Event: "ticket.commented", Subject: "Reply", BodyText: "Body"},
			{UserID: requester.ID, Event: "account.reset", Subject: "Reset", BodyText: "Body"},
		})
		if err != nil {
			t.Fatal(err)
		}
		mailer := &fakeMailer{}
		w := Worker{Store: tracker, Mailer: mailer, BatchSize: 10}
		if enabled {
			w.ReplyAddress = testReplyAddress
		}
		if err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(mailer.messages) != 3 {
			t.Fatalf("sent %d messages", len(mailer.messages))
		}
		for _, message := range mailer.messages {
			want := enabled && message.To == requester.Email && message.Subject == "Reply"
			if (message.ReplyTo != "") != want {
				t.Fatalf("unexpected reply address: %#v", message)
			}
			if !want {
				continue
			}
			local, _, _ := strings.Cut(message.ReplyTo, "@")
			token := strings.TrimPrefix(local, "reply+")
			if _, err := tracker.SaveEmailReply(token, requester.Email, "reply", "Hello", nil); err != nil {
				t.Fatal(err)
			}
			raw := renderMessage(&mail.Address{Address: "sender@example.test"}, &mail.Address{Address: requester.Email}, message)
			parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
			if err != nil || parsed.Header.Get("Reply-To") != message.ReplyTo || parsed.Header.Get("Auto-Submitted") != "auto-generated" {
				t.Fatalf("headers: %s, %v", raw, err)
			}
		}
	}
}

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
