package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pappice/internal/store"
)

func TestLoginRateLimitSharesEmailVariants(t *testing.T) {
	for _, test := range []struct {
		name  string
		email string
	}{
		{"case and whitespace", "  ADMIN@EXAMPLE.TEST  "},
		{"display name", "Alias <admin@example.test>"},
		{"quoted display name", `"Another Alias" <admin@example.test>`},
		{"angle brackets", "<admin@example.test>"},
		{"quoted local part", `"admin"@example.test`},
		{"comment", "admin@example.test (Alias)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker, server, client := newTestServer(t, Options{
				LoginRateLimit: RateLimit{Limit: 2, Window: time.Minute},
			})
			user, err := tracker.CreateFirstAdmin(store.CreateUser{
				Email: "admin@example.test", Password: "correct horse",
			})
			if err != nil {
				t.Fatal(err)
			}
			login := func(email, password string, status int) {
				t.Helper()
				resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
					"email": email, "password": password,
				}, nil, "", server.URL)
				requireStatus(t, resp, body, status)
				if status == http.StatusOK && decodeNestedInt64(t, body, "user", "id") != user.ID {
					t.Fatalf("email variant authenticated a different user: %s", body)
				}
			}

			// A successful login through an equivalent address clears the shared bucket.
			login(user.Email, "wrong password", http.StatusUnauthorized)
			login(test.email, "correct horse", http.StatusOK)

			// Alternating representations still consumes one account's attempt limit.
			login(test.email, "wrong password", http.StatusUnauthorized)
			login(user.Email, "wrong password", http.StatusUnauthorized)
			login(test.email, "wrong password", http.StatusTooManyRequests)
			login(user.Email, "correct horse", http.StatusTooManyRequests)
		})
	}
}

func TestSessionRequestsRequireSameOriginJSON(t *testing.T) {
	for _, endpoint := range []string{"setup", "login", "account link"} {
		t.Run(endpoint, func(t *testing.T) {
			tracker, server, client := newTestServer(t)
			input := store.CreateUser{Email: "account@example.test", Password: "known=password", Role: "customer"}
			path := "/api/" + endpoint
			payload := map[string]any{"email": input.Email, "password": input.Password}
			wantStatus := http.StatusOK
			switch endpoint {
			case "setup":
				wantStatus = http.StatusCreated
			case "login":
				if _, err := tracker.CreateFirstAdmin(input); err != nil {
					t.Fatal(err)
				}
			case "account link":
				_, _, token, err := tracker.CreateUserWithSetupLink(input, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				path = "/api/account-links/" + token
				delete(payload, "email")
			}

			for _, test := range []struct {
				name        string
				origin      string
				referer     string
				contentType string
				status      int
			}{
				{"cross-site form", "https://unrelated.example.test", "", "text/plain", http.StatusForbidden},
				{"cross-site JSON", "https://unrelated.example.test", "", "application/json", http.StatusForbidden},
				{"null origin", "null", "", "application/json", http.StatusForbidden},
				{"missing origin", "", "", "application/json", http.StatusForbidden},
				{"origin overrides referer", "https://unrelated.example.test", server.URL + "/", "application/json", http.StatusForbidden},
				{"cross-site referer", "", "https://unrelated.example.test/", "application/json", http.StatusForbidden},
				{"plain text", server.URL, "", "text/plain", http.StatusUnsupportedMediaType},
				{"empty media type", server.URL, "", " ", http.StatusUnsupportedMediaType},
				{"form encoded", server.URL, "", "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
				{"invalid media type", server.URL, "", "application/json; invalid", http.StatusUnsupportedMediaType},
			} {
				t.Run(test.name, func(t *testing.T) {
					resp, body := doJSONWithHeaders(t, client, http.MethodPost, server.URL+path, payload, nil, "", map[string]string{
						"Origin":       test.origin,
						"Referer":      test.referer,
						"Content-Type": test.contentType,
					})
					requireStatus(t, resp, body, test.status)
					if len(resp.Cookies()) != 0 {
						t.Fatal("rejected request set a session cookie")
					}
				})
			}

			// Referer is a same-origin fallback; JSON may include a charset parameter.
			resp, body := doJSONWithHeaders(t, client, http.MethodPost, server.URL+path, payload, nil, "", map[string]string{
				"Referer":      server.URL + "/",
				"Content-Type": "application/json; charset=utf-8",
			})
			requireStatus(t, resp, body, wantStatus)
			if len(resp.Cookies()) == 0 || decodeString(t, body, "csrf_token") == "" {
				t.Fatal("valid request did not create a browser session")
			}
		})
	}
}

func TestSetupRequiresHTTPS(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	server := httptest.NewServer(NewServer(tracker))
	defer server.Close()

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/setup", map[string]any{
		"email":    "admin@example.test",
		"password": "correct horse",
	}, nil, "", server.URL)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", resp.StatusCode, body)
	}

	resp, body = doJSONWithHeaders(t, server.Client(), http.MethodPost, server.URL+"/api/setup", map[string]any{
		"email":    "admin@example.test",
		"password": "correct horse",
	}, nil, "", map[string]string{"X-Forwarded-Proto": "https"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("forwarded proto without trust status = %d body=%s, want 400", resp.StatusCode, body)
	}
}

