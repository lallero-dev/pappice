package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestEmailFailureAndRetry(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	user, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		UserID:         user.ID,
		RecipientEmail: user.Email,
		RecipientName:  user.DisplayName,
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
	notifications := mustListEmailNotifications(t, tracker, 10)
	if len(notifications) != 1 || notifications[0].Status != "failed" || notifications[0].LastError != "temporary failure" {
		t.Fatalf("notifications = %#v", notifications)
	}
	stats, err := tracker.EmailNotificationStats()
	if err != nil {
		t.Fatalf("email notification stats: %v", err)
	}
	if stats.Total != 1 || stats.Failed != 1 || stats.LastError != "temporary failure" {
		t.Fatalf("email stats = %#v", stats)
	}
	retried, err := tracker.RetryEmailNotification(queued[0].ID, EventContext{})
	if err != nil {
		t.Fatalf("retry email: %v", err)
	}
	if retried.Status != "pending" || retried.Attempts != 0 || retried.LastError != "" {
		t.Fatalf("retried email = %#v", retried)
	}
}

func TestEmailRecipientsAndOutbox(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	admin, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
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
		ProductID:      productID,
		Title:          "Notify operators",
		AssigneeUserID: assignee.ID,
		ActorUserID:    customer.ID,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	assignedUserID := assignee.ID
	assigned, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       UpdateTicket{AssigneeUserID: &assignedUserID},
		ActorUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("assign ticket: %v", err)
	}
	ticket = assigned.Ticket

	createdRecipients, err := tracker.TicketEmailRecipients("ticket.created", ticket, customer.ID)
	if err != nil {
		t.Fatalf("created recipients: %v", err)
	}
	if !hasRecipient(createdRecipients, admin.Email) || hasRecipient(createdRecipients, customer.Email) {
		t.Fatalf("created recipients = %#v, want admin only excluding actor customer", createdRecipients)
	}
	commentRecipients, err := tracker.TicketEmailRecipients("ticket.commented", ticket, customer.ID)
	if err != nil {
		t.Fatalf("comment recipients: %v", err)
	}
	if !hasRecipient(commentRecipients, assignee.Email) || hasRecipient(commentRecipients, customer.Email) {
		t.Fatalf("comment recipients = %#v, want assignee excluding actor customer", commentRecipients)
	}
	updateRecipients, err := tracker.TicketEmailRecipients("ticket.updated", ticket, admin.ID)
	if err != nil {
		t.Fatalf("update recipients: %v", err)
	}
	if !hasRecipient(updateRecipients, assignee.Email) || hasRecipient(updateRecipients, customer.Email) {
		t.Fatalf("update recipients = %#v, want assignee excluding customer reporter", updateRecipients)
	}
	assignedRecipients, err := tracker.TicketEmailRecipients("ticket.assigned", ticket, admin.ID)
	if err != nil {
		t.Fatalf("assigned recipients: %v", err)
	}
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
	renamedEmail, renamedName := "renamed@example.test", "Renamed Assignee"
	if _, err := tracker.UpdateUser(assignee.ID, UpdateUser{Email: &renamedEmail, DisplayName: &renamedName}); err != nil {
		t.Fatalf("rename assignee: %v", err)
	}
	second, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProductID:      ticket.ProductID,
		TicketID:       ticket.ID,
		UserID:         assignee.ID,
		RecipientEmail: "stale@example.test",
		RecipientName:  "Stale Assignee",
		Event:          "ticket.commented",
		Subject:        "[PME-1] Ticket update",
		BodyText:       "second update",
		SendAfter:      sendAfter.Add(time.Hour),
		Coalesce:       true,
	}})
	if err != nil {
		t.Fatalf("enqueue second coalesced email: %v", err)
	}
	if first[0].ID != second[0].ID || second[0].Event != "ticket.commented" || second[0].BodyText != "second update" ||
		second[0].RecipientEmail != "renamed@example.test" || second[0].RecipientName != "Renamed Assignee" {
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
	disabled := true
	if _, err := tracker.UpdateUser(assignee.ID, UpdateUser{Disabled: &disabled}); err != nil {
		t.Fatalf("disable assignee: %v", err)
	}
	if _, err := tracker.GetEmailNotification(second[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled user pending notification error = %v, want ErrNotFound", err)
	}
	queued, err = tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		Event:          "ticket.updated",
		Subject:        "Ignored",
		BodyText:       "Disabled account",
	}})
	if err != nil || len(queued) != 0 {
		t.Fatalf("disabled user queued notifications = %#v err=%v", queued, err)
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
