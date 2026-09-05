package server

import (
	"bytes"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"pappice/internal/store"
)

func TestAPITicketIdempotency(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tracker.Close() })
	admin, err := tracker.CreateFirstAdmin(store.CreateUser{DisplayName: "Support Bot", Email: "bot@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	products, err := tracker.ListProducts(admin)
	if err != nil {
		t.Fatal(err)
	}
	newTicket := func() store.Ticket {
		t.Helper()
		ticket, err := tracker.CreateTicket(store.CreateTicket{ProductID: products[0].ID, Title: "Retry", ActorUserID: admin.ID})
		if err != nil {
			t.Fatal(err)
		}
		return ticket
	}
	ticket := newTicket()
	_, token, err := tracker.CreateAPIToken(admin.ID, store.CreateAPIToken{Name: "automation"})
	if err != nil {
		t.Fatal(err)
	}
	uploadDir := t.TempDir()
	app := NewServer(tracker, Options{UploadDir: uploadDir})
	send := func(method, path, contentType, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "https://pappice.test"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		return w
	}
	path := "/api/tickets/" + itoa(ticket.ID)
	for _, visibility := range []string{"public", "internal"} {
		body := `{"body":"Suggested reply","visibility":"` + visibility + `"}`
		for attempt := range 2 {
			w := send(http.MethodPost, path+"/comments", "application/json", visibility, body)
			if w.Code != http.StatusCreated || (w.Header().Get("Idempotency-Replayed") == "true") != (attempt == 1) {
				t.Fatalf("%s attempt %d: status = %d, headers = %v, body = %s", visibility, attempt, w.Code, w.Header(), w.Body)
			}
		}
		w := send(http.MethodPost, path+"/comments", "application/json", visibility, `{"body":"Changed","visibility":"`+visibility+`"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("conflict status = %d, body = %s", w.Code, w.Body)
		}
	}
	for _, key := range []string{"", "has spaces", strings.Repeat("x", 201)} {
		w := send(http.MethodPost, path+"/comments", "application/json", key, `{"body":"Invalid key"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid key status = %d, body = %s", w.Code, w.Body)
		}
	}
	for attempt := range 2 {
		w := send(http.MethodPatch, path, "application/json", "close", `{"status":"closed","comment":{"body":"Closing","visibility":"internal"}}`)
		if w.Code != http.StatusOK || (w.Header().Get("Idempotency-Replayed") == "true") != (attempt == 1) {
			t.Fatalf("patch status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
		}
	}
	// A multipart retry gets fresh files on disk, which must be cleaned up.
	for attempt := range 2 {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("visibility", "internal"); err != nil {
			t.Fatal(err)
		}
		file, err := writer.CreateFormFile("attachments", "context.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, "Product context"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		w := send(http.MethodPost, path+"/comments", writer.FormDataContentType(), "attachment", body.String())
		if w.Code != http.StatusCreated || (w.Header().Get("Idempotency-Replayed") == "true") != (attempt == 1) {
			t.Fatalf("upload status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
		}
	}
	ticket, err = tracker.GetTicket(ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticket.Comments) != 4 || len(ticket.StatusChanges) != 1 || ticket.Status != "closed" {
		t.Fatalf("ticket = %#v", ticket)
	}
	for _, comment := range ticket.Comments {
		if comment.AuthorUserID != admin.ID || comment.Author != "Support Bot" {
			t.Fatalf("API reply identity = %#v", comment)
		}
	}
	files := 0
	err = filepath.WalkDir(uploadDir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files++
		}
		return err
	})
	if err != nil || files != 1 {
		t.Fatalf("upload files = %d, err = %v", files, err)
	}
	// Keys belong to a user and ticket, so separate tickets can use the same key.
	other := newTicket()
	w := send(http.MethodPost, "/api/tickets/"+itoa(other.ID)+"/comments", "application/json", "public", `{"body":"Another ticket"}`)
	if w.Code != http.StatusCreated || w.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("other ticket status = %d, body = %s", w.Code, w.Body)
	}
	// Replays still require current permissions, including internal-note access.
	if _, err := tracker.CreateUser(store.CreateUser{Email: "admin@example.test", Role: "admin", Password: "correct horse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.UpdateUser(admin.ID, store.UpdateUser{Role: new("customer")}); err != nil {
		t.Fatal(err)
	}
	w = send(http.MethodPost, path+"/comments", "application/json", "internal", `{"body":"Suggested reply","visibility":"internal"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("replay bypassed permissions: status = %d, body = %s", w.Code, w.Body)
	}
}
