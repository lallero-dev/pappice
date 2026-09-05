package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookIdentitySurvivesCoalescingAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	tracker, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tracker.Close() })
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
	tracker, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = tracker.ClaimWebhookNotifications(1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim = %#v, err = %v", claimed, err)
	}
	if got := claimed[0]; got.ID != first.ID || got.DeliveryID != first.DeliveryID || got.PayloadJSON != coalesced.PayloadJSON || got.Attempts != 2 {
		t.Fatalf("recovered notification changed: %#v", got)
	}
}

func TestMigrateWebhookDeliveryIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	tracker, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tracker.Close() })
	// Recreate the old queue shape, preserving rows in every delivery state.
	if _, err := tracker.CreateWebhook(CreateWebhook{Name: "test", URL: "https://example.test/hook", Events: []string{"ticket.created"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.db.Exec(`
		DELETE FROM schema_migrations WHERE version = 8;
		DROP TABLE ticket_requests;
		ALTER TABLE webhook_notifications DROP COLUMN delivery_id;
		INSERT INTO webhook_notifications (webhook_id, event, payload_json, status, attempts, next_attempt_at, created_at)
		VALUES (1, 'ticket.created', '{}', 'pending', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		       (1, 'ticket.created', '{}', 'sending', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		       (1, 'ticket.created', '{}', 'sent', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		       (1, 'ticket.created', '{}', 'failed', 5, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(path, MigrationOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if exists, err := tableHasColumn(tracker.db, "webhook_notifications", "delivery_id"); err != nil || exists {
		t.Fatalf("dry run changed source: exists = %v, err = %v", exists, err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(path, MigrationOptions{}); err != nil {
		t.Fatal(err)
	}
	tracker, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for i, status := range []string{"pending", "sending", "sent", "failed"} {
		notification, err := tracker.GetWebhookNotification(int64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
		if notification.DeliveryID == "" || ids[notification.DeliveryID] || notification.Status != status || notification.PayloadJSON != "{}" {
			t.Fatalf("migrated notification = %#v", notification)
		}
		ids[notification.DeliveryID] = true
	}
	if exists, err := tableExists(tracker.db, "ticket_requests"); err != nil || !exists {
		t.Fatalf("ticket_requests exists = %v, err = %v", exists, err)
	}
}
