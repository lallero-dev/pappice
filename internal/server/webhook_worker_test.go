package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pappice/internal/security"
	"pappice/internal/store"
)

func TestStalledWebhookDoesNotBlockAuditAndEmail(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
		EmailNotifications:    true,
		NotificationDelay:     -time.Second,
	})
	cookie, csrf := setupAdmin(t, client, server.URL)
	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, cookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		target.Close()
	})
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/webhooks", map[string]any{
		"name": "stalled-hook", "url": target.URL, "events": []string{"ticket.created"},
	}, cookie, csrf, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	for _, title := range []string{"First ticket", "Second ticket", "Third ticket"} {
		resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
			"product_id": productID, "title": title,
		}, cookie, csrf, server.URL)
		requireStatus(t, resp, body, http.StatusCreated)
		ticketID := decodeInt64(t, body, "id")
		if title == "First ticket" {
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("webhook did not reach the stalled receiver")
			}
		}
		// Check successive events so projection must keep making progress while
		// the receiver remains blocked, even across separate dispatch batches.
		waitForDomainEvents(t, tracker)
		audit, err := tracker.ListAuditEventsPage(store.AuditEventFilter{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, event := range audit.Events {
			if event.Action == "ticket.created" && event.TargetID == ticketID {
				found = true
			}
		}
		if !found {
			t.Fatalf("audit entry for %q was delayed by the webhook", title)
		}
		notification := requireNotificationForTicketEmail(t, tracker, ticketID, fixtureEmail("admin"))
		if notification.Event != "ticket.created" || notification.Status != "pending" {
			t.Fatalf("email for %q = %#v", title, notification)
		}
	}
}

func TestWebhookDispatcherDrainsFullBatches(t *testing.T) {
	received := make(chan struct{}, webhookDispatchBatchSize+1)
	tracker, app, hook := newWebhookWorkerTest(t, func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	})
	inputs := make([]store.CreateWebhookNotification, webhookDispatchBatchSize+1)
	for i := range inputs {
		inputs[i] = store.CreateWebhookNotification{WebhookID: hook.ID, Event: "ticket.created", PayloadJSON: `{}`}
	}
	notifications, err := tracker.EnqueueWebhookNotifications(inputs)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.RunWebhookDispatcher(ctx, time.Hour)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	for range inputs {
		select {
		case <-received:
		case <-time.After(2 * time.Second):
			t.Fatal("webhook worker did not drain its backlog")
		}
	}
	eventually(t, func() bool {
		last, err := tracker.GetWebhookNotification(notifications[len(notifications)-1].ID)
		if err != nil {
			t.Fatal(err)
		}
		return last.Status == "sent"
	}, "last webhook was not marked sent")
}

func TestWebhookDispatcherCancelsBlockedRequest(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	tracker, app, hook := newWebhookWorkerTest(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	notifications, err := tracker.EnqueueWebhookNotifications([]store.CreateWebhookNotification{{
		WebhookID: hook.ID, Event: "ticket.created", PayloadJSON: `{}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.RunWebhookDispatcher(ctx, time.Hour)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook request did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("webhook worker ignored cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("webhook connection was left open")
	}
	notification, err := tracker.GetWebhookNotification(notifications[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if notification.Status != "pending" || !strings.Contains(notification.LastError, context.Canceled.Error()) {
		t.Fatalf("cancelled notification was not left for retry: %#v", notification)
	}
}

func newWebhookWorkerTest(t *testing.T, handler http.HandlerFunc) (*store.Store, *Server, store.Webhook) {
	t.Helper()
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	target := httptest.NewServer(handler)
	t.Cleanup(target.Close)
	app := NewServer(tracker, Options{AllowInsecureWebhooks: true, AllowPrivateWebhooks: true})
	hook, err := tracker.CreateWebhook(store.CreateWebhook{
		Name: "worker-hook", URL: target.URL, Events: []string{"ticket.created"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tracker, app, hook
}

func TestWebhookRedeliveryKeepsSignedIdentity(t *testing.T) {
	type request struct {
		body          []byte
		id, signature string
	}
	requests := make(chan request, 3)
	tracker, app, hook := newWebhookWorkerTest(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		requests <- request{body, r.Header.Get("X-Pappice-Delivery-ID"), r.Header.Get("X-Pappice-Signature")}
		// The receiver accepted the work, but its acknowledgment was lost.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	})
	notifications, err := tracker.EnqueueWebhookNotifications([]store.CreateWebhookNotification{{
		WebhookID: hook.ID, Event: "ticket.created", PayloadJSON: `{"event":"ticket.created","ticket":{"id":123}}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var first request
	for attempt := range 2 {
		if err := app.dispatchPendingWebhookNotifications(context.Background(), 1); err == nil {
			t.Fatal("lost response should cause a retry")
		}
		got := <-requests
		if got.id == "" || got.id != notifications[0].DeliveryID {
			t.Fatalf("delivery ID = %q, want %q", got.id, notifications[0].DeliveryID)
		}
		var payload struct {
			DeliveryID string `json:"delivery_id"`
		}
		if err := json.Unmarshal(got.body, &payload); err != nil || payload.DeliveryID != got.id {
			t.Fatalf("signed body ID = %q, err = %v", payload.DeliveryID, err)
		}
		if want := "sha256=" + security.HMACSHA256(hook.Secret, got.body); got.signature != want {
			t.Fatalf("signature = %q, want %q", got.signature, want)
		}
		if attempt == 0 {
			first = got
		} else if !bytes.Equal(got.body, first.body) || got.signature != first.signature {
			t.Fatalf("retry changed signed payload: first = %s, retry = %s", first.body, got.body)
		}
		makePendingWebhookNotificationsDue(t, tracker)
	}
	// Crash recovery uses the same identity as an ordinary failed delivery.
	execStoreSQL(t, tracker, `UPDATE webhook_notifications SET status = 'sending', locked_until = ? WHERE id = ?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), notifications[0].ID)
	if err := app.dispatchPendingWebhookNotifications(context.Background(), 1); err == nil {
		t.Fatal("lost response should cause a retry")
	}
	if got := <-requests; got.id != first.id || !bytes.Equal(got.body, first.body) {
		t.Fatalf("recovered delivery changed: %#v", got)
	}
}
