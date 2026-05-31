package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"pappice/internal/store"
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

func TestSecurityHeadersAllowBlobImagePreviews(t *testing.T) {
	_, server, client := newTestServer(t)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/health", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"img-src 'self' blob:",
		"object-src 'none'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP %q missing %q", csp, want)
		}
	}
	if resp.Header.Get("Strict-Transport-Security") == "" {
		t.Fatalf("TLS response missing HSTS header")
	}
}

func TestProductRBACAndCSRF(t *testing.T) {
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

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "OPS",
		"name": "Operations",
	}, adminCookie, "", server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	bobID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "bob",
		"email":    "bob@example.test",
		"password": "correct horse",
		"role":     "staff",
	})

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/members", map[string]any{
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

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/tickets", map[string]any{
		"title": "Viewer cannot create",
	}, bobCookie, bobCSRF, server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/members", map[string]any{
		"user_id": bobID,
		"role":    "customer",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/tickets", map[string]any{
		"title": "Customer can create",
	}, bobCookie, bobCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"status": "assigned",
	}, bobCookie, bobCSRF, server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer patch status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/members", map[string]any{
		"user_id": bobID,
		"role":    "agent",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"status": "assigned",
	}, bobCookie, bobCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
}

func TestSessionAssetsTokensAndLogoutFlow(t *testing.T) {
	tracker, server, client := newTestServer(t)
	_ = tracker

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pappice")) {
		t.Fatalf("index body missing app name: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/tickets", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pappice")) {
		t.Fatalf("tickets route should serve the main app: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/tickets/", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/admin/accounts", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pappice")) {
		t.Fatalf("admin route should serve the main app: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/admin/products", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/products", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pappice")) {
		t.Fatalf("products route should serve the main app: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/products/1", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pappice")) {
		t.Fatalf("product route should serve the main app: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/products/1/webhooks", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pappice")) {
		t.Fatalf("product section route should serve the main app: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/products/1/unknown", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/missing", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/support", nil, nil, "", "")
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

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/logout", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusMethodNotAllowed)
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("logout Allow header = %q, want POST", allow)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":        "BAD",
		"name":       "Bad payload",
		"unexpected": true,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)

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
	resp, body = doJSONBearer(t, client, http.MethodGet, server.URL+"/api/products", nil, tokenValue)
	requireStatus(t, resp, body, http.StatusOK)

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/tokens/"+itoa(tokenID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSONBearer(t, client, http.MethodGet, server.URL+"/api/products", nil, tokenValue)
	requireStatus(t, resp, body, http.StatusUnauthorized)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/logout", nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/session", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeBool(t, body, "authenticated"); got {
		t.Fatalf("session authenticated after logout: %s", body)
	}
}

func TestAPIMethodContracts(t *testing.T) {
	_, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	userID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "methodstaff",
		"email":    "methodstaff@example.test",
		"password": "correct horse",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, userID, "agent")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Method contract",
		"description": "Exercise route methods",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tokens", map[string]any{"name": "method-contract"}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	tokenID := decodeNestedInt64(t, body, "token", "id")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/webhooks", map[string]any{
		"name": "global-method-contract",
		"url":  "https://example.com/pappice-hook",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	hookID := decodeNestedInt64(t, body, "webhook", "id")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/email-notifications/test", map[string]any{}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	notificationID := decodeNestedInt64(t, body, "notification", "id")

	tests := []struct {
		name   string
		method string
		path   string
		allow  string
	}{
		{"health", http.MethodPost, "/api/health", http.MethodGet},
		{"session", http.MethodPost, "/api/session", http.MethodGet},
		{"me", http.MethodPost, "/api/me", "GET, PATCH"},
		{"me password", http.MethodGet, "/api/me/password", http.MethodPost},
		{"setup", http.MethodGet, "/api/setup", http.MethodPost},
		{"login", http.MethodGet, "/api/login", http.MethodPost},
		{"logout", http.MethodGet, "/api/logout", http.MethodPost},
		{"account link", http.MethodPut, "/api/account-links/not-a-real-token", "GET, POST"},
		{"products", http.MethodPut, "/api/products", "GET, POST"},
		{"single product", http.MethodPost, "/api/products/" + itoa(productID), "GET, PATCH, DELETE"},
		{"product members", http.MethodPut, "/api/products/" + itoa(productID) + "/members", "GET, POST"},
		{"product tickets", http.MethodPut, "/api/products/" + itoa(productID) + "/tickets", "GET, POST"},
		{"product webhooks", http.MethodPut, "/api/products/" + itoa(productID) + "/webhooks", "GET, POST"},
		{"product deliveries", http.MethodPost, "/api/products/" + itoa(productID) + "/webhook-deliveries", http.MethodGet},
		{"tickets", http.MethodPut, "/api/tickets", "GET, POST"},
		{"single ticket", http.MethodPost, "/api/tickets/" + itoa(ticketID), "GET, PATCH, DELETE"},
		{"ticket comments", http.MethodGet, "/api/tickets/" + itoa(ticketID) + "/comments", http.MethodPost},
		{"ticket read", http.MethodGet, "/api/tickets/" + itoa(ticketID) + "/read", http.MethodPost},
		{"attachments", http.MethodPost, "/api/attachments/1", http.MethodGet},
		{"users", http.MethodPut, "/api/users", "GET, POST"},
		{"single user", http.MethodGet, "/api/users/" + itoa(userID), "PATCH, DELETE"},
		{"password reset", http.MethodGet, "/api/users/" + itoa(userID) + "/password-reset", http.MethodPost},
		{"tokens", http.MethodPut, "/api/tokens", "GET, POST"},
		{"single token", http.MethodGet, "/api/tokens/" + itoa(tokenID), http.MethodDelete},
		{"global webhooks", http.MethodPut, "/api/webhooks", "GET, POST"},
		{"single webhook", http.MethodGet, "/api/webhooks/" + itoa(hookID), "PATCH, DELETE"},
		{"webhook test", http.MethodGet, "/api/webhooks/" + itoa(hookID) + "/test", http.MethodPost},
		{"webhook secret", http.MethodGet, "/api/webhooks/" + itoa(hookID) + "/secret", http.MethodPost},
		{"webhook deliveries", http.MethodPost, "/api/webhook-deliveries", http.MethodGet},
		{"email notifications", http.MethodPost, "/api/email-notifications", http.MethodGet},
		{"email retry", http.MethodGet, "/api/email-notifications/" + itoa(notificationID) + "/retry", http.MethodPost},
		{"audit", http.MethodPost, "/api/audit-events", http.MethodGet},
		{"maintenance", http.MethodPost, "/api/admin/maintenance", http.MethodGet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			csrf, origin := "", ""
			if isUnsafeMethod(tt.method) {
				csrf = adminCSRF
				origin = server.URL
			}
			resp, body := doJSON(t, client, tt.method, server.URL+tt.path, nil, adminCookie, csrf, origin)
			requireStatus(t, resp, body, http.StatusMethodNotAllowed)
			if got := resp.Header.Get("Allow"); got != tt.allow {
				t.Fatalf("Allow = %q body=%s, want %q", got, body, tt.allow)
			}
		})
	}
}

func TestAPIAuthAndCSRFContracts(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/tokens", map[string]any{"name": "contract"}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	tokenValue := decodeString(t, body, "value")

	for _, path := range []string{
		"/api/products",
		"/api/tickets",
		"/api/users",
		"/api/tokens",
		"/api/webhook-deliveries",
		"/api/email-notifications",
		"/api/audit-events",
		"/api/admin/maintenance",
		"/api/attachments/1",
	} {
		t.Run("unauthenticated "+path, func(t *testing.T) {
			resp, body := doJSON(t, client, http.MethodGet, server.URL+path, nil, nil, "", "")
			requireStatus(t, resp, body, http.StatusUnauthorized)
		})
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "NO-CSRF",
		"name": "Missing token",
	}, adminCookie, "", server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)
	if !bytes.Contains(body, []byte("valid CSRF token")) {
		t.Fatalf("missing csrf response = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "BAD-ORIGIN",
		"name": "Bad origin",
	}, adminCookie, adminCSRF, "https://evil.example.test")
	requireStatus(t, resp, body, http.StatusForbidden)
	if !bytes.Contains(body, []byte("same-origin")) {
		t.Fatalf("bad origin response = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "BAD-CSRF",
		"name": "Bad token",
	}, adminCookie, "wrong-token", server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)

	resp, body = doJSONBearer(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "API",
		"name": "Created by API token",
	}, tokenValue)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSONBearer(t, client, http.MethodPost, server.URL+"/api/me/password", map[string]any{
		"current_password": "correct horse",
		"new_password":     "better password",
	}, tokenValue)
	requireStatus(t, resp, body, http.StatusForbidden)
	if !bytes.Contains(body, []byte("browser session")) {
		t.Fatalf("token password change response = %s", body)
	}
}

