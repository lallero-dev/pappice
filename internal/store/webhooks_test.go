package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestWebhookIdentitySurvivesCoalescingAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	tracker := openTestStore(t, path)
	hook, err := tracker.CreateWebhook(CreateWebhook{Name: "test", URL: "https://example.test/hook", Events: []string{"ticket.created"}})
	if err != nil {
		t.Fatal(err)
	}
	input := CreateWebhookNotification{WebhookID: hook.ID, Event: "ticket.created", PayloadJSON: `{"version":1}`, Coalesce: true}
	enqueue := func() WebhookNotification {
		t.Helper()
		notifications, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{input})
		if err != nil {
			t.Fatal(err)
		}
		return notifications[0]
	}
	first := enqueue()
	input.PayloadJSON = `{"version":2}`
	coalesced := enqueue()
	if first.DeliveryID == "" || coalesced.DeliveryID != first.DeliveryID || coalesced.ID != first.ID || coalesced.PayloadJSON != input.PayloadJSON {
		t.Fatalf("coalesced = %#v, first = %#v", coalesced, first)
	}
	claimed, err := tracker.ClaimWebhookNotifications(1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %#v, err = %v", claimed, err)
	}
	if err := tracker.MarkWebhookNotificationFailed(first.ID, errors.New("lost response"), 5); err != nil {
		t.Fatal(err)
	}
	input.PayloadJSON = `{"version":3}`
	next := enqueue()
	if next.ID == first.ID || next.DeliveryID == first.DeliveryID || next.DeliveryID == "" {
		t.Fatalf("new payload reused attempted identity: %#v", next)
	}
	// Simulate a crash with the original delivery still leased, then recover it.
	if _, err := tracker.db.Exec(`UPDATE webhook_notifications SET status = 'sending', locked_until = ? WHERE id = ?`, formatTime(time.Now().Add(-time.Hour)), first.ID); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	tracker = openTestStore(t, path)
	claimed, err = tracker.ClaimWebhookNotifications(1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim = %#v, err = %v", claimed, err)
	}
	if got := claimed[0]; got.ID != first.ID || got.DeliveryID != first.DeliveryID || got.PayloadJSON != coalesced.PayloadJSON || got.Attempts != 2 {
		t.Fatalf("recovered notification changed: %#v", got)
	}
}

