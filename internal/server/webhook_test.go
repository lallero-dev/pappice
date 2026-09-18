package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pappice/internal/store"
)

func TestWebhookDNSRespectsCancellation(t *testing.T) {
	for _, operation := range []string{"validate", "dial"} {
		t.Run(operation, func(t *testing.T) {
			started := make(chan struct{}, 1)
			resolver := net.DefaultResolver
			net.DefaultResolver = &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					select {
					case started <- struct{}{}:
					default:
					}
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}
			defer func() { net.DefaultResolver = resolver }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			app := &Server{}
			done := make(chan error, 1)
			go func() {
				if operation == "validate" {
					done <- app.validateWebhookTarget(ctx, "https://hooks.example.test")
					return
				}
				conn, err := app.dialWebhookContext(ctx, "tcp", "hooks.example.test:443")
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
			}()
			select {
			case <-started:
			case err := <-done:
				t.Fatalf("request ended before DNS lookup: %v", err)
			case <-time.After(time.Second):
				t.Fatal("DNS lookup did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lookup error = %v, want cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("DNS lookup ignored cancellation")
			}
		})
	}
}

func TestWebhookGuardrails(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	blocking := httptest.NewTLSServer(NewServer(tracker))
	defer blocking.Close()
	client := blocking.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	setupResp, setupBody := doJSON(t, client, http.MethodPost, blocking.URL+"/api/setup", map[string]any{
		"email":    "admin@example.test",
		"password": "correct horse",
	}, nil, "", blocking.URL)
	requireStatus(t, setupResp, setupBody, http.StatusCreated)
	adminCookie := setupResp.Cookies()[0]
	adminCSRF := decodeString(t, setupBody, "csrf_token")

	resp, body := doJSON(t, client, http.MethodPost, blocking.URL+"/api/webhooks", map[string]any{
		"name": "local",
		"url":  target.URL,
	}, adminCookie, adminCSRF, blocking.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	hook := decodeNestedInt64(t, body, "webhook", "id")

	resp, body = doJSON(t, client, http.MethodPost, blocking.URL+"/api/webhooks/"+itoa(hook)+"/test", nil, adminCookie, adminCSRF, blocking.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if targetHits != 0 {
		t.Fatalf("blocked webhook reached target")
	}
	if got := decodeString(t, body, "error"); got == "" {
		t.Fatalf("blocked webhook error missing: %s", body)
	}

	permissiveStore, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open permissive store: %v", err)
	}
	defer permissiveStore.Close()
	permissive := httptest.NewTLSServer(NewServer(permissiveStore, Options{AllowInsecureWebhooks: true, AllowPrivateWebhooks: true}))
	defer permissive.Close()
	permissiveClient := permissive.Client()
	permissiveClient.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	setupResp, setupBody = doJSON(t, permissiveClient, http.MethodPost, permissive.URL+"/api/setup", map[string]any{
		"email":    "admin@example.test",
		"password": "correct horse",
	}, nil, "", permissive.URL)
	requireStatus(t, setupResp, setupBody, http.StatusCreated)
	adminCookie = setupResp.Cookies()[0]
	adminCSRF = decodeString(t, setupBody, "csrf_token")
	resp, body = doJSON(t, permissiveClient, http.MethodPost, permissive.URL+"/api/webhooks", map[string]any{
		"name": "local",
		"url":  target.URL,
	}, adminCookie, adminCSRF, permissive.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	hook = decodeNestedInt64(t, body, "webhook", "id")
	resp, body = doJSON(t, permissiveClient, http.MethodPost, permissive.URL+"/api/webhooks/"+itoa(hook)+"/test", nil, adminCookie, adminCSRF, permissive.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if targetHits != 1 {
		t.Fatalf("target hits = %d, want 1", targetHits)
	}

	redirectHits := 0
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectHits++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer redirectTarget.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer redirector.Close()

	resp, body = doJSON(t, permissiveClient, http.MethodPost, permissive.URL+"/api/webhooks", map[string]any{
		"name": "redirect",
		"url":  redirector.URL,
	}, adminCookie, adminCSRF, permissive.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	hook = decodeNestedInt64(t, body, "webhook", "id")
	resp, body = doJSON(t, permissiveClient, http.MethodPost, permissive.URL+"/api/webhooks/"+itoa(hook)+"/test", nil, adminCookie, adminCSRF, permissive.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if redirectHits != 0 {
		t.Fatalf("redirected webhook reached target")
	}
	if got := decodeInt64(t, body, "status_code"); got != http.StatusFound {
		t.Fatalf("redirect webhook status = %d, want %d; body=%s", got, http.StatusFound, body)
	}
}

func TestWebhookValidationBlocksPrivateHTTP(t *testing.T) {
	server := &Server{options: Options{AllowInsecureWebhooks: true}}
	for _, target := range []string{
		"http://localhost:8080/hook",
		"http://127.0.0.1:8080/hook",
	} {
		if err := server.validateWebhookTarget(context.Background(), target); err == nil {
			t.Fatalf("validateWebhookTarget(%q) succeeded, want private target error", target)
		}
	}
}

func TestMutationQueuesWebhookDelivery(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	app := NewServer(tracker, Options{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
	})
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = time.Second
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)
	if err := app.dispatchPendingEvents(context.Background(), eventDispatchBatchSize); err != nil {
		t.Fatalf("dispatch setup event: %v", err)
	}
	select {
	case <-app.eventWake:
	default:
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	released := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	hook, err := tracker.CreateWebhook(store.CreateWebhook{
		Name:   "blocked-hook",
		URL:    target.URL,
		Events: []string{"ticket.created"},
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	notifications, err := tracker.EnqueueWebhookNotifications([]store.CreateWebhookNotification{{
		WebhookID:   hook.ID,
		Event:       "ticket.created",
		PayloadJSON: `{}`,
	}})
	if err != nil {
		t.Fatalf("enqueue webhook: %v", err)
	}

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "OPS",
		"name": "Operations",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	select {
	case <-app.eventWake:
	default:
		t.Fatal("mutation did not wake the event dispatcher")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.RunWebhookDispatcher(ctx, time.Hour)
	}()
	t.Cleanup(func() {
		if !released {
			close(release)
		}
		cancel()
		<-done
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background dispatcher did not deliver the webhook")
	}
	close(release)
	released = true
	eventually(t, func() bool {
		notification, err := tracker.GetWebhookNotification(notifications[0].ID)
		if err != nil {
			t.Fatalf("get webhook notification: %v", err)
		}
		return notification.Status == "sent"
	}, "webhook notification was not marked sent")
}

func TestWebhookDeliveryFlow(t *testing.T) {
	_, server, client := newTestServer(t, Options{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	var webhookHits atomic.Int64
	var signatureSeen atomic.Bool
	var ticketEventSeen atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookHits.Add(1)
		if strings.HasPrefix(r.Header.Get("X-Pappice-Signature"), "sha256=") {
			signatureSeen.Store(true)
		}
		switch got := r.Header.Get("X-Pappice-Event"); got {
		case "webhook.test":
			w.WriteHeader(http.StatusAccepted)
			return
		case "ticket.created":
			ticketEventSeen.Store(true)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/webhooks", map[string]any{
		"name":   "product-hook",
		"url":    target.URL,
		"events": []string{"ticket.created"},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	hookID := decodeNestedInt64(t, body, "webhook", "id")
	createdSecret := decodeString(t, body, "secret")
	if hookID == 0 || createdSecret == "" {
		t.Fatalf("webhook create response = %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(productID)+"/webhooks", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("product-hook")) {
		t.Fatalf("product webhooks missing hook: %s", body)
	}
	if bytes.Contains(body, []byte(createdSecret)) {
		t.Fatalf("product webhooks leaked secret: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/webhooks/"+itoa(hookID)+"/test", nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if webhookHits.Load() != 1 || !signatureSeen.Load() {
		t.Fatalf("webhook hits=%d signature=%v", webhookHits.Load(), signatureSeen.Load())
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id": productID,
		"title":      "Webhook-backed ticket",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	eventually(t, func() bool {
		return webhookHits.Load() >= 2 && ticketEventSeen.Load()
	}, "ticket webhook was not delivered")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/webhooks/"+itoa(hookID)+"/secret", nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	rotatedSecret := decodeString(t, body, "secret")
	if rotatedSecret == "" || rotatedSecret == createdSecret {
		t.Fatalf("webhook rotate response = %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(productID)+"/webhook-deliveries", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"status_code":202`)) {
		t.Fatalf("product deliveries missing test delivery: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/webhook-deliveries", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/webhooks/"+itoa(hookID), map[string]any{
		"name":    "renamed-hook",
		"enabled": false,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("renamed-hook")) || !bytes.Contains(body, []byte(`"enabled":false`)) {
		t.Fatalf("webhook patch response = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/webhooks/"+itoa(hookID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
}

func TestWebhookNotificationsUseNotificationDelay(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
		NotificationDelay:     time.Hour,
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	var webhookHits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/webhooks", map[string]any{
		"name":   "delayed-product-hook",
		"url":    target.URL,
		"events": []string{"ticket.created"},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id": productID,
		"title":      "Delayed webhook ticket",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	if webhookHits.Load() != 0 {
		t.Fatalf("webhook was delivered before notification delay")
	}
	waitForDomainEvents(t, tracker)
	claimed, err := tracker.ClaimWebhookNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim webhook notifications: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed delayed webhook notification too early: %#v", claimed)
	}
}

func TestWebhookNotificationsCoalescePendingTicketUpdates(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	app := NewServer(tracker, Options{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
		NotificationDelay:     time.Hour,
	})
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)
	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	delivered := make(chan []byte, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		delivered <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/webhooks", map[string]any{
		"name":   "coalesced-product-hook",
		"url":    target.URL,
		"events": []string{"ticket.updated"},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id": productID,
		"title":      "Webhook coalesce ticket",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"title": "First webhook update",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"title": "Second webhook update",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)

	if err := app.dispatchPendingEvents(context.Background(), 10); err != nil {
		t.Fatalf("project coalesced webhook events: %v", err)
	}
	makePendingWebhookNotificationsDue(t, tracker)
	if err := app.dispatchPendingWebhookNotifications(context.Background(), 10); err != nil {
		t.Fatalf("dispatch coalesced webhook: %v", err)
	}
	select {
	case payload := <-delivered:
		if !bytes.Contains(payload, []byte("Second webhook update")) || bytes.Contains(payload, []byte("First webhook update")) {
			t.Fatalf("coalesced webhook payload = %s", payload)
		}
		var envelope struct {
			Actor map[string]json.RawMessage `json:"actor"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatalf("decode webhook payload: %v", err)
		}
		if _, ok := envelope.Actor["id"]; !ok {
			t.Fatalf("webhook actor has no id: %s", payload)
		}
		for _, field := range []string{"disabled", "password_reset_required", "created_at", "updated_at"} {
			if _, ok := envelope.Actor[field]; ok {
				t.Fatalf("webhook actor contains non-snapshot field %q: %s", field, payload)
			}
		}
	default:
		t.Fatalf("coalesced webhook was not delivered")
	}
	select {
	case payload := <-delivered:
		t.Fatalf("duplicate webhook delivery payload = %s", payload)
	default:
	}
}

func TestDomainEventProjectionDoesNotDuplicateWebhookNotifications(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	app := NewServer(tracker, Options{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
		NotificationDelay:     -time.Second,
	})
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)
	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	var webhookHits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/webhooks", map[string]any{
		"name":   "projection-hook",
		"url":    target.URL,
		"events": []string{"ticket.created"},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id": productID,
		"title":      "Projection ticket",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	if err := app.dispatchPendingEvents(context.Background(), 10); err != nil {
		t.Fatalf("dispatch pending events: %v", err)
	}
	if err := app.dispatchPendingWebhookNotifications(context.Background(), 10); err != nil {
		t.Fatalf("dispatch pending webhooks: %v", err)
	}
	if webhookHits.Load() != 1 {
		t.Fatalf("webhook deliveries = %d, want 1", webhookHits.Load())
	}

	if err := app.dispatchPendingEvents(context.Background(), 10); err != nil {
		t.Fatalf("dispatch pending events again: %v", err)
	}
	if err := app.dispatchPendingWebhookNotifications(context.Background(), 10); err != nil {
		t.Fatalf("dispatch pending webhooks: %v", err)
	}
	if webhookHits.Load() != 1 {
		t.Fatalf("duplicate webhook deliveries = %d", webhookHits.Load())
	}
}
