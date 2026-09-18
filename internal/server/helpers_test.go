package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"pappice/internal/security"
	"pappice/internal/store"
)

func newTestServer(t *testing.T, opts ...Options) (*store.Store, *httptest.Server, *http.Client) {
	t.Helper()
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	app := NewServer(tracker, opts...)
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	workers.Go(func() { app.RunEventDispatcher(ctx, time.Hour) })
	workers.Go(func() { app.RunWebhookDispatcher(ctx, time.Hour) })
	server := httptest.NewTLSServer(app)
	t.Cleanup(func() {
		server.Close()
		cancel()
		workers.Wait()
		_ = tracker.Close()
	})
	client := server.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return tracker, server, client
}

func waitForDomainEvents(t *testing.T, tracker *store.Store) {
	t.Helper()
	eventually(t, func() bool {
		for _, event := range mustDomainEvents(t, tracker, 200) {
			if event.Status == "pending" || event.Status == "processing" {
				return false
			}
		}
		return true
	}, "domain events were not dispatched")
}

func eventually(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func setupAdmin(t *testing.T, client *http.Client, baseURL string) (*http.Cookie, string) {
	t.Helper()
	payload := map[string]any{
		"email":    fixtureEmail("admin"),
		"password": "correct horse",
	}
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/setup", payload, nil, "", baseURL)
	requireStatus(t, resp, body, http.StatusCreated)
	if len(resp.Cookies()) == 0 {
		t.Fatalf("setup response did not set cookie: %s", body)
	}
	return resp.Cookies()[0], decodeString(t, body, "csrf_token")
}

func loginUser(t *testing.T, client *http.Client, baseURL, email, password string) (*http.Cookie, string) {
	t.Helper()
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/login", map[string]any{
		"email":    fixtureEmail(email),
		"password": password,
	}, nil, "", baseURL)
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
	id := decodeInt64(t, body, "id")
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode create user response: %v", err)
	}
	if _, ok := response["account_link"]; ok {
		password, _ := payload["password"].(string)
		link := decodeNestedString(t, body, "account_link", "url")
		token := accountLinkTokenFromURL(t, link)
		resp, body = doJSON(t, client, http.MethodPost, baseURL+"/api/account-links/"+token, map[string]any{
			"password": password,
		}, nil, "", baseURL)
		requireStatus(t, resp, body, http.StatusOK)
	}
	return id
}

func fixtureEmail(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "@") {
		return value
	}
	return value + "@example.test"
}

func addProductMember(t *testing.T, client *http.Client, baseURL string, cookie *http.Cookie, csrf string, productID, userID int64, role string) {
	t.Helper()
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/products/"+itoa(productID)+"/members", map[string]any{
		"user_id": userID,
		"role":    role,
	}, cookie, csrf, baseURL)
	requireStatus(t, resp, body, http.StatusCreated)
}

func requireSessionCookieTTL(t *testing.T, cookie *http.Cookie, started time.Time, ttl time.Duration) {
	t.Helper()
	if cookie == nil {
		t.Fatal("missing session cookie")
	}
	minExpires := started.Add(ttl - time.Minute)
	maxExpires := started.Add(ttl + time.Minute)
	if cookie.Expires.Before(minExpires) || cookie.Expires.After(maxExpires) {
		t.Fatalf("session cookie expires at %s, want within %s and %s", cookie.Expires, minExpires, maxExpires)
	}
	minMaxAge := int((ttl - time.Minute).Seconds())
	maxMaxAge := int(ttl.Seconds())
	if cookie.MaxAge < minMaxAge || cookie.MaxAge > maxMaxAge {
		t.Fatalf("session cookie max-age = %d, want between %d and %d", cookie.MaxAge, minMaxAge, maxMaxAge)
	}
}

func expireSessionToken(t *testing.T, tracker *store.Store, token string) {
	t.Helper()
	changed := execStoreSQL(t, tracker, `UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, pastTimestamp(), security.HashToken(token))
	if changed == 0 {
		t.Fatal("session was not expired")
	}
}

func expireAccountLinkToken(t *testing.T, tracker *store.Store, token string) {
	t.Helper()
	changed := execStoreSQL(t, tracker, `UPDATE account_links SET expires_at = ? WHERE token_hash = ?`, pastTimestamp(), security.HashToken(token))
	if changed == 0 {
		t.Fatal("account link was not expired")
	}
}

func makePendingWebhookNotificationsDue(t *testing.T, tracker *store.Store) {
	t.Helper()
	changed := execStoreSQL(t, tracker, `UPDATE webhook_notifications SET next_attempt_at = ? WHERE status = 'pending'`, pastTimestamp())
	if changed == 0 {
		t.Fatal("no pending webhook notifications were made due")
	}
}

func execStoreSQL(t *testing.T, tracker *store.Store, query string, args ...any) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", tracker.Path())
	if err != nil {
		t.Fatalf("open test store connection: %v", err)
	}
	defer db.Close()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("execute test store SQL: %v", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("read affected rows: %v", err)
	}
	return changed
}

func pastTimestamp() string {
	return time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
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
	headers := map[string]string{}
	if origin != "" {
		headers["Origin"] = origin
	}
	return doJSONWithHeaders(t, client, method, rawURL, payload, cookie, csrf, headers)
}

func doJSONWithHeaders(t *testing.T, client *http.Client, method, rawURL string, payload any, cookie *http.Cookie, csrf string, headers map[string]string) (*http.Response, []byte) {
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
	for name, value := range headers {
		if value != "" {
			req.Header.Set(name, value)
		}
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
	waitForDomainEvents(t, tracker)
	var matches []store.EmailNotification
	for _, notification := range mustEmailNotifications(t, tracker, 100) {
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

func requireStoreList[T any](t *testing.T, values []T, err error) []T {
	t.Helper()
	if err != nil {
		t.Fatalf("list store values: %v", err)
	}
	return values
}

func mustDomainEvents(t *testing.T, tracker *store.Store, limit int) []store.DomainEvent {
	values, err := tracker.ListDomainEvents(limit)
	return requireStoreList(t, values, err)
}

func mustEmailNotifications(t *testing.T, tracker *store.Store, limit int) []store.EmailNotification {
	page, err := tracker.ListEmailNotificationsPage(store.EmailNotificationFilter{Limit: limit})
	return requireStoreList(t, page.Notifications, err)
}
