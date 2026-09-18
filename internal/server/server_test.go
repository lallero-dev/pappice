package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pappice/internal/store"
)

func TestProductRolePermissions(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	server := httptest.NewTLSServer(NewServer(tracker))
	defer server.Close()
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	setupResp, setupBody := doJSON(t, client, http.MethodPost, server.URL+"/api/setup", map[string]any{
		"email":    "admin@example.test",
		"password": "correct horse",
	}, nil, "", server.URL)
	requireStatus(t, setupResp, setupBody, http.StatusCreated)
	adminCookie := setupResp.Cookies()[0]
	adminCSRF := decodeString(t, setupBody, "csrf_token")

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	bobID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
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
		"email":    "bob@example.test",
		"password": "correct horse",
	}, nil, "", server.URL)
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
		"status": "closed",
	}, bobCookie, bobCSRF, server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer patch status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products/"+itoa(productID)+"/members", map[string]any{
		"user_id": bobID,
		"role":    "staff",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"status": "closed",
	}, bobCookie, bobCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
}

func TestSessionAssetsTokensAndLogoutFlow(t *testing.T) {
	_, server, client := newTestServer(t)

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
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/products/1/general", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusOK)
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

	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/session", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeBool(t, body, "authenticated"); !got {
		t.Fatalf("session authenticated = false after setup: %s", body)
	}
	if got := decodeString(t, body, "csrf_token"); got == "" {
		t.Fatalf("session csrf missing: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"email":    "admin@example.test",
		"password": "wrong password",
	}, nil, "", server.URL)
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
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	userID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"email":    "methodstaff@example.test",
		"password": "correct horse",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, userID, "staff")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Method contract",
		"description": "Exercise route methods",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")
	ticketKey := decodeString(t, body, "key")

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
		{"ticket by key", http.MethodPost, "/api/tickets/key/" + ticketKey, http.MethodGet},
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
		{"email test", http.MethodGet, "/api/email-notifications/test", http.MethodPost},
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

func TestProductDeletionRequiresAdmin(t *testing.T) {
	tracker, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	managerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"password": "correct horse",
		"email":    "manager@example.test",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, managerID, "manager")
	managerCookie, managerCSRF := loginUser(t, client, server.URL, "manager", "correct horse")

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/products/"+itoa(productID), nil, managerCookie, managerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)
	if _, err := tracker.GetProduct(productID); err != nil {
		t.Fatalf("manager delete removed product: %v", err)
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

func TestProductMemberRemovalAPI(t *testing.T) {
	tracker, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	userID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Removable Staff",
		"email":        "removable@example.test",
		"password":     "correct horse",
		"role":         "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, userID, "staff")
	if role, err := tracker.ProductRole(userID, productID); err != nil || role != "staff" {
		t.Fatalf("product role before delete = %q err=%v", role, err)
	}

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/products/"+itoa(productID)+"/members/"+itoa(userID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if !decodeBool(t, body, "ok") {
		t.Fatalf("delete member response = %s", body)
	}
	if role, err := tracker.ProductRole(userID, productID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("product role after delete = %q err=%v, want none", role, err)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(productID)+"/members", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("removable@example.test")) {
		t.Fatalf("member list still contains removed user: %s", body)
	}
	waitForDomainEvents(t, tracker)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/audit-events?q=product_member.removed", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("product_member.removed")) || !bytes.Contains(body, []byte("removable@example.test")) {
		t.Fatalf("audit log missing product member removal: %s", body)
	}
}

func TestTicketDeletionRequiresAdmin(t *testing.T) {
	tracker, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	managerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"password": "correct horse",
		"email":    "manager@example.test",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, managerID, "manager")
	managerCookie, managerCSRF := loginUser(t, client, server.URL, "manager", "correct horse")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id": productID,
		"title":      "Delete from admin only",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/tickets/"+itoa(ticketID), nil, managerCookie, managerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusForbidden)
	if _, err := tracker.GetTicket(ticketID); err != nil {
		t.Fatalf("manager delete removed ticket: %v", err)
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
	waitForDomainEvents(t, tracker)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/audit-events?q=ticket.deleted", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("ticket.deleted")) {
		t.Fatalf("audit log missing ticket deletion: %s", body)
	}
}

