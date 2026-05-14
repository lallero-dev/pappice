package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"pemmece/internal/store"
)

func TestSetupRequiresHTTPS(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	server := httptest.NewServer(New(tracker))
	defer server.Close()

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/setup", map[string]any{
		"username": "admin",
		"password": "correct horse",
	}, nil, "", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", resp.StatusCode, body)
	}
}

func TestProjectRBACAndCSRF(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	server := httptest.NewTLSServer(New(tracker))
	defer server.Close()
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	setupResp, setupBody := doJSON(t, client, http.MethodPost, server.URL+"/api/setup", map[string]any{
		"username": "admin",
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, setupResp, setupBody, http.StatusCreated)
	adminCookie := setupResp.Cookies()[0]
	adminCSRF := decodeString(t, setupBody, "csrf_token")

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/projects", map[string]any{
		"key":  "OPS",
		"name": "Operations",
	}, adminCookie, "", server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/projects", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	projectID := decodeFirstProjectID(t, body)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/users", map[string]any{
		"username": "bob",
		"email":    "bob@example.test",
		"password": "correct horse",
		"role":     "staff",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	bobID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/members", map[string]any{
		"user_id": bobID,
		"role":    "viewer",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	loginResp, loginBody := doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"username": "bob",
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, loginResp, loginBody, http.StatusOK)
	bobCookie := loginResp.Cookies()[0]
	bobCSRF := decodeString(t, loginBody, "csrf_token")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/tickets", map[string]any{
		"title": "Viewer cannot create",
	}, bobCookie, bobCSRF, server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/members", map[string]any{
		"user_id": bobID,
		"role":    "customer",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/tickets", map[string]any{
		"title": "Customer can create",
	}, bobCookie, bobCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	issueID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(issueID), map[string]any{
		"status": "assigned",
	}, bobCookie, bobCSRF, server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer patch status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/members", map[string]any{
		"user_id": bobID,
		"role":    "agent",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(issueID), map[string]any{
		"status": "assigned",
	}, bobCookie, bobCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
}

