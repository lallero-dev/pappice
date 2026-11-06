package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestEmailReplyCredentials(t *testing.T) {
	for _, change := range []string{"none", "disabled", "email", "membership", "viewer", "requester", "token"} {
		t.Run(change, func(t *testing.T) {
			tracker := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
			if err != nil {
				t.Fatal(err)
			}
			customer, err := tracker.CreateUser(CreateUser{Email: "requester@example.test", Password: "correct horse", Role: "customer"})
			if err != nil {
				t.Fatal(err)
			}
			productID := mustListProducts(t, tracker, admin)[0].ID
			if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: customer.ID, Role: "customer"}); err != nil {
				t.Fatal(err)
			}
			ticket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Email reply", ActorUserID: admin.ID, RequesterUserID: customer.ID})
			if err != nil {
				t.Fatal(err)
			}
			token, err := tracker.EmailReplyToken(ticket.ID, customer.ID, customer.Email)
			if err != nil || len(token) != 40 {
				t.Fatalf("issue token: %v", err)
			}
			if again, err := tracker.EmailReplyToken(ticket.ID, customer.ID, customer.Email); err != nil || token != again {
				t.Fatalf("token changed between notifications: %v", err)
			}
			if other, err := tracker.EmailReplyToken(ticket.ID, admin.ID, admin.Email); err != nil || other != "" {
				t.Fatalf("issued token to non-requester: %v", err)
			}
			if _, err := tracker.SaveEmailReply(token, admin.Email, "forged", "Hello", nil); !errors.Is(err, ErrNotFound) {
				t.Fatalf("accepted wrong sender: %v", err)
			}
			switch change {
			case "disabled":
				_, err = tracker.UpdateUser(customer.ID, UpdateUser{Disabled: new(true)})
			case "email":
				_, err = tracker.UpdateUser(customer.ID, UpdateUser{Email: new("new@example.test")})
			case "membership":
				err = tracker.DeleteProductMember(productID, customer.ID, EventContext{})
			case "viewer":
				_, err = tracker.db.Exec(`UPDATE product_members SET role = 'viewer' WHERE user_id = ?`, customer.ID)
			case "requester":
				_, err = tracker.db.Exec(`UPDATE tickets SET requester_user_id = ? WHERE id = ?`, admin.ID, ticket.ID)
			case "token":
				_, err = tracker.db.Exec(`DELETE FROM email_reply_tokens WHERE ticket_id = ?`, ticket.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := tracker.SaveEmailReply(token, customer.Email, "reply", "Hello", nil)
			if change != "none" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("accepted revoked access: %v", err)
				}
				return
			}
			if err != nil || len(result.Ticket.Comments) != 1 || result.Ticket.Comments[0].AuthorUserID != customer.ID || result.Ticket.Comments[0].Visibility != "public" {
				t.Fatalf("saved reply = %#v, %v", result, err)
			}
			if _, err := tracker.SaveEmailReply(token, customer.Email, "reply", "Different content", nil); !errors.Is(err, ErrConflict) {
				t.Fatalf("reused message ID: %v", err)
			}
		})
	}
}

func TestEmailReplyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	tracker := openTestStore(t, path)
	if _, err := tracker.db.Exec(`DROP TABLE email_reply_tokens; DROP TABLE email_mailboxes; DELETE FROM schema_migrations WHERE version = 9`); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Migrate(path, MigrationOptions{})
	if err != nil || len(result.Applied) != 1 || result.Applied[0].Version != 9 {
		t.Fatalf("migration = %#v, %v", result, err)
	}
	tracker = openTestStore(t, path)
	if err := tracker.AdvanceEmailMailbox("mailbox", 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AdvanceEmailMailbox("mailbox", 1, 5); err != nil {
		t.Fatal(err)
	}
	if uid, err := tracker.EmailMailboxCursor("mailbox", 1); err != nil || uid != 10 {
		t.Fatalf("cursor went backwards: %d, %v", uid, err)
	}
	if uid, err := tracker.EmailMailboxCursor("mailbox", 2); err != nil || uid != 0 {
		t.Fatalf("new UIDVALIDITY retained old cursor: %d, %v", uid, err)
	}
}