func TestWebhookLifecycle(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	admin, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	product := mustListProducts(t, tracker, admin)[0]
	enabled := false
	hook, err := tracker.CreateWebhook(CreateWebhook{
		ProductID: &product.ID,
		Name:      "product hook",
		URL:       "https://hooks.example.test/incoming",
		Events:    []string{"*"},
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	hooks, err := tracker.ListWebhooks(&product.ID)
	if err != nil {
		t.Fatalf("list product hooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("product hooks = %#v", hooks)
	}
	enabled = true
	hook, err = tracker.UpdateWebhook(hook.ID, UpdateWebhook{
		Name:    new("renamed hook"),
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("update webhook: %v", err)
	}
	if hook.Name != "renamed hook" || !hook.Enabled {
		t.Fatalf("updated hook = %#v", hook)
	}
	events := []string{"ticket.updated"}
	secret := "manual-secret"
	hook, err = tracker.UpdateWebhook(hook.ID, UpdateWebhook{
		URL:    new("https://hooks.example.test/renamed"),
		Secret: &secret,
		Events: &events,
	})
	if err != nil {
		t.Fatalf("update webhook details: %v", err)
	}
	if hook.URL != "https://hooks.example.test/renamed" || hook.Secret != secret || !slices.Equal(hook.Events, events) {
		t.Fatalf("updated hook details = %#v", hook)
	}
	rotated, rotatedSecret, err := tracker.RotateWebhookSecret(hook.ID, EventContext{})
	if err != nil {
		t.Fatalf("rotate webhook secret: %v", err)
	}
	if rotatedSecret == "" || rotatedSecret == secret || rotated.Secret != rotatedSecret {
		t.Fatalf("rotated hook = %#v secret=%q", rotated, rotatedSecret)
	}
	if err := tracker.RecordDelivery(WebhookDelivery{WebhookID: hook.ID, ProductID: &product.ID, Event: "ticket.created", StatusCode: 204}); err != nil {
		t.Fatalf("record delivery: %v", err)
	}
	deliveries := mustListDeliveries(t, tracker, 10)
	if len(deliveries) != 1 || deliveries[0].StatusCode != 204 {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	delayedWebhook, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{{
		WebhookID:   hook.ID,
		ProductID:   &product.ID,
		Event:       "ticket.created",
		PayloadJSON: `{"event":"ticket.created"}`,
		SendAfter:   time.Now().UTC().Add(time.Hour),
	}})
	if err != nil {
		t.Fatalf("enqueue delayed webhook notification: %v", err)
	}
	if len(delayedWebhook) != 1 || delayedWebhook[0].Status != "pending" {
		t.Fatalf("delayed webhook notification = %#v", delayedWebhook)
	}
	claimedWebhooks, err := tracker.ClaimWebhookNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim delayed webhook notification: %v", err)
	}
	if len(claimedWebhooks) != 0 {
		t.Fatalf("claimed delayed webhook too early: %#v", claimedWebhooks)
	}
	dueWebhook, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{{
		WebhookID:   hook.ID,
		ProductID:   &product.ID,
		Event:       "ticket.created",
		PayloadJSON: `{"event":"ticket.created"}`,
		SendAfter:   time.Now().UTC().Add(-time.Second),
	}})
	if err != nil {
		t.Fatalf("enqueue due webhook notification: %v", err)
	}
	claimedWebhooks, err = tracker.ClaimWebhookNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim due webhook notification: %v", err)
	}
	if len(claimedWebhooks) != 1 || claimedWebhooks[0].ID != dueWebhook[0].ID || claimedWebhooks[0].Status != "sending" || claimedWebhooks[0].Attempts != 1 {
		t.Fatalf("claimed due webhook = %#v", claimedWebhooks)
	}
	if err := tracker.MarkWebhookNotificationFailed(claimedWebhooks[0].ID, errors.New("temporary webhook failure"), 1); err != nil {
		t.Fatalf("mark webhook notification failed: %v", err)
	}
	failedWebhook, err := tracker.GetWebhookNotification(claimedWebhooks[0].ID)
	if err != nil {
		t.Fatalf("get failed webhook notification: %v", err)
	}
	if failedWebhook.Status != "failed" || failedWebhook.LastError != "temporary webhook failure" {
		t.Fatalf("failed webhook notification = %#v", failedWebhook)
	}

	ticketForWebhook, err := tracker.CreateTicket(CreateTicket{ProductID: product.ID, Title: "Coalesced webhook ticket", ActorUserID: admin.ID})
	if err != nil {
		t.Fatalf("create webhook ticket: %v", err)
	}
	firstSendAfter := time.Now().UTC().Add(30 * time.Second)
	firstWebhook, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{{
		WebhookID:   hook.ID,
		ProductID:   &product.ID,
		TicketID:    ticketForWebhook.ID,
		Event:       "ticket.updated",
		PayloadJSON: `{"version":1}`,
		SendAfter:   firstSendAfter,
		Coalesce:    true,
	}})
	if err != nil {
		t.Fatalf("enqueue first coalesced webhook: %v", err)
	}
	secondSendAfter := time.Now().UTC().Add(45 * time.Second)
	secondWebhook, err := tracker.EnqueueWebhookNotifications([]CreateWebhookNotification{{
		WebhookID:   hook.ID,
		ProductID:   &product.ID,
		TicketID:    ticketForWebhook.ID,
		Event:       "ticket.commented",
		PayloadJSON: `{"version":2}`,
		SendAfter:   secondSendAfter,
		Coalesce:    true,
	}})
	if err != nil {
		t.Fatalf("enqueue second coalesced webhook: %v", err)
	}
	if len(firstWebhook) != 1 || len(secondWebhook) != 1 || secondWebhook[0].ID != firstWebhook[0].ID {
		t.Fatalf("coalesced webhook IDs = first %#v second %#v", firstWebhook, secondWebhook)
	}
	coalescedWebhook, err := tracker.GetWebhookNotification(firstWebhook[0].ID)
	if err != nil {
		t.Fatalf("get coalesced webhook notification: %v", err)
	}
	if coalescedWebhook.Event != "ticket.commented" || coalescedWebhook.PayloadJSON != `{"version":2}` {
		t.Fatalf("coalesced webhook payload = %#v", coalescedWebhook)
	}
	if coalescedWebhook.NextAttemptAt.Sub(secondSendAfter).Abs() > time.Second {
		t.Fatalf("coalesced webhook next attempt = %s, want near %s", coalescedWebhook.NextAttemptAt, secondSendAfter)
	}
	claimedWebhooks, err = tracker.ClaimWebhookNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim coalesced delayed webhook: %v", err)
	}
	if len(claimedWebhooks) != 0 {
		t.Fatalf("claimed coalesced delayed webhook too early: %#v", claimedWebhooks)
	}

	if err := tracker.DeleteWebhook(hook.ID, EventContext{}); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalWebhookMatchesProductEvents(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	admin, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	hook, err := tracker.CreateWebhook(CreateWebhook{
		Name:    "local",
		URL:     "http://127.0.0.1/hook",
		Events:  []string{"ticket.created"},
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	hooks, err := tracker.ListWebhooksForEvent("ticket.created", productID)
	if err != nil {
		t.Fatalf("list event hooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("event hooks = %#v", hooks)
	}
}