func TestTrustedProxyHeadersAllowBrowserSession(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	server := httptest.NewServer(NewServer(tracker, Options{TrustProxyHeaders: true}))
	defer server.Close()
	client := server.Client()
	headers := map[string]string{
		"Origin":            "https://support.example.test",
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "support.example.test",
		"X-Forwarded-For":   "198.51.100.99",
		"X-Real-IP":         "203.0.113.42",
	}

	resp, body := doJSONWithHeaders(t, client, http.MethodPost, server.URL+"/api/setup", map[string]any{
		"email":    "admin@example.test",
		"password": "correct horse",
	}, nil, "", headers)
	requireStatus(t, resp, body, http.StatusCreated)
	if resp.Header.Get("Strict-Transport-Security") == "" {
		t.Fatalf("trusted proxy HTTPS response missing HSTS header")
	}
	adminCookie := resp.Cookies()[0]
	adminCSRF := decodeString(t, body, "csrf_token")

	resp, body = doJSONWithHeaders(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "OPS",
		"name": "Operations",
	}, adminCookie, adminCSRF, headers)
	requireStatus(t, resp, body, http.StatusCreated)
}

func TestTrustedProxyClientIPPrefersRealIP(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	request.Header.Set("X-Forwarded-For", "198.51.100.99, 192.0.2.10")
	request.Header.Set("X-Real-IP", "203.0.113.42")

	server := NewServer(nil, Options{TrustProxyHeaders: true})
	if got := server.clientIP(request); got != "203.0.113.42" {
		t.Fatalf("clientIP = %q, want X-Real-IP over spoofable X-Forwarded-For", got)
	}

	untrusted := NewServer(nil)
	if got := untrusted.clientIP(request); got != "192.0.2.10" {
		t.Fatalf("untrusted clientIP = %q, want remote address", got)
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

func TestAPIAuthAndCSRFContracts(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/tokens", map[string]any{"name": "contract"}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	tokenValue := decodeString(t, body, "value")
	tokenID := decodeNestedInt64(t, body, "token", "id")

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

	browserOnlyWithToken := []struct {
		name    string
		method  string
		path    string
		payload any
	}{
		{"profile update", http.MethodPatch, "/api/me", map[string]any{"display_name": "Token Rename"}},
		{"password change", http.MethodPost, "/api/me/password", map[string]any{
			"current_password": "correct horse",
			"new_password":     "better password",
		}},
		{"logout", http.MethodPost, "/api/logout", nil},
		{"list tokens", http.MethodGet, "/api/tokens", nil},
		{"create token", http.MethodPost, "/api/tokens", map[string]any{"name": "nested-token"}},
		{"delete token", http.MethodDelete, "/api/tokens/" + itoa(tokenID), nil},
	}
	for _, tt := range browserOnlyWithToken {
		t.Run("token cannot "+tt.name, func(t *testing.T) {
			resp, body := doJSONBearer(t, client, tt.method, server.URL+tt.path, tt.payload, tokenValue)
			requireStatus(t, resp, body, http.StatusForbidden)
			if !bytes.Contains(body, []byte("browser session")) {
				t.Fatalf("token browser-only response = %s", body)
			}
		})
	}
}

func TestAccountSetupAndResetLinks(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/users", map[string]any{
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
		"email":    "pending@example.test",
		"password": "correct horse",
	}, nil, "", server.URL)
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
	if !bytes.Contains(body, []byte(`"purpose":"setup"`)) || !bytes.Contains(body, []byte(`"email":"pending@example.test"`)) {
		t.Fatalf("account setup link lookup = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+setupToken, map[string]any{
		"password": "correct horse",
	}, nil, "", server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if len(resp.Cookies()) == 0 {
		t.Fatalf("setup link did not create session: %s", body)
	}
	userCookie := resp.Cookies()[0]
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+setupToken, map[string]any{
		"password": "correct horse",
	}, nil, "", server.URL)
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
		"email":    "pending@example.test",
		"password": "correct horse",
	}, nil, "", server.URL)
	requireStatus(t, resp, body, http.StatusUnauthorized)
	if !bytes.Contains(body, []byte("password setup or reset is required")) {
		t.Fatalf("old password reset-required error = %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+resetToken, map[string]any{
		"password": "new correct horse",
	}, nil, "", server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	loginUser(t, client, server.URL, "pending", "new correct horse")

	_, _, expiredToken, err := tracker.CreatePasswordResetLink(userID, time.Nanosecond, store.EventContext{})
	if err != nil {
		t.Fatalf("create expired reset link: %v", err)
	}
	expireAccountLinkToken(t, tracker, expiredToken)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/account-links/"+expiredToken, nil, nil, "", "")
	requireStatus(t, resp, body, http.StatusGone)
	if !bytes.Contains(body, []byte("expired")) || !bytes.Contains(body, []byte(`"reason":"expired"`)) {
		t.Fatalf("expired reset link response = %s", body)
	}
}