func TestTicketListPaginationAndSorting(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)
	for _, ticket := range []struct{ title, priority string }{
		{"Zulu", "urgent"},
		{"Alpha", "low"},
		{"Mike", "normal"},
	} {
		resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
			"product_id": productID,
			"title":      ticket.title,
			"priority":   ticket.priority,
		}, adminCookie, adminCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusCreated)
	}

	getPage := func(path string) ticketListResult {
		t.Helper()
		resp, body := doJSON(t, client, http.MethodGet, server.URL+path, nil, adminCookie, "", "")
		requireStatus(t, resp, body, http.StatusOK)
		var page ticketListResult
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("decode ticket page: %v", err)
		}
		return page
	}

	first := getPage("/api/tickets?status=open&sort=title&direction=asc&limit=2")
	if len(first.Tickets) != 2 || first.Tickets[0].Title != "Alpha" || first.Tickets[1].Title != "Mike" {
		t.Fatalf("first ticket page = %#v", first.Tickets)
	}
	if !first.HasMore || first.Limit != 2 || first.Offset != 0 {
		t.Fatalf("first ticket pagination = %#v", first)
	}
	if first.Counts["all"] != 3 || first.Counts["open"] != 3 {
		t.Fatalf("ticket aggregates changed by pagination: %#v", first.Counts)
	}

	second := getPage("/api/tickets?status=open&sort=title&direction=asc&limit=2&offset=2")
	if len(second.Tickets) != 1 || second.Tickets[0].Title != "Zulu" || second.HasMore {
		t.Fatalf("second ticket page = %#v", second)
	}
	byPriority := getPage("/api/tickets?status=open&sort=priority&direction=desc")
	if len(byPriority.Tickets) != 3 || byPriority.Tickets[0].Title != "Zulu" || byPriority.Tickets[2].Title != "Alpha" {
		t.Fatalf("priority-sorted tickets = %#v", byPriority.Tickets)
	}
}

