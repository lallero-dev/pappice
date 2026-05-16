package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

	bobID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "bob",
		"email":    "bob@example.test",
		"password": "correct horse",
		"role":     "staff",
	})

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

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/logout", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusMethodNotAllowed)
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("logout Allow header = %q, want POST", allow)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects", map[string]any{
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

func TestAccountSetupAndResetLinks(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	_ = tracker
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
	if !bytes.Contains(body, []byte("Pemmece")) {
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
		t.Fatalf("setup link did not issue session: %s", body)
	}
	userCookie := resp.Cookies()[0]
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+setupToken, map[string]any{
		"password": "correct horse",
	}, nil, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)
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
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?project_id="+itoa(projectID)+"&status=new&status=assigned&assignee=dev&q=dashboard", nil, adminCookie, "", "")
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

func TestTicketSaveGroupsWorkflowAndCommentEmail(t *testing.T) {
	_, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/projects", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	projectID := decodeFirstProjectID(t, body)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "dev",
		"password": "correct horse",
		"email":    "dev@example.test",
		"role":     "staff",
	})
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, devID, "agent")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"project_id":  projectID,
		"title":       "Grouped save",
		"description": "Needs one email",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	issueID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(issueID), map[string]any{
		"status":   "assigned",
		"assignee": "dev",
		"comment": map[string]any{
			"body":       "This should roll back",
			"visibility": "private",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(issueID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("This should roll back")) || bytes.Contains(body, []byte(`"status":"assigned"`)) {
		t.Fatalf("failed grouped save was not rolled back: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(issueID), map[string]any{
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
	requireStatus(t, resp, body, http.StatusNotFound)

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
	noEmailID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"username": "noemail",
		"password": "correct horse",
		"role":     "customer",
	})
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, customerID, "customer")
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, intruderID, "customer")
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, noEmailID, "customer")

	noEmailCookie, noEmailCSRF := loginUser(t, client, server.URL, "noemail", "correct horse")
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"project_id":  projectID,
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

func TestRequesterNotificationPolicy(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL, "admin", "admin@example.test")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/projects", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	projectID := decodeFirstProjectID(t, body)

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
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, devID, "agent")
	addProjectMember(t, client, server.URL, adminCookie, adminCSRF, projectID, customerID, "customer")

	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")
	createTicket := func(title string) int64 {
		t.Helper()
		resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
			"project_id":  projectID,
			"title":       title,
			"description": "Customer needs help",
		}, customerCookie, customerCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusCreated)
		return decodeInt64(t, body, "id")
	}

	workflowOnlyID := createTicket("Workflow-only change")
	notification := requireNotificationForIssueEmail(t, tracker, workflowOnlyID, "customer@example.test")
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
	notification = requireNotificationForIssueEmail(t, tracker, workflowOnlyID, "customer@example.test")
	if notification.Event != "ticket.created" || strings.Contains(notification.BodyText, "Internal triage details") {
		t.Fatalf("workflow-only change should not notify requester: %#v", notification)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(workflowOnlyID), map[string]any{
		"status": "resolved",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	notification = requireNotificationForIssueEmail(t, tracker, workflowOnlyID, "customer@example.test")
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
	notification = requireNotificationForIssueEmail(t, tracker, publicReplyID, "customer@example.test")
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

func requireNotificationForIssueEmail(t *testing.T, tracker *store.Store, issueID int64, email string) store.EmailNotification {
	t.Helper()
	var matches []store.EmailNotification
	for _, notification := range tracker.ListEmailNotifications(100) {
		if notification.IssueID == issueID && strings.EqualFold(notification.RecipientEmail, email) {
			matches = append(matches, notification)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("notifications for issue %d and %s = %#v, want exactly one", issueID, email, matches)
	}
	return matches[0]
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