func TestAdminCreatesUserWithManualPassword(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/users", map[string]any{
		"display_name": "Manual Customer",
		"email":        "manual@example.test",
		"role":         "customer",
		"password":     "manual horse battery",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	if bytes.Contains(body, []byte("account_link")) ||
		bytes.Contains(body, []byte("manual horse battery")) ||
		!bytes.Contains(body, []byte(`"password_reset_required":false`)) {
		t.Fatalf("manual password create response = %s", body)
	}
	if notifications := mustEmailNotifications(t, tracker, 10); len(notifications) != 0 {
		t.Fatalf("manual password create queued email notifications: %#v", notifications)
	}
	loginUser(t, client, server.URL, "manual", "manual horse battery")
}

func TestProfileAndPasswordChangeFlow(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)
	staffID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
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
	loginUser(t, client, server.URL, "person@example.test", "better password")
}

func TestSecurityHardeningRateLimitsAuditAndSessionTTL(t *testing.T) {
	ttlStarted := time.Now().UTC()
	ttlTracker, ttlServer, ttlClient := newTestServer(t, Options{SessionTTL: time.Hour})
	adminCookie, _ := setupAdmin(t, ttlClient, ttlServer.URL)
	requireSessionCookieTTL(t, adminCookie, ttlStarted, time.Hour)
	waitForDomainEvents(t, ttlTracker)
	expireSessionToken(t, ttlTracker, adminCookie.Value)
	resp, body := doJSON(t, ttlClient, http.MethodGet, ttlServer.URL+"/api/session", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if decodeBool(t, body, "authenticated") {
		t.Fatalf("short-lived session still authenticated: %s", body)
	}

	tracker, server, client := newTestServer(t, Options{
		LoginRateLimit:       RateLimit{Limit: 2, Window: time.Minute},
		AccountLinkRateLimit: RateLimit{Limit: 2, Window: time.Minute},
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	for range 2 {
		resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
			"email":    "missing@example.test",
			"password": "wrong password",
		}, nil, "", server.URL)
		requireStatus(t, resp, body, http.StatusUnauthorized)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/login", map[string]any{
		"email":    "missing@example.test",
		"password": "wrong password",
	}, nil, "", server.URL)
	requireStatus(t, resp, body, http.StatusTooManyRequests)
	if retry := resp.Header.Get("Retry-After"); retry == "" {
		t.Fatalf("rate-limited response missing Retry-After: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/users", map[string]any{
		"email": "limited@example.test",
		"role":  "staff",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	token := accountLinkTokenFromURL(t, decodeNestedString(t, body, "account_link", "url"))
	for range 2 {
		resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+token, map[string]any{
			"password": "short",
		}, nil, "", server.URL)
		requireStatus(t, resp, body, http.StatusBadRequest)
	}
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/account-links/"+token, map[string]any{
		"password": "short",
	}, nil, "", server.URL)
	requireStatus(t, resp, body, http.StatusTooManyRequests)

	waitForDomainEvents(t, tracker)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/audit-events", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("setup.completed")) ||
		!bytes.Contains(body, []byte("user.created")) ||
		!bytes.Contains(body, []byte("limited")) {
		t.Fatalf("audit log missing setup/user events: %s", body)
	}
}