func TestTicketAssigneeRequiresProductStaff(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)
	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	eligibleID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Eligible Staff",
		"email":        "eligible@example.test",
		"password":     "correct horse",
		"role":         "staff",
	})
	viewerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Product Viewer",
		"email":        "viewer@example.test",
		"password":     "correct horse",
		"role":         "staff",
	})
	disabledID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Disabled Staff",
		"email":        "disabled@example.test",
		"password":     "correct horse",
		"role":         "staff",
	})
	otherID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Other Staff",
		"email":        "other@example.test",
		"password":     "correct horse",
		"role":         "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, eligibleID, "staff")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, viewerID, "viewer")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, disabledID, "staff")
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/users/"+itoa(disabledID), map[string]any{
		"disabled": true,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("eligible@example.test")) ||
		bytes.Contains(body, []byte("viewer@example.test")) || bytes.Contains(body, []byte("other@example.test")) ||
		bytes.Contains(body, []byte("disabled@example.test")) {
		t.Fatalf("product assignee candidates = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id": productID,
		"title":      "Assignment eligibility",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	for _, userID := range []int64{viewerID, otherID, disabledID, 999999} {
		resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
			"assignee_user_id": userID,
		}, adminCookie, adminCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusBadRequest)
		if !bytes.Contains(body, []byte("active staff member of this product")) {
			t.Fatalf("invalid assignee response = %s", body)
		}
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"assignee_user_id": eligibleID,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if decodeInt64(t, body, "assignee_user_id") != eligibleID || decodeString(t, body, "assignee_email") != "eligible@example.test" {
		t.Fatalf("assignee response = %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/users/"+itoa(eligibleID), map[string]any{
		"email": "renamed@example.test",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(ticketID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if decodeInt64(t, body, "assignee_user_id") != eligibleID || decodeString(t, body, "assignee_email") != "renamed@example.test" {
		t.Fatalf("assignment did not follow user id after email change: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":       productID,
		"title":            "Invalid initial assignee",
		"assignee_user_id": otherID,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)
}

func TestAPIValidationContracts(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	userID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
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

	_, disabledServer, disabledClient := newTestServer(t)
	disabledCookie, disabledCSRF := setupAdmin(t, disabledClient, disabledServer.URL)
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
	uploadDir := filepath.Join(t.TempDir(), "uploads")
	latestBackup := filepath.Join(backupDir, "20260101T120000Z")
	if err := os.MkdirAll(latestBackup, 0o755); err != nil {
		t.Fatalf("create backup dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(latestBackup, "pappice.db"), []byte("backup"), 0o600); err != nil {
		t.Fatalf("write backup db marker: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(uploadDir, "aa"), 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uploadDir, "aa", "attachment"), []byte("eleven bytes"), 0o600); err != nil {
		t.Fatalf("write attachment marker: %v", err)
	}
	_, server, client := newTestServer(t, Options{
		EmailNotifications:   true,
		PublicURL:            "https://tracker.example.test",
		UploadDir:            uploadDir,
		BackupDir:            backupDir,
		DomainEventRetention: 48 * time.Hour,
		Version:              "test-version",
	})
	adminCookie, _ := setupAdmin(t, client, server.URL)

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
	if got := decodeInt64(t, body, "database_size_bytes"); got <= 0 {
		t.Fatalf("database_size_bytes = %d", got)
	}
	if got := decodeInt64(t, body, "attachment_storage_bytes"); got != 12 {
		t.Fatalf("attachment_storage_bytes = %d, want 12", got)
	}

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/admin/maintenance", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusUnauthorized)
}

func TestAdminHistoryPaginationAndFilters(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	for range 2 {
		resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/email-notifications/test", map[string]any{}, adminCookie, adminCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusCreated)
	}
	notifications := mustEmailNotifications(t, tracker, 10)
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

func TestRegisteredCustomerTicketFlow(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)
	users, err := tracker.ListUsers()
	if err != nil || len(users) != 1 {
		t.Fatalf("list initial users = %#v err=%v", users, err)
	}
	adminID := users[0].ID

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/support/products", nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Customer",
		"email":        "customer@example.test",
		"password":     "correct horse",
		"role":         "customer",
	})
	intruderID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"email":    "intruder@example.test",
		"password": "correct horse",
		"role":     "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, intruderID, "customer")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/users", map[string]any{
		"display_name": "No Email",
		"password":     "correct horse",
		"role":         "customer",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)

	loginResp, loginBody := doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"email":    "customer@example.test",
		"password": "correct horse",
	}, nil, "", server.URL)
	requireStatus(t, loginResp, loginBody, http.StatusOK)
	customerCookie := loginResp.Cookies()[0]
	customerCSRF := decodeString(t, loginBody, "csrf_token")

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("intruder@example.test")) {
		t.Fatalf("customer product list leaked requester candidates: %s", body)
	}
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
		ID              int64  `json:"id"`
		Key             string `json:"key"`
		Title           string `json:"title"`
		RequesterUserID int64  `json:"requester_user_id"`
		RequesterEmail  string `json:"requester_email"`
		CreatedByUserID int64  `json:"created_by_user_id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created ticket: %v", err)
	}
	if created.ID == 0 || created.Key == "" || created.RequesterUserID != customerID || created.CreatedByUserID != customerID || created.RequesterEmail != "customer@example.test" {
		t.Fatalf("created ticket = %#v", created)
	}
	if notification := requireNotificationForTicketEmail(t, tracker, created.ID, "customer@example.test"); notification.Event != "ticket.created" {
		t.Fatalf("requester notification = %#v", notification)
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
	if !bytes.Contains(body, []byte("Need help")) || !bytes.Contains(body, []byte(`"has_unread":true`)) {
		t.Fatalf("customer unread list missing ticket: %s", body)
	}
	var ticketList struct {
		Tickets     []map[string]json.RawMessage `json:"tickets"`
		Counts      map[string]int               `json:"counts"`
		UnreadTotal int                          `json:"unread_total"`
	}
	if err := json.Unmarshal(body, &ticketList); err != nil {
		t.Fatalf("decode ticket list: %v", err)
	}
	if len(ticketList.Tickets) != 1 || ticketList.Counts["all"] != 1 || ticketList.Counts["open"] != 1 || ticketList.UnreadTotal != 1 {
		t.Fatalf("ticket list aggregates = %#v", ticketList)
	}
	for _, field := range []string{"description", "comments", "attachments"} {
		if _, exists := ticketList.Tickets[0][field]; exists {
			t.Fatalf("ticket summary includes %q: %s", field, body)
		}
	}
	if bytes.Contains(body, []byte("Something is wrong")) || bytes.Contains(body, []byte("Public staff reply")) {
		t.Fatalf("ticket summary leaked conversation content: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?q=Something%20is%20wrong", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("ticket description search did not match summary: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?q=Public%20staff%20reply", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("ticket search unexpectedly matched conversation content: %s", body)
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
		"priority":         "high",
		"assignee_user_id": adminID,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(created.ID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if decodeBool(t, body, "has_unread") || bytes.Contains(body, []byte(`"assignee_user_id":`)) {
		t.Fatalf("internal ticket change became customer-visible: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(created.ID), map[string]any{
		"status": "closed",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=open", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("explicit open filter should hide read closed ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=open&include_unread_outside_status=1", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Need help")) || !bytes.Contains(body, []byte(`"status":"closed"`)) {
		t.Fatalf("default open view should include unread closed ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/read", nil, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=open&include_unread_outside_status=1", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("default open view should hide read closed ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=closed", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("explicit closed filter should show closed ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(created.ID)+"/comments", map[string]any{
		"body":       "The problem returned",
		"visibility": "public",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	if !bytes.Contains(body, []byte(`"status":"open"`)) || !bytes.Contains(body, []byte(`"current_status":"open"`)) {
		t.Fatalf("customer reply did not reopen ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?status=open", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Need help")) {
		t.Fatalf("reopened ticket missing from open view: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Private staff note")) {
		t.Fatalf("customer ticket list leaked internal note: %s", body)
	}
	if bytes.Contains(body, []byte("Public staff reply")) {
		t.Fatalf("customer ticket list included conversation content: %s", body)
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

func TestStaffCreatesTicketForCustomer(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
		UploadDir:          t.TempDir(),
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)
	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	staffID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Support",
		"email":        "staff@example.test",
		"password":     "correct horse",
		"role":         "staff",
	})
	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Customer",
		"email":        "customer@example.test",
		"password":     "correct horse",
		"role":         "customer",
	})
	otherCustomerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"email":    "other@example.test",
		"password": "correct horse",
		"role":     "customer",
	})
	outsideCustomerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"email":    "outside@example.test",
		"password": "correct horse",
		"role":     "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, staffID, "staff")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, otherCustomerID, "customer")
	staffCookie, staffCSRF := loginUser(t, client, server.URL, "staff", "correct horse")

	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, staffCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	var products struct {
		Requesters []store.ProductAccount `json:"requesters"`
	}
	if err := json.Unmarshal(body, &products); err != nil {
		t.Fatalf("decode product requesters: %v", err)
	}
	if len(products.Requesters) != 2 || products.Requesters[0].UserID != customerID || products.Requesters[1].UserID != otherCustomerID {
		t.Fatalf("product requesters = %#v", products.Requesters)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":        productID,
		"title":             "Opened by support",
		"description":       "Customer called support",
		"assignee_user_id":  staffID,
		"requester_user_id": customerID,
	}, staffCookie, staffCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	var created store.Ticket
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode staff-created ticket: %v", err)
	}
	if created.RequesterUserID != customerID || created.CreatedByUserID != staffID || created.CreatedByName != "Support" || created.AssigneeUserID != staffID {
		t.Fatalf("staff-created ticket = %#v", created)
	}
	notification := requireNotificationForTicketEmail(t, tracker, created.ID, "customer@example.test")
	if notification.Event != "ticket.created" {
		t.Fatalf("requester notification = %#v", notification)
	}
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(created.ID), map[string]any{
		"status": "closed",
	}, staffCookie, staffCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/users/"+itoa(customerID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusConflict)
	if !bytes.Contains(body, []byte("disable it instead")) {
		t.Fatalf("historical account deletion response = %s", body)
	}

	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(created.ID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	var customerTicket store.Ticket
	if err := json.Unmarshal(body, &customerTicket); err != nil {
		t.Fatalf("decode customer ticket: %v", err)
	}
	if customerTicket.Title != "Opened by support" || customerTicket.CreatedByUserID != staffID || customerTicket.CreatedByName != "Support" || !customerTicket.HasUnread {
		t.Fatalf("customer cannot see staff-created ticket: %s", body)
	}
	if got := customerTicket.StatusChanges; len(got) != 1 || got[0].ActorUserID != staffID ||
		got[0].ActorName != "Support" || got[0].PreviousStatus != "open" || got[0].CurrentStatus != "closed" {
		t.Fatalf("customer status history = %#v", got)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":        productID,
		"title":             "Forged requester",
		"requester_user_id": otherCustomerID,
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)

	resp, body = doMultipart(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]string{
		"product_id":        itoa(productID),
		"title":             "Multipart customer ticket",
		"requester_user_id": itoa(customerID),
	}, []testUpload{{Filename: "context.txt", Body: "customer context"}}, staffCookie, staffCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode multipart ticket: %v", err)
	}
	if created.RequesterUserID != customerID || len(created.Attachments) != 1 || created.Attachments[0].CreatedByUserID != staffID {
		t.Fatalf("multipart staff-created ticket = %#v", created)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":        productID,
		"title":             "Customer outside product",
		"requester_user_id": outsideCustomerID,
	}, staffCookie, staffCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)
}