func TestSessionAssetsTokensAndLogoutFlow(t *testing.T) {
	tracker, server, client := newTestServer(t)
	_ = tracker

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pemmece")) {
		t.Fatalf("index body missing app name: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/support", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pemmece")) || bytes.Contains(body, []byte("support-portal")) {
		t.Fatalf("support route should serve the main app: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/missing", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/health", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"customer"`)) {
		t.Fatalf("health should expose customer role: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/session", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeBool(t, body, "needs_setup"); !got {
		t.Fatalf("session needs_setup = false before setup: %s", body)
	}

	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/session", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeBool(t, body, "authenticated"); !got {
		t.Fatalf("session authenticated = false after setup: %s", body)
	}
	if got := decodeString(t, body, "csrf_token"); got == "" {
		t.Fatalf("session csrf missing: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"username": "admin",
		"password": "wrong password",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusUnauthorized)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tokens", map[string]any{"name": "cli"}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	tokenValue := decodeString(t, body, "value")
	tokenID := decodeNestedInt64(t, body, "token", "id")
	if tokenValue == "" || tokenID == 0 {
		t.Fatalf("token response = %s", body)
	}
	resp, body = doJSONBearer(t, client, http.MethodGet, server.URL+"/api/projects", nil, tokenValue)
	requireStatus(t, resp, body, http.StatusOK)

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/tokens/"+itoa(tokenID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSONBearer(t, client, http.MethodGet, server.URL+"/api/projects", nil, tokenValue)
	requireStatus(t, resp, body, http.StatusUnauthorized)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/logout", nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/session", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeBool(t, body, "authenticated"); got {
		t.Fatalf("session authenticated after logout: %s", body)
	}
}

func TestAdminProjectIssueCommentAndNotificationFlow(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{EmailNotifications: true})
	_ = tracker
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/projects", map[string]any{
		"key":         "OPS",
		"name":        "Operations",
		"description": "Ops product",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	projectID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/projects/"+itoa(projectID), map[string]any{
		"name":        "Operations Desk",
		"description": "Client operations",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeString(t, body, "name"); got != "Operations Desk" {
		t.Fatalf("project name = %q body=%s", got, body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/projects/"+itoa(projectID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "dev",
		"password": "correct horse",
		"email":    "dev@example.test",
		"role":     "staff",
	})
	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "customer",
		"password": "correct horse",
		"email":    "customer@example.test",
		"role":     "customer",
	})
	disabledID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "disabled",
		"password": "correct horse",
		"email":    "disabled@example.test",
		"role":     "staff",
	})
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/users/"+itoa(disabledID), map[string]any{"disabled": true}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/users/"+itoa(disabledID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/users", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)

	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, devID, "agent")
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, customerID, "customer")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/projects/"+itoa(projectID)+"/members", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("agent")) || !bytes.Contains(body, []byte("customer")) {
		t.Fatalf("members missing roles: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"project_id":  projectID,
		"title":       "Dashboard fails",
		"description": "The dashboard cannot load",
		"priority":    "high",
		"tags":        []string{"dashboard", "customer"},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	issueID := decodeInt64(t, body, "id")

	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(issueID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)

	devCookie, devCSRF := loginUser(t, client, server.URL, "dev", "correct horse")
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(issueID)+"/comments", map[string]any{
		"body":       "I can reproduce this",
		"visibility": "public",
	}, devCookie, devCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(issueID)+"/comments", map[string]any{
		"body":       "Customer tries an internal note",
		"visibility": "internal",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(issueID)+"/comments", map[string]any{
		"body":       "Internal triage",
		"visibility": "internal",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(issueID), map[string]any{
		"status":   "assigned",
		"assignee": "dev",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?project_id="+itoa(projectID)+"&status=assigned&assignee=dev&q=dashboard", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Dashboard fails")) {
		t.Fatalf("filtered tickets missing ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(issueID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Internal triage")) || !bytes.Contains(body, []byte(`"visibility":"internal"`)) {
		t.Fatalf("ticket body missing internal note: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("ticket.assigned")) || !bytes.Contains(body, []byte("dev@example.test")) {
		t.Fatalf("email outbox missing assignment notification: %s", body)
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

	blocking := httptest.NewTLSServer(New(tracker))
	defer blocking.Close()
	client := blocking.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	setupResp, setupBody := doJSON(t, client, http.MethodPost, blocking.URL+"/api/setup", map[string]any{
		"username": "admin",
		"password": "correct horse",
	}, nil, "", "")
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
	permissive := httptest.NewTLSServer(New(permissiveStore, Options{AllowInsecureWebhooks: true, AllowPrivateWebhooks: true}))
	defer permissive.Close()
	permissiveClient := permissive.Client()
	permissiveClient.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	setupResp, setupBody = doJSON(t, permissiveClient, http.MethodPost, permissive.URL+"/api/setup", map[string]any{
		"username": "admin",
		"password": "correct horse",
	}, nil, "", "")
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
}

func TestWebhookDeliveryFlow(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
	})
	_ = tracker
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/projects", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	projectID := decodeFirstProjectID(t, body)

	var webhookHits atomic.Int64
	var signatureSeen atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookHits.Add(1)
		if strings.HasPrefix(r.Header.Get("X-Pemmece-Signature"), "sha256=") {
			signatureSeen.Store(true)
		}
		if got := r.Header.Get("X-Pemmece-Event"); got == "webhook.test" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/webhooks", map[string]any{
		"name":   "project-hook",
		"url":    target.URL,
		"events": []string{"ticket.created"},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	hookID := decodeNestedInt64(t, body, "webhook", "id")
	if hookID == 0 || decodeString(t, body, "secret") == "" {
		t.Fatalf("webhook create response = %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/projects/"+itoa(projectID)+"/webhooks", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("project-hook")) {
		t.Fatalf("project webhooks missing hook: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/webhooks/"+itoa(hookID)+"/test", nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if webhookHits.Load() != 1 || !signatureSeen.Load() {
		t.Fatalf("webhook hits=%d signature=%v", webhookHits.Load(), signatureSeen.Load())
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/projects/"+itoa(projectID)+"/webhook-deliveries", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"status_code":202`)) {
		t.Fatalf("project deliveries missing test delivery: %s", body)
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

func TestRegisteredCustomerTicketFlow(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	_ = tracker
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/support/projects", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusUnauthorized)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/projects", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	projectID := decodeFirstProjectID(t, body)

	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username":     "customer",
		"display_name": "Customer",
		"email":        "customer@example.test",
		"password":     "correct horse",
		"role":         "customer",
	})
	intruderID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "intruder",
		"email":    "intruder@example.test",
		"password": "correct horse",
		"role":     "customer",
	})
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, customerID, "customer")
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, intruderID, "customer")

	loginResp, loginBody := doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"username": "customer",
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, loginResp, loginBody, http.StatusOK)
	customerCookie := loginResp.Cookies()[0]
	customerCSRF := decodeString(t, loginBody, "csrf_token")

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/projects", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	projectID = decodeFirstProjectID(t, body)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"project_id":  projectID,
		"title":       "Missing CSRF",
		"description": "This should fail",
	}, customerCookie, "", server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"project_id":  projectID,
		"title":       "Need help",
		"description": "Something is wrong",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	var created struct {
		ID             int64  `json:"id"`
		Key            string `json:"key"`
		Title          string `json:"title"`
		Source         string `json:"source"`
		RequesterEmail string `json:"requester_email"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created ticket: %v", err)
	}
	if created.ID == 0 || created.Key == "" || created.Source != "portal" || created.RequesterEmail != "customer@example.test" {
		t.Fatalf("created ticket = %#v", created)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(created.ID), nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusUnauthorized)

	intruderCookie, intruderCSRF := loginUser(t, client, server.URL, "intruder", "correct horse")
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"project_id":  projectID,
		"title":       "Intruder issue",
		"description": "Another customer ticket",
	}, intruderCookie, intruderCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Need help")) || bytes.Contains(body, []byte("Intruder issue")) {
		t.Fatalf("customer ticket list has wrong visibility: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(created.ID), nil, intruderCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(created.ID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/comments", map[string]any{
		"body":       "Adding more detail",
		"visibility": "public",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	if !bytes.Contains(body, []byte("Adding more detail")) {
		t.Fatalf("ticket comment missing from body=%s", body)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/comments", map[string]any{
		"body":       "Customer internal note",
		"visibility": "internal",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/comments", map[string]any{
		"body":       "Private staff note",
		"visibility": "internal",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/comments", map[string]any{
		"body":       "Public staff reply",
		"visibility": "public",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(created.ID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Private staff note")) {
		t.Fatalf("customer ticket leaked internal note: %s", body)
	}
	if !bytes.Contains(body, []byte("Public staff reply")) {
		t.Fatalf("customer ticket missing public reply: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("customer@example.test")) ||
		!bytes.Contains(body, []byte("Replies to this email are not read")) ||
		!bytes.Contains(body, []byte("https://tracker.example.test/")) ||
		bytes.Contains(body, []byte("/support/tickets/")) {
		t.Fatalf("outbox missing no-reply customer notification: %s", body)
	}
	if bytes.Contains(body, []byte("Private staff note")) {
		t.Fatalf("outbox leaked internal note: %s", body)
	}
}

func newTestServer(t *testing.T, opts ...Options) (*store.Store, *httptest.Server, *http.Client) {
	t.Helper()
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	server := httptest.NewTLSServer(New(tracker, opts...))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return tracker, server, client
}

func setupAdmin(t *testing.T, client *http.Client, baseURL, username, email string) (*http.Cookie, string) {
	t.Helper()
	payload := map[string]any{
		"username": username,
		"password": "correct horse",
	}
	if email != "" {
		payload["email"] = email
	}
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/setup", payload, nil, "", "")
	requireStatus(t, resp, body, http.StatusCreated)
	if len(resp.Cookies()) == 0 {
		t.Fatalf("setup response did not set cookie: %s", body)
	}
	return resp.Cookies()[0], decodeString(t, body, "csrf_token")
}

func loginUser(t *testing.T, client *http.Client, baseURL, username, password string) (*http.Cookie, string) {
	t.Helper()
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/login", map[string]any{
		"username": username,
		"password": password,
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if len(resp.Cookies()) == 0 {
		t.Fatalf("login response did not set cookie: %s", body)
	}
	return resp.Cookies()[0], decodeString(t, body, "csrf_token")
}

func createUser(t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, csrf string, payload map[string]any) int64 {
	t.Helper()
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/users", payload, cookie, csrf, baseURL)
	requireStatus(t, resp, body, http.StatusCreated)
	return decodeInt64(t, body, "id")
}

func addProjectMember(t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, csrf string, projectID, userID int64, role string) {
	t.Helper()
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/projects/"+itoa(projectID)+"/members", map[string]any{
		"user_id": userID,
		"role":    role,
	}, cookie, csrf, baseURL)
	requireStatus(t, resp, body, http.StatusCreated)
}

func doJSON(t *testing.T, client *http.Client, method, rawURL string, payload any, cookie *http.Cookie, csrf, origin string) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		content, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(content)
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-Pemmece-CSRF", csrf)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, data
}

func doJSONBearer(t *testing.T, client *http.Client, method, rawURL string, payload any, token string) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		content, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(content)
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, data
}

func requireStatus(t *testing.T, resp *http.Response, body []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d body=%s, want %d", resp.StatusCode, body, want)
	}
}

func decodeBool(t *testing.T, body []byte, key string) bool {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	value, _ := payload[key].(bool)
	return value
}

func decodeString(t *testing.T, body []byte, key string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	value, _ := payload[key].(string)
	return value
}

func decodeInt64(t *testing.T, body []byte, key string) int64 {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	value, _ := payload[key].(float64)
	return int64(value)
}

func decodeNestedInt64(t *testing.T, body []byte, parent, key string) int64 {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	nested, _ := payload[parent].(map[string]any)
	value, _ := nested[key].(float64)
	return int64(value)
}

func decodeFirstProjectID(t *testing.T, body []byte) int64 {
	t.Helper()
	var payload struct {
		Projects []store.Project `json:"projects"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(payload.Projects) == 0 {
		t.Fatal("no projects returned")
	}
	return payload.Projects[0].ID
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
