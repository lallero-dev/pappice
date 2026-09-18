package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestTicketRequestIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	tracker := openTestStore(t, path)
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	ticket, err := tracker.CreateTicket(CreateTicket{ProductID: productID, Title: "Retry", ActorUserID: admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	input := SaveTicketInput{
		TicketID: ticket.ID, ActorUserID: admin.ID, IdempotencyKey: "reply-1",
		Comment: &AddComment{Body: "One reply", Visibility: "public"},
	}
	var workers sync.WaitGroup
	results := make(chan SaveTicketResult, 10)
	errorsSeen := make(chan error, 10)
	for range 10 {
		workers.Go(func() {
			result, err := tracker.SaveTicket(input)
			results <- result
			errorsSeen <- err
		})
	}
	workers.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	created := 0
	for result := range results {
		if !result.Replayed {
			created++
		}
		if len(result.Ticket.Comments) != 1 {
			t.Fatalf("comments = %#v", result.Ticket.Comments)
		}
	}
	if created != 1 {
		t.Fatalf("new requests = %d, want 1", created)
	}
	if events := mustListDomainEvents(t, tracker, 100); len(events) != 2 {
		t.Fatalf("events = %d, want ticket creation and one comment", len(events))
	}

	// Retrying an old public reply must not reopen a subsequently closed ticket.
	closed := "closed"
	saved, err := tracker.SaveTicket(SaveTicketInput{TicketID: ticket.ID, ActorUserID: admin.ID, Patch: UpdateTicket{Status: &closed}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	tracker = openTestStore(t, path)
	replayed, err := tracker.SaveTicket(input)
	if err != nil || !replayed.Replayed || replayed.Ticket.Status != "closed" || !replayed.Ticket.UpdatedAt.Equal(saved.Ticket.UpdatedAt) {
		t.Fatalf("replay after restart = %#v, err = %v", replayed, err)
	}
	for _, comment := range []AddComment{
		{Body: "Changed reply", Visibility: "public"},
		{Body: "One reply", Visibility: "internal"},
	} {
		conflicting := input
		conflicting.Comment = &comment
		if _, err := tracker.SaveTicket(conflicting); !errors.Is(err, ErrConflict) {
			t.Fatalf("changed comment error = %v, want conflict", err)
		}
	}
	// Invalid mutations must roll back the key reservation as well as the write.
	invalid := "invalid"
	input.IdempotencyKey = "reply-2"
	input.Patch.Priority = &invalid
	if _, err := tracker.SaveTicket(input); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid priority error = %v", err)
	}
	input.Patch = UpdateTicket{}
	if result, err := tracker.SaveTicket(input); err != nil || result.Replayed || len(result.Ticket.Comments) != 2 {
		t.Fatalf("retry after rollback = %#v, err = %v", result, err)
	}
	// Omitting the key preserves the existing API behavior.
	input.IdempotencyKey = ""
	for want := 3; want <= 4; want++ {
		if result, err := tracker.SaveTicket(input); err != nil || result.Replayed || len(result.Ticket.Comments) != want {
			t.Fatalf("unkeyed request = %#v, err = %v", result, err)
		}
	}
}