func TestCustomerPermissionBoundaries(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)
	supportID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Support",
		"email":        "support@example.test",
		"password":     "correct horse",
		"role":         "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, supportID, "staff")
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":  "BILL",
		"name": "Billing",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	otherProductID := decodeInt64(t, body, "id")

	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Customer",
		"email":        "customer@example.test",
		"password":     "correct horse",
		"role":         "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":       productID,
		"title":            "Customer-owned ticket",
		"description":      "Customer-visible request",
		"priority":         "urgent",
		"assignee_user_id": supportID,
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	customerTicketID := decodeInt64(t, body, "id")
	var created store.Ticket
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode customer ticket: %v", err)
	}
	if created.Priority != "urgent" || created.AssigneeUserID != 0 || created.AssigneeEmail != "" || created.RequesterUserID != customerID || created.CreatedByUserID != customerID {
		t.Fatalf("customer-controlled fields were not normalized: %#v", created)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(customerTicketID), map[string]any{
		"assignee_user_id": supportID,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"assignee_email":"support@example.test"`)) {
		t.Fatalf("admin assignment response = %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(customerTicketID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte(`"assignee_email":`)) || bytes.Contains(body, []byte(`"assignee_user_id":`)) {
		t.Fatalf("customer ticket detail leaked assignee: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?assignee_user_id="+itoa(supportID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Customer-owned ticket")) || bytes.Contains(body, []byte(`"assignee_email":`)) || bytes.Contains(body, []byte(`"assignee_user_id":`)) {
		t.Fatalf("customer assignee filter should be ignored and sanitized: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?q=support@example.test", nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Customer-owned ticket")) || bytes.Contains(body, []byte(`"assignee_email":`)) || bytes.Contains(body, []byte(`"assignee_user_id":`)) {
		t.Fatalf("customer search leaked assignee matches: %s", body)
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
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(customerTicketID), map[string]any{
		"assignee_user_id": 0,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)

	for name, patch := range map[string]map[string]any{
		"status":      {"status": "closed"},
		"priority":    {"priority": "low"},
		"assignee":    {"assignee_user_id": supportID},
		"title":       {"title": "Customer renamed ticket"},
		"description": {"description": "Customer edited description"},
		"mixed": {
			"status": "closed",
			"comment": map[string]any{
				"body":       "Should not persist",
				"visibility": "public",
			},
		},
	} {
		resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(customerTicketID), patch, customerCookie, customerCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusForbidden)
		t.Logf("blocked customer ticket patch %s", name)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(customerTicketID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("Should not persist")) ||
		bytes.Contains(body, []byte("Customer renamed ticket")) ||
		bytes.Contains(body, []byte("Customer edited description")) ||
		bytes.Contains(body, []byte(`"status":"closed"`)) ||
		bytes.Contains(body, []byte(`"priority":"low"`)) ||
		bytes.Contains(body, []byte(`"assignee_email":`)) ||
		bytes.Contains(body, []byte(`"assignee_user_id":`)) {
		t.Fatalf("blocked customer ticket change persisted: %s", body)
	}
}

func TestAdminOnlyEndpointsRejectStaffAndCustomers(t *testing.T) {
	_, server, client := newTestServer(t)
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)
	staffID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"email":    "staff@example.test",
		"password": "correct horse",
		"role":     "staff",
	})
	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"email":    "customer@example.test",
		"password": "correct horse",
		"role":     "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, staffID, "staff")
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
		{"/api/users", map[string]any{"email": "blocked@example.test", "role": "staff"}},
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
