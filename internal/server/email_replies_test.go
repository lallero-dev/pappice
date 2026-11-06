package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pappice/internal/notify"
	"pappice/internal/store"
)

func TestEmailReplyAttachments(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	user, err := tracker.CreateFirstAdmin(store.CreateUser{Email: "requester@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	products, err := tracker.ListProducts(user)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := tracker.CreateTicket(store.CreateTicket{ProductID: products[0].ID, Title: "Reply", ActorUserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	token, err := tracker.EmailReplyToken(ticket.ID, user.ID, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	app := NewServer(tracker, Options{UploadDir: dir, MaxUploadSize: 50, MaxUploadFiles: 2})
	reply := notify.EmailReply{Token: token, From: user.Email, Key: "one", Attachments: []notify.EmailAttachment{{Filename: "../report.txt", Data: []byte("report")}}}
	for range 2 {
		if err := app.AcceptEmailReply(reply); err != nil {
			t.Fatal(err)
		}
	}
	ticket, err = tracker.GetTicket(ticket.ID)
	if err != nil || len(ticket.Comments) != 1 || len(ticket.Comments[0].Attachments) != 1 {
		t.Fatalf("ticket = %#v, %v", ticket, err)
	}
	attachment := ticket.Comments[0].Attachments[0]
	if attachment.Filename != "report.txt" || attachment.ContentType != "text/plain" {
		t.Fatalf("attachment = %#v", attachment)
	}
	for _, test := range []struct {
		name  string
		files []notify.EmailAttachment
		token string
	}{
		{"size", []notify.EmailAttachment{{Filename: "big.txt", Data: []byte(strings.Repeat("x", 51))}}, token},
		{"type after valid file", []notify.EmailAttachment{reply.Attachments[0], {Filename: "bad.html", Data: []byte("<!DOCTYPE html><html>blocked</html>")}}, token},
		{"count", []notify.EmailAttachment{reply.Attachments[0], reply.Attachments[0], reply.Attachments[0]}, token},
		{"credential after upload", reply.Attachments, "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := reply
			input.Key, input.Token, input.Attachments = test.name, test.token, test.files
			if err := app.AcceptEmailReply(input); !errors.Is(err, notify.ErrRejected) {
				t.Fatalf("rejection = %v", err)
			}
		})
	}
	files := 0
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatalf("orphan uploads after rejection/replay: %d", files)
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(attachment.StorageKey)))
	if err != nil || string(data) != "report" {
		t.Fatalf("committed attachment damaged: %q, %v", data, err)
	}
}

func TestRequesterEmailReplyInstructions(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		app := NewServer(nil, Options{EmailReplies: enabled})
		_, plain, rich := app.requesterEmailContent("ticket.created", store.Ticket{Key: "SUP-1"}, "", nil)
		for _, body := range []string{plain, rich} {
			if strings.Contains(body, notify.ReplyMarker) != enabled || !strings.Contains(body, "Open Pappice to continue the conversation.") || !strings.Contains(body, "Open ticket") {
				t.Fatalf("incorrect reply instructions, enabled=%v: %s", enabled, body)
			}
			for _, unwanted := range []string{"Reply to this email", "Reply above this line", "Replies to this email are not read"} {
				if strings.Contains(body, unwanted) {
					t.Fatalf("unexpected email instruction %q, enabled=%v", unwanted, enabled)
				}
			}
		}
	}
}

func TestEmailReplyToEarlierNotification(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	admin, err := tracker.CreateFirstAdmin(store.CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	customer, err := tracker.CreateUser(store.CreateUser{Email: "requester@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	products, err := tracker.ListProducts(admin)
	if err != nil {
		t.Fatal(err)
	}
	productID := products[0].ID
	if _, err := tracker.UpsertProductMember(productID, store.UpsertProductMember{UserID: customer.ID, Role: "customer"}); err != nil {
		t.Fatal(err)
	}
	app := NewServer(tracker, Options{EmailNotifications: true, EmailReplies: true})
	for _, status := range []string{"open", "closed"} {
		t.Run(status, func(t *testing.T) {
			ticket, err := tracker.CreateTicket(store.CreateTicket{ProductID: productID, Title: "Reply", ActorUserID: admin.ID, RequesterUserID: customer.ID, AssigneeUserID: admin.ID})
			if err != nil {
				t.Fatal(err)
			}
			originalToken, err := tracker.EmailReplyToken(ticket.ID, customer.ID, customer.Email)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tracker.SaveTicket(store.SaveTicketInput{TicketID: ticket.ID, ActorUserID: admin.ID, Comment: &store.AddComment{Body: "Later staff reply"}, Patch: store.UpdateTicket{Status: &status}}); err != nil {
				t.Fatal(err)
			}
			latestToken, err := tracker.EmailReplyToken(ticket.ID, customer.ID, customer.Email)
			if err != nil || latestToken != originalToken {
				t.Fatalf("earlier reply address changed: %v", err)
			}
			if err := app.AcceptEmailReply(notify.EmailReply{Token: originalToken, From: customer.Email, Key: "reply", Body: "Reply to the earlier email"}); err != nil {
				t.Fatal(err)
			}
			ticket, err = tracker.GetTicket(ticket.ID)
			if err != nil || ticket.Status != "open" || len(ticket.Comments) != 2 {
				t.Fatalf("reply ticket = %#v, %v", ticket, err)
			}
			last := ticket.Comments[1]
			if last.AuthorUserID != customer.ID || last.Visibility != "public" || last.Body != "Reply to the earlier email" {
				t.Fatalf("reply = %#v", last)
			}
			notifications := 0
			for _, event := range mustDomainEvents(t, tracker, 100) {
				if event.TicketID != ticket.ID || event.ActorUserID != customer.ID {
					continue
				}
				projection, err := app.domainEventProjection(event)
				if err != nil {
					t.Fatal(err)
				}
				for _, notification := range projection.EmailNotifications {
					if notification.UserID != admin.ID {
						t.Fatalf("notified reply author: %#v", notification)
					}
					notifications++
				}
			}
			if notifications != 1 {
				t.Fatalf("staff notifications = %d, want 1", notifications)
			}
		})
	}
}