func TestProductDeletionRequiresAdmin(t *testing.T) {
	tracker, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	ownerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "owner",
		"password": "correct horse",
		"email":    "owner@example.test",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, ownerID, "owner")
	ownerCookie, ownerCSRF := loginUser(t, client, server.URL, "owner", "correct horse")

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/products/"+itoa(productID), nil, ownerCookie, ownerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)
	if _, err := tracker.GetProduct(productID); err != nil {
		t.Fatalf("owner delete removed product: %v", err)
	}

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/products/"+itoa(productID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if !decodeBool(t, body, "ok") {
		t.Fatalf("delete product response = %s", body)
	}
	if _, err := tracker.GetProduct(productID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted product err = %v, want not found", err)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(productID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
}

func TestTicketDeletionRequiresAdmin(t *testing.T) {
	tracker, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	ownerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "owner",
		"password": "correct horse",
		"email":    "owner@example.test",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, ownerID, "owner")
	ownerCookie, ownerCSRF := loginUser(t, client, server.URL, "owner", "correct horse")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id": productID,
		"title":      "Delete from admin only",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/tickets/"+itoa(ticketID), nil, ownerCookie, ownerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)
	if _, err := tracker.GetTicket(ticketID); err != nil {
		t.Fatalf("owner delete removed ticket: %v", err)
	}

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/tickets/"+itoa(ticketID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if !decodeBool(t, body, "ok") {
		t.Fatalf("delete ticket response = %s", body)
	}
	if _, err := tracker.GetTicket(ticketID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted ticket err = %v, want not found", err)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(ticketID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/audit-events?q=ticket.deleted", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("ticket.deleted")) {
		t.Fatalf("audit log missing ticket deletion: %s", body)
	}
}

func TestAPIValidationContracts(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	userID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "validationstaff",
		"email":    "validationstaff@example.test",
		"password": "correct horse",
		"role":     "staff",
	})

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id": productID,
		"title":      "Validation ticket",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"malformed json", http.MethodPost, "/api/products", `{"key":`, http.StatusBadRequest},
		{"unknown json field", http.MethodPost, "/api/products", `{"key":"BAD","name":"Bad","extra":true}`, http.StatusBadRequest},
		{"multiple json documents", http.MethodPost, "/api/products", `{"key":"BAD","name":"Bad"} {}`, http.StatusBadRequest},
		{"invalid product id", http.MethodGet, "/api/products/not-a-number", ``, http.StatusBadRequest},
		{"invalid ticket id", http.MethodGet, "/api/tickets/not-a-number", ``, http.StatusBadRequest},
		{"invalid token id", http.MethodDelete, "/api/tokens/not-a-number", ``, http.StatusBadRequest},
		{"invalid email notification id", http.MethodPost, "/api/email-notifications/not-a-number/retry", `{}`, http.StatusBadRequest},
		{"empty ticket patch", http.MethodPatch, "/api/tickets/" + itoa(ticketID), `{}`, http.StatusBadRequest},
		{"direct password patch blocked", http.MethodPatch, "/api/users/" + itoa(userID), `{"password":"new password"}`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			csrf, origin := "", ""
			if isUnsafeMethod(tt.method) {
				csrf = adminCSRF
				origin = server.URL
			}
			resp, body := doRawJSON(t, client, tt.method, server.URL+tt.path, tt.body, adminCookie, csrf, origin)
			requireStatus(t, resp, body, tt.want)
			if !bytes.Contains(body, []byte("error")) {
				t.Fatalf("validation response missing error field: %s", body)
			}
		})
	}

	disabledTracker, disabledServer, disabledClient := newTestServer(t)
	_ = disabledTracker
	disabledCookie, disabledCSRF := setupAdmin(t, disabledClient, disabledServer.URL, "admin", "admin@example.test")
	resp, body = doJSON(t, disabledClient, http.MethodPost, disabledServer.URL+"/api/email-notifications/test", map[string]any{}, disabledCookie, disabledCSRF, disabledServer.URL)
	requireStatus(t, resp, body, http.StatusConflict)
	if !bytes.Contains(body, []byte("email notifications are not configured")) {
		t.Fatalf("disabled email test response = %s", body)
	}
}

