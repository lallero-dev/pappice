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
		"password": "correct horse",
		"role":     "user",
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

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/issues", map[string]any{
		"title": "Viewer cannot create",
	}, bobCookie, bobCSRF, server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/members", map[string]any{
		"user_id": bobID,
		"role":    "reporter",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/issues", map[string]any{
		"title": "Reporter can create",
	}, bobCookie, bobCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	issueID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/issues/"+itoa(issueID), map[string]any{
		"status": "assigned",
	}, bobCookie, bobCSRF, server.URL)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reporter patch status = %d body=%s, want 403", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/projects/"+itoa(projectID)+"/members", map[string]any{
		"user_id": bobID,
		"role":    "developer",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/issues/"+itoa(issueID), map[string]any{
		"status": "assigned",
	}, bobCookie, bobCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
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

func requireStatus(t *testing.T, resp *http.Response, body []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d body=%s, want %d", resp.StatusCode, body, want)
	}
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