func TestHealthExposesBranding(t *testing.T) {
	_, server, client := newTestServer(t, Options{Branding: Branding{
		Name:     "Acme Support",
		Subtitle: "support desk",
		Mark:     "AS",
		Color:    "#111827",
	}})

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/health", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeNestedString(t, body, "branding", "name"); got != "Acme Support" {
		t.Fatalf("branding.name = %q body=%s", got, body)
	}
	if got := decodeNestedString(t, body, "branding", "subtitle"); got != "support desk" {
		t.Fatalf("branding.subtitle = %q body=%s", got, body)
	}
	if got := decodeNestedString(t, body, "branding", "mark"); got != "AS" {
		t.Fatalf("branding.mark = %q body=%s", got, body)
	}
	if got := decodeNestedString(t, body, "branding", "color"); got != "#111827" {
		t.Fatalf("branding.color = %q body=%s", got, body)
	}
}

func TestAdminMaintenanceEndpoint(t *testing.T) {
	backupDir := filepath.Join(t.TempDir(), "backups")
	latestBackup := filepath.Join(backupDir, "20260101T120000Z")
	if err := os.MkdirAll(latestBackup, 0o755); err != nil {
		t.Fatalf("create backup dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestBackup, "pappice.db"), []byte("backup"), 0o600); err != nil {
		t.Fatalf("write backup db marker: %v", err)
	}
	_, server, client := newTestServer(t, Options{
		EmailNotifications:   true,
		PublicURL:            "https://tracker.example.test",
		UploadDir:            filepath.Join(t.TempDir(), "uploads"),
		BackupDir:            backupDir,
		DomainEventRetention: 48 * time.Hour,
		Version:              "test-version",
	})
	adminCookie, _ := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/admin/maintenance", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"version":"test-version"`)) ||
		!bytes.Contains(body, []byte(`"database_path"`)) ||
		!bytes.Contains(body, []byte(`"upload_path"`)) ||
		!bytes.Contains(body, []byte(`"path":"`+backupDir+`"`)) ||
		!bytes.Contains(body, []byte(`"latest_name":"20260101T120000Z"`)) ||
		!bytes.Contains(body, []byte(`"domain_event_retention_seconds":172800`)) ||
		!bytes.Contains(body, []byte(`"enabled":true`)) ||
		!bytes.Contains(body, []byte(`"public_url":"https://tracker.example.test"`)) {
		t.Fatalf("maintenance response = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/admin/maintenance", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusUnauthorized)
}

func TestAccountSetupAndResetLinks(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/users", map[string]any{
		"username":     "pending",
		"display_name": "Pending User",
		"email":        "pending@example.test",
		"role":         "staff",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	userID := decodeInt64(t, body, "id")
	setupURL := decodeNestedString(t, body, "account_link", "url")
	if !strings.HasPrefix(setupURL, "https://tracker.example.test/account/setup/") ||
		!bytes.Contains(body, []byte(`"email_queued":true`)) ||
		!bytes.Contains(body, []byte(`"password_reset_required":true`)) {
		t.Fatalf("create user account link response = %s", body)
	}
	setupToken := accountLinkTokenFromURL(t, setupURL)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"username": "pending",
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusUnauthorized)
	if !bytes.Contains(body, []byte("password setup or reset is required")) {
		t.Fatalf("pending login error = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/account/setup/"+setupToken, nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Pappice")) {
		t.Fatalf("account setup route should serve app: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/account-links/"+setupToken, nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"purpose":"setup"`)) || !bytes.Contains(body, []byte(`"username":"pending"`)) {
		t.Fatalf("account setup link lookup = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+setupToken, map[string]any{
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if len(resp.Cookies()) == 0 {
		t.Fatalf("setup link did not create session: %s", body)
	}
	userCookie := resp.Cookies()[0]
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+setupToken, map[string]any{
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusGone)
	if !bytes.Contains(body, []byte("already been used")) || !bytes.Contains(body, []byte(`"reason":"used"`)) {
		t.Fatalf("used setup link response = %s", body)
	}
	loginUser(t, client, server.URL, "pending", "correct horse")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/users/"+itoa(userID)+"/password-reset", nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	resetURL := decodeNestedString(t, body, "account_link", "url")
	if !strings.Contains(resetURL, "/account/reset/") ||
		!bytes.Contains(body, []byte(`"email_queued":true`)) ||
		!bytes.Contains(body, []byte(`"password_reset_required":true`)) {
		t.Fatalf("password reset response = %s", body)
	}
	resetToken := accountLinkTokenFromURL(t, resetURL)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/session", nil, userCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if decodeBool(t, body, "authenticated") {
		t.Fatalf("old session still authenticated after reset: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"username": "pending",
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusUnauthorized)
	if !bytes.Contains(body, []byte("password setup or reset is required")) {
		t.Fatalf("old password reset-required error = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+resetToken, map[string]any{
		"password": "new correct horse",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	loginUser(t, client, server.URL, "pending", "new correct horse")

	_, _, expiredToken, err := tracker.CreatePasswordResetLink(userID, time.Nanosecond)
	if err != nil {
		t.Fatalf("create expired reset link: %v", err)
	}
	time.Sleep(time.Millisecond)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/account-links/"+expiredToken, nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusGone)
	if !bytes.Contains(body, []byte("expired")) || !bytes.Contains(body, []byte(`"reason":"expired"`)) {
		t.Fatalf("expired reset link response = %s", body)
	}
}

func TestProfileAndPasswordChangeFlow(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")
	staffID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username":     "staffer",
		"display_name": "Staffer",
		"email":        "staffer@example.test",
		"password":     "correct horse",
		"role":         "staff",
	})
	if staffID == 0 {
		t.Fatal("created staff id is zero")
	}

	staffCookie1, staffCSRF1 := loginUser(t, client, server.URL, "staffer", "correct horse")
	staffCookie2, _ := loginUser(t, client, server.URL, "staffer", "correct horse")

	resp, body := doJSON(t, client, http.MethodPatch, server.URL+"/api/me", map[string]any{
		"display_name": "Staff Person",
		"email":        "person@example.test",
	}, staffCookie1, staffCSRF1, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"display_name":"Staff Person"`)) || !bytes.Contains(body, []byte(`"email":"person@example.test"`)) {
		t.Fatalf("profile patch response = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/me/password", map[string]any{
		"current_password": "wrong password",
		"new_password":     "better password",
	}, staffCookie1, staffCSRF1, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/me/password", map[string]any{
		"current_password": "correct horse",
		"new_password":     "better password",
	}, staffCookie1, staffCSRF1, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeString(t, body, "csrf_token"); got == "" {
		t.Fatalf("password change csrf missing: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/session", nil, staffCookie1, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !decodeBool(t, body, "authenticated") {
		t.Fatalf("current session not kept after password change: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/session", nil, staffCookie2, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if decodeBool(t, body, "authenticated") {
		t.Fatalf("other session still authenticated after password change: %s", body)
	}
	loginUser(t, client, server.URL, "staffer", "better password")
}

func TestSecurityHardeningRateLimitsAuditAndSessionTTL(t *testing.T) {
	_, ttlServer, ttlClient := newTestServer(t, Options{SessionTTL: 20 * time.Millisecond})
	adminCookie, _ := setupAdmin(t, ttlClient, ttlServer.URL, "admin", "admin@example.test")
	time.Sleep(30 * time.Millisecond)
	resp, body := doJSON(t, ttlClient, http.MethodGet, ttlServer.URL+"/api/session", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if decodeBool(t, body, "authenticated") {
		t.Fatalf("short-lived session still authenticated: %s", body)
	}

	_, server, client := newTestServer(t, Options{
		LoginRateLimit:       RateLimit{Limit: 2, Window: time.Minute},
		AccountLinkRateLimit: RateLimit{Limit: 2, Window: time.Minute},
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	for attempt := 0; attempt < 2; attempt++ {
		resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
			"username": "missing",
			"password": "wrong password",
		}, nil, "", "")
		requireStatus(t, resp, body, http.StatusUnauthorized)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"username": "missing",
		"password": "wrong password",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusTooManyRequests)
	if retry := resp.Header.Get("Retry-After"); retry == "" {
		t.Fatalf("rate-limited response missing Retry-After: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/users", map[string]any{
		"username": "limited",
		"email":    "limited@example.test",
		"role":     "staff",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	token := accountLinkTokenFromURL(t, decodeNestedString(t, body, "account_link", "url"))
	for attempt := 0; attempt < 2; attempt++ {
		resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+token, map[string]any{
			"password": "short",
		}, nil, "", "")
		requireStatus(t, resp, body, http.StatusBadRequest)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+token, map[string]any{
		"password": "short",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusTooManyRequests)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/audit-events", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("setup.completed")) ||
		!bytes.Contains(body, []byte("user.created")) ||
		!bytes.Contains(body, []byte("limited")) {
		t.Fatalf("audit log missing setup/user events: %s", body)
	}
}

func TestAdminProductTicketCommentAndNotificationFlow(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{EmailNotifications: true})
	_ = tracker
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":         "OPS",
		"name":        "Operations",
		"description": "Ops product",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	productID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/products/"+itoa(productID), map[string]any{
		"name":        "Operations Desk",
		"description": "Client operations",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeString(t, body, "name"); got != "Operations Desk" {
		t.Fatalf("product name = %q body=%s", got, body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(productID), nil, adminCookie, "", "")
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

	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, devID, "agent")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(productID)+"/members", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("agent")) || !bytes.Contains(body, []byte("customer")) {
		t.Fatalf("members missing roles: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Dashboard fails",
		"description": "The dashboard cannot load",
		"priority":    "high",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")
	ticketKey := decodeString(t, body, "key")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/key/"+ticketKey, nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if decodeInt64(t, body, "id") != ticketID {
		t.Fatalf("ticket by key returned wrong ticket: %s", body)
	}

	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(ticketID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)

	devCookie, devCSRF := loginUser(t, client, server.URL, "dev", "correct horse")
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(ticketID)+"/comments", map[string]any{
		"body":       "I can reproduce this",
		"visibility": "public",
	}, devCookie, devCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(ticketID)+"/comments", map[string]any{
		"body":       "Customer tries an internal note",
		"visibility": "internal",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(ticketID)+"/comments", map[string]any{
		"body":       "Internal triage",
		"visibility": "internal",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"status":   "assigned",
		"assignee": "dev",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?product_id="+itoa(productID)+"&status=new&status=assigned&assignee=dev&q=dashboard", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Dashboard fails")) {
		t.Fatalf("filtered tickets missing ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(ticketID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Internal triage")) || !bytes.Contains(body, []byte(`"visibility":"internal"`)) {
		t.Fatalf("ticket body missing internal note: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("ticket.updated")) ||
		!bytes.Contains(body, []byte("Ticket update")) ||
		!bytes.Contains(body, []byte("dev@example.test")) {
		t.Fatalf("email outbox missing grouped update notification: %s", body)
	}
}

func TestEmailNotificationAdminTools(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/email-notifications/test", map[string]any{}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	var created struct {
		Notification store.EmailNotification `json:"notification"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode test email: %v", err)
	}
	if created.Notification.ID == 0 || created.Notification.Event != "email.test" || created.Notification.RecipientEmail != "admin@example.test" {
		t.Fatalf("test email notification = %#v", created.Notification)
	}

	if err := tracker.MarkEmailFailed(created.Notification.ID, errors.New("smtp unavailable"), 1); err != nil {
		t.Fatalf("mark test email failed: %v", err)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"failed":1`)) || !bytes.Contains(body, []byte("smtp unavailable")) {
		t.Fatalf("email outbox missing failed overview: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/email-notifications/"+itoa(created.Notification.ID)+"/retry", nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	var retried struct {
		Notification store.EmailNotification `json:"notification"`
	}
	if err := json.Unmarshal(body, &retried); err != nil {
		t.Fatalf("decode retried email: %v", err)
	}
	if retried.Notification.Status != "pending" || retried.Notification.Attempts != 0 || retried.Notification.LastError != "" {
		t.Fatalf("retried notification = %#v", retried.Notification)
	}
}

func TestAdminHistoryPaginationAndFilters(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	for i := 0; i < 2; i++ {
		resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/email-notifications/test", map[string]any{}, adminCookie, adminCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusCreated)
	}
	notifications := tracker.ListEmailNotifications(10)
	if len(notifications) < 2 {
		t.Fatalf("test notifications = %#v", notifications)
	}
	if err := tracker.MarkEmailFailed(notifications[0].ID, errors.New("smtp unavailable"), 1); err != nil {
		t.Fatalf("mark email failed: %v", err)
	}

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications?limit=1&status=failed&q=smtp", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	var emailPage struct {
		Notifications []store.EmailNotification `json:"notifications"`
		Total         int                       `json:"total"`
		Limit         int                       `json:"limit"`
		Offset        int                       `json:"offset"`
	}
	if err := json.Unmarshal(body, &emailPage); err != nil {
		t.Fatalf("decode email page: %v", err)
	}
	if emailPage.Total != 1 || emailPage.Limit != 1 || emailPage.Offset != 0 || len(emailPage.Notifications) != 1 {
		t.Fatalf("email page = %#v body=%s", emailPage, body)
	}
	if emailPage.Notifications[0].Status != "failed" || emailPage.Notifications[0].LastError != "smtp unavailable" {
		t.Fatalf("filtered notification = %#v", emailPage.Notifications[0])
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/audit-events?limit=1&q=email_notification.test_queued", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	var auditPage struct {
		Events []store.AuditEvent `json:"events"`
		Total  int                `json:"total"`
		Limit  int                `json:"limit"`
		Offset int                `json:"offset"`
	}
	if err := json.Unmarshal(body, &auditPage); err != nil {
		t.Fatalf("decode audit page: %v", err)
	}
	if auditPage.Total != 2 || auditPage.Limit != 1 || auditPage.Offset != 0 || len(auditPage.Events) != 1 {
		t.Fatalf("audit page = %#v body=%s", auditPage, body)
	}
	if auditPage.Events[0].Action != "email_notification.test_queued" {
		t.Fatalf("filtered audit event = %#v", auditPage.Events[0])
	}
}

func TestTicketSaveGroupsWorkflowAndCommentEmail(t *testing.T) {
	_, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "dev",
		"password": "correct horse",
		"email":    "dev@example.test",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, devID, "agent")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Grouped save",
		"description": "Needs one email",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"status":   "assigned",
		"assignee": "dev",
		"comment": map[string]any{
			"body":       "This should roll back",
			"visibility": "private",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(ticketID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("This should roll back")) || bytes.Contains(body, []byte(`"status":"assigned"`)) {
		t.Fatalf("failed grouped save was not rolled back: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"status":   "assigned",
		"assignee": "dev",
		"comment": map[string]any{
			"body":       "Taking this now",
			"visibility": "public",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Taking this now")) || !bytes.Contains(body, []byte(`"status":"assigned"`)) {
		t.Fatalf("grouped ticket save response = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("ticket.updated")) ||
		!bytes.Contains(body, []byte("Ticket update")) ||
		!bytes.Contains(body, []byte("dev@example.test")) ||
		!bytes.Contains(body, []byte("Taking this now")) {
		t.Fatalf("email outbox missing grouped ticket update: %s", body)
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
		if err := server.validateWebhookTarget(target); err == nil {
			t.Fatalf("validateWebhookTarget(%q) succeeded, want private target error", target)
		}
	}
}

func TestWebhookDeliveryFlow(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
	})
	_ = tracker
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

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
	waitUntil(t, func() bool { return webhookHits.Load() >= 2 && ticketEventSeen.Load() })

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
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

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
	time.Sleep(50 * time.Millisecond)
	if webhookHits.Load() != 0 {
		t.Fatalf("webhook was delivered before notification delay")
	}
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
		NotificationDelay:     80 * time.Millisecond,
	})
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")
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

	time.Sleep(40 * time.Millisecond)
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"title": "Second webhook update",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)

	time.Sleep(100 * time.Millisecond)
	if err := app.dispatchPendingEvents(nil, 10); err != nil {
		t.Fatalf("dispatch coalesced webhook: %v", err)
	}
	select {
	case payload := <-delivered:
		if !bytes.Contains(payload, []byte("Second webhook update")) || bytes.Contains(payload, []byte("First webhook update")) {
			t.Fatalf("coalesced webhook payload = %s", payload)
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

	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")
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
	waitUntil(t, func() bool { return webhookHits.Load() == 1 })

	if err := app.dispatchPendingEvents(nil, 10); err != nil {
		t.Fatalf("dispatch pending events again: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if webhookHits.Load() != 1 {
		t.Fatalf("duplicate webhook deliveries = %d", webhookHits.Load())
	}
}

func TestRegisteredCustomerTicketFlow(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	_ = tracker
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/support/products", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

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
	noEmailID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "noemail",
		"password": "correct horse",
		"role":     "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, intruderID, "customer")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, noEmailID, "customer")

	noEmailCookie, noEmailCSRF := loginUser(t, client, server.URL, "noemail", "correct horse")
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "No email",
		"description": "Customer accounts need an email for support tickets",
	}, noEmailCookie, noEmailCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)

	loginResp, loginBody := doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"username": "customer",
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, loginResp, loginBody, http.StatusOK)
	customerCookie := loginResp.Cookies()[0]
	customerCSRF := decodeString(t, loginBody, "csrf_token")

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID = decodeFirstProductID(t, body)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Missing CSRF",
		"description": "This should fail",
	}, customerCookie, "", server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
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
		"product_id":  productID,
		"title":       "Intruder ticket",
		"description": "Another customer ticket",
	}, intruderCookie, intruderCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Need help")) || bytes.Contains(body, []byte("Intruder ticket")) {
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
	if !decodeBool(t, body, "has_unread") || decodeInt64(t, body, "unread_count") != 1 {
		t.Fatalf("customer ticket unread state missing staff public reply: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?unread=1", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Public staff reply")) {
		t.Fatalf("customer unread list missing ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/read", nil, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if decodeBool(t, body, "has_unread") || decodeInt64(t, body, "unread_count") != 0 {
		t.Fatalf("customer ticket should be read after mark-read: %s", body)
	}
	if decodeString(t, body, "last_read_at") == "" {
		t.Fatalf("customer ticket read response missing last_read_at: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?unread=1", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("customer unread list should be empty after mark-read: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(created.ID), map[string]any{
		"status": "resolved",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=new&status=assigned", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("explicit active status filter should hide read terminal ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=new&status=assigned&include_unread_outside_status=1", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Need help")) || !bytes.Contains(body, []byte(`"status":"resolved"`)) {
		t.Fatalf("default active view should include unread resolved ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/read", nil, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=new&status=assigned&include_unread_outside_status=1", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("default active view should hide read terminal ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=resolved", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("explicit resolved filter should show resolved ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Private staff note")) {
		t.Fatalf("customer ticket list leaked internal note: %s", body)
	}
	if !bytes.Contains(body, []byte("Public staff reply")) {
		t.Fatalf("customer ticket list missing public reply: %s", body)
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

func TestCustomerPermissionBoundaries(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "BILL",
		"name": "Billing",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	otherProductID := decodeInt64(t, body, "id")

	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username":     "customer",
		"display_name": "Customer",
		"email":        "customer@example.test",
		"password":     "correct horse",
		"role":         "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Customer-owned ticket",
		"description": "Customer-visible request",
		"priority":    "urgent",
		"assignee":    "admin",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	customerTicketID := decodeInt64(t, body, "id")
	var created store.Ticket
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode customer ticket: %v", err)
	}
	if created.Priority != "urgent" || created.Assignee != "" || created.Source != "portal" {
		t.Fatalf("customer-controlled fields were not normalized: %#v", created)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  otherProductID,
		"title":       "Wrong product",
		"description": "Customer is not a member here",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(otherProductID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  otherProductID,
		"title":       "Other product staff ticket",
		"description": "Customer must not see this",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	otherTicketID := decodeInt64(t, body, "id")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(otherTicketID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?product_id="+itoa(otherProductID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Other product staff ticket")) {
		t.Fatalf("customer ticket list leaked another product: %s", body)
	}

	for name, patch := range map[string]map[string]any{
		"status":      {"status": "assigned"},
		"priority":    {"priority": "low"},
		"assignee":    {"assignee": "admin"},
		"title":       {"title": "Customer renamed ticket"},
		"description": {"description": "Customer edited description"},
		"mixed": {
			"status": "assigned",
			"comment": map[string]any{
				"body":       "Should not persist",
				"visibility": "public",
			},
		},
	} {
		resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(customerTicketID), patch, customerCookie, customerCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusForbidden)
		t.Logf("blocked customer workflow patch %s", name)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(customerTicketID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Should not persist")) ||
		bytes.Contains(body, []byte("Customer renamed ticket")) ||
		bytes.Contains(body, []byte("Customer edited description")) ||
		bytes.Contains(body, []byte(`"status":"assigned"`)) ||
		bytes.Contains(body, []byte(`"priority":"low"`)) ||
		bytes.Contains(body, []byte(`"assignee":"admin"`)) {
		t.Fatalf("blocked customer workflow change persisted: %s", body)
	}
}

func TestAdminOnlyEndpointsRejectStaffAndCustomers(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)
	staffID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "staff",
		"email":    "staff@example.test",
		"password": "correct horse",
		"role":     "staff",
	})
	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "customer",
		"email":    "customer@example.test",
		"password": "correct horse",
		"role":     "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, staffID, "agent")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	staffCookie, staffCSRF := loginUser(t, client, server.URL, "staff", "correct horse")
	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")

	adminOnlyGets := []string{
		"/api/admin/maintenance",
		"/api/email-notifications",
		"/api/audit-events",
		"/api/webhooks",
	}
	for _, path := range adminOnlyGets {
		resp, body = doJSON(t, client, http.MethodGet, server.URL+path, nil, staffCookie, "", "")
		requireStatus(t, resp, body, http.StatusForbidden)
		resp, body = doJSON(t, client, http.MethodGet, server.URL+path, nil, customerCookie, "", "")
		requireStatus(t, resp, body, http.StatusForbidden)
	}

	adminOnlyWrites := []struct {
		path    string
		payload any
	}{
		{"/api/products", map[string]any{"key": "NOPE", "name": "Nope"}},
		{"/api/users", map[string]any{"username": "blocked", "email": "blocked@example.test", "role": "staff"}},
	}
	for _, item := range adminOnlyWrites {
		resp, body = doJSON(t, client, http.MethodPost, server.URL+item.path, item.payload, staffCookie, staffCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusForbidden)
		resp, body = doJSON(t, client, http.MethodPost, server.URL+item.path, item.payload, customerCookie, customerCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusForbidden)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tokens", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusForbidden)
}

func TestTicketAttachmentsVisibilityAndDownload(t *testing.T) {
	_, server, client := newTestServer(t, Options{UploadDir: t.TempDir()})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "customer",
		"email":    "customer@example.test",
		"password": "correct horse",
		"role":     "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")

	resp, body = doMultipart(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]string{
		"product_id":  itoa(productID),
		"title":       "Attachment ticket",
		"description": "Please see the attached log",
	}, []testUpload{{
		Field:    "attachments",
		Filename: "request.txt",
		Body:     "customer log content",
	}, {
		Field:    "attachments",
		Filename: "pixel.gif",
		Body:     "GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;",
	}}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	var created store.Ticket
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created ticket: %v", err)
	}
	if len(created.Attachments) != 2 {
		t.Fatalf("created attachments = %#v body=%s", created.Attachments, body)
	}
	var textAttachmentID, imageAttachmentID int64
	for _, attachment := range created.Attachments {
		switch attachment.Filename {
		case "request.txt":
			textAttachmentID = attachment.ID
		case "pixel.gif":
			imageAttachmentID = attachment.ID
			if attachment.ContentType != "image/gif" {
				t.Fatalf("image attachment content type = %q", attachment.ContentType)
			}
		}
	}
	if textAttachmentID == 0 || imageAttachmentID == 0 {
		t.Fatalf("created attachment ids text=%d image=%d attachments=%#v", textAttachmentID, imageAttachmentID, created.Attachments)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(textAttachmentID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("customer log content")) || !strings.Contains(resp.Header.Get("Content-Disposition"), "request.txt") {
		t.Fatalf("download response headers=%v body=%s", resp.Header, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download disposition = %q", resp.Header.Get("Content-Disposition"))
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(imageAttachmentID)+"?preview=1", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(resp.Header.Get("Content-Type"), "image/gif") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "inline") ||
		!bytes.HasPrefix(body, []byte("GIF89a")) {
		t.Fatalf("image preview headers=%v body prefix=%q", resp.Header, body[:min(len(body), 8)])
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(textAttachmentID)+"?preview=1", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("text preview disposition should stay attachment, got %q", resp.Header.Get("Content-Disposition"))
	}

	resp, body = doMultipart(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/comments", map[string]string{
		"body":       "Internal file",
		"visibility": "internal",
	}, []testUpload{{
		Field:    "attachments",
		Filename: "internal.txt",
		Body:     "internal attachment content",
	}}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	var withInternal store.Ticket
	if err := json.Unmarshal(body, &withInternal); err != nil {
		t.Fatalf("decode internal ticket: %v", err)
	}
	internalAttachmentID := int64(0)
	for _, comment := range withInternal.Comments {
		if comment.Visibility == "internal" && len(comment.Attachments) == 1 {
			internalAttachmentID = comment.Attachments[0].ID
		}
	}
	if internalAttachmentID == 0 {
		t.Fatalf("internal attachment missing: %#v", withInternal.Comments)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(internalAttachmentID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("internal attachment content")) {
		t.Fatalf("admin internal download body=%s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(internalAttachmentID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(created.ID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("internal.txt")) || bytes.Contains(body, []byte("internal attachment content")) {
		t.Fatalf("customer ticket leaked internal attachment: %s", body)
	}

	resp, body = doMultipart(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/comments", map[string]string{
		"visibility": "public",
	}, []testUpload{{
		Field:    "attachments",
		Filename: "public.txt",
		Body:     "public attachment content",
	}}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	var withPublic store.Ticket
	if err := json.Unmarshal(body, &withPublic); err != nil {
		t.Fatalf("decode public ticket: %v", err)
	}
	publicAttachmentID := int64(0)
	for _, comment := range withPublic.Comments {
		if comment.Visibility == "public" && len(comment.Attachments) == 1 && comment.Attachments[0].Filename == "public.txt" {
			publicAttachmentID = comment.Attachments[0].ID
		}
	}
	if publicAttachmentID == 0 {
		t.Fatalf("public file-only comment missing: %#v", withPublic.Comments)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(publicAttachmentID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("public attachment content")) {
		t.Fatalf("customer public download body=%s", body)
	}
}

func TestMultipartTicketPatchUpdatesWorkflowCommentAndAttachments(t *testing.T) {
	uploadDir := t.TempDir()
	tracker, server, client := newTestServer(t, Options{UploadDir: uploadDir})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "patchdev",
		"email":    "patchdev@example.test",
		"password": "correct horse",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, devID, "agent")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Multipart patch source",
		"description": "Original description",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doMultipart(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]string{
		"title":       "Multipart patched ticket",
		"description": "Updated through a multipart save",
		"status":      "assigned",
		"priority":    "high",
		"assignee":    "patchdev",
		"body":        "Patch evidence attached",
		"visibility":  "public",
	}, []testUpload{{
		Field:    "attachments",
		Filename: "patch-evidence.txt",
		Body:     "multipart patch attachment content",
	}}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	var patched store.Ticket
	if err := json.Unmarshal(body, &patched); err != nil {
		t.Fatalf("decode patched ticket: %v", err)
	}
	if patched.Title != "Multipart patched ticket" ||
		patched.Description != "Updated through a multipart save" ||
		patched.Status != "assigned" ||
		patched.Priority != "high" ||
		patched.Assignee != "patchdev" {
		t.Fatalf("multipart patch did not update workflow fields: %#v", patched)
	}

	var attachmentID int64
	for _, comment := range patched.Comments {
		if comment.Body == "Patch evidence attached" && comment.Visibility == "public" && len(comment.Attachments) == 1 {
			attachmentID = comment.Attachments[0].ID
		}
	}
	if attachmentID == 0 {
		t.Fatalf("multipart patch comment attachment missing: %#v", patched.Comments)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(attachmentID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("multipart patch attachment content")) {
		t.Fatalf("multipart patch attachment body=%s", body)
	}

	attachment, err := tracker.GetAttachment(attachmentID)
	if err != nil {
		t.Fatalf("load attachment: %v", err)
	}
	if err := os.Remove(filepath.Join(uploadDir, filepath.FromSlash(attachment.StorageKey))); err != nil {
		t.Fatalf("remove stored attachment file: %v", err)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(attachmentID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
	if !bytes.Contains(body, []byte("attachment file not found")) {
		t.Fatalf("missing attachment response = %s", body)
	}
}

func TestBlockedUploadReturnsClearMessage(t *testing.T) {
	_, server, client := newTestServer(t, Options{
		UploadDir:     t.TempDir(),
		MaxUploadSize: 8,
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")
	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	resp, body = doMultipart(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]string{
		"product_id":  itoa(productID),
		"title":       "Blocked upload",
		"description": "This file is too large",
	}, []testUpload{{
		Field:    "attachments",
		Filename: "large.txt",
		Body:     "this body is too large",
	}}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)
	if !bytes.Contains(body, []byte("Upload blocked")) || !bytes.Contains(body, []byte("large.txt")) {
		t.Fatalf("blocked upload response = %s", body)
	}
}

func TestRequesterNotificationPolicy(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "dev",
		"password": "correct horse",
		"email":    "dev@example.test",
		"role":     "staff",
	})
	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username":     "customer",
		"display_name": "Customer",
		"email":        "customer@example.test",
		"password":     "correct horse",
		"role":         "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, devID, "agent")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")

	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")
	createTicket := func(title string) int64 {
		t.Helper()
		resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
			"product_id":  productID,
			"title":       title,
			"description": "Customer needs help",
		}, customerCookie, customerCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusCreated)
		return decodeInt64(t, body, "id")
	}

	workflowOnlyID := createTicket("Workflow-only change")
	notification := requireNotificationForTicketEmail(t, tracker, workflowOnlyID, "customer@example.test")
	if notification.Event != "ticket.created" {
		t.Fatalf("initial requester notification = %#v", notification)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(workflowOnlyID), map[string]any{
		"status":   "assigned",
		"priority": "urgent",
		"assignee": "dev",
		"comment": map[string]any{
			"body":       "Internal triage details",
			"visibility": "internal",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	notification = requireNotificationForTicketEmail(t, tracker, workflowOnlyID, "customer@example.test")
	if notification.Event != "ticket.created" || strings.Contains(notification.BodyText, "Internal triage details") {
		t.Fatalf("workflow-only change should not notify requester: %#v", notification)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(workflowOnlyID), map[string]any{
		"status": "resolved",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	notification = requireNotificationForTicketEmail(t, tracker, workflowOnlyID, "customer@example.test")
	if notification.Event != "ticket.updated" || !strings.Contains(notification.BodyText, "Current status: Resolved") {
		t.Fatalf("resolved status should notify requester: %#v", notification)
	}

	publicReplyID := createTicket("Grouped public reply")
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(publicReplyID), map[string]any{
		"status":   "assigned",
		"assignee": "dev",
		"comment": map[string]any{
			"body":       "Visible staff reply",
			"visibility": "public",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	notification = requireNotificationForTicketEmail(t, tracker, publicReplyID, "customer@example.test")
	if notification.Event != "ticket.commented" ||
		!strings.Contains(notification.BodyText, "Visible staff reply") ||
		strings.Contains(notification.BodyText, "Current status: Assigned") {
		t.Fatalf("public reply should be the requester-facing event: %#v", notification)
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
	password, hasPassword := payload["password"].(string)
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/users", payload, cookie, csrf, baseURL)
	requireStatus(t, resp, body, http.StatusCreated)
	id := decodeInt64(t, body, "id")
	if hasPassword && strings.TrimSpace(password) != "" {
		link := decodeNestedString(t, body, "account_link", "url")
		token := accountLinkTokenFromURL(t, link)
		resp, body = doJSON(t, client, http.MethodPost, baseURL+"/api/account-links/"+token, map[string]any{
			"password": password,
		}, nil, "", "")
		requireStatus(t, resp, body, http.StatusOK)
	}
	return id
}

func addProductMember(t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, csrf string, productID, userID int64, role string) {
	t.Helper()
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/products/"+itoa(productID)+"/members", map[string]any{
		"user_id": userID,
		"role":    role,
	}, cookie, csrf, baseURL)
	requireStatus(t, resp, body, http.StatusCreated)
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !condition() {
		t.Fatal("condition was not met before timeout")
	}
}

type testUpload struct {
	Field    string
	Filename string
	Body     string
}

func doMultipart(t *testing.T, client *http.Client, method, rawURL string, fields map[string]string, files []testUpload, cookie *http.Cookie, csrf, origin string) (*http.Response, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write multipart field: %v", err)
		}
	}
	for _, file := range files {
		field := file.Field
		if field == "" {
			field = "attachments"
		}
		part, err := writer.CreateFormFile(field, file.Filename)
		if err != nil {
			t.Fatalf("create multipart file: %v", err)
		}
		if _, err := io.WriteString(part, file.Body); err != nil {
			t.Fatalf("write multipart file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(method, rawURL, &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-Pappice-CSRF", csrf)
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
		req.Header.Set("X-Pappice-CSRF", csrf)
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

func doRawJSON(t *testing.T, client *http.Client, method, rawURL string, rawBody string, cookie *http.Cookie, csrf, origin string) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if rawBody != "" {
		body = strings.NewReader(rawBody)
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
		req.Header.Set("X-Pappice-CSRF", csrf)
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

func decodeNestedString(t *testing.T, body []byte, parent, key string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	nested, _ := payload[parent].(map[string]any)
	value, _ := nested[key].(string)
	if value == "" {
		t.Fatalf("body missing %s.%s: %s", parent, key, body)
	}
	return value
}

func accountLinkTokenFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse account link url %q: %v", rawURL, err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "account" || (parts[1] != "setup" && parts[1] != "reset") || parts[2] == "" {
		t.Fatalf("invalid account link url %q", rawURL)
	}
	return parts[2]
}

func decodeFirstProductID(t *testing.T, body []byte) int64 {
	t.Helper()
	var payload struct {
		Products []store.Product `json:"products"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode products: %v", err)
	}
	if len(payload.Products) == 0 {
		t.Fatal("no products returned")
	}
	return payload.Products[0].ID
}

func requireNotificationForTicketEmail(t *testing.T, tracker *store.Store, ticketID int64, email string) store.EmailNotification {
	t.Helper()
	var matches []store.EmailNotification
	for _, notification := range tracker.ListEmailNotifications(100) {
		if notification.TicketID == ticketID && strings.EqualFold(notification.RecipientEmail, email) {
			matches = append(matches, notification)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("notifications for ticket %d and %s = %#v, want exactly one", ticketID, email, matches)
	}
	return matches[0]
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
