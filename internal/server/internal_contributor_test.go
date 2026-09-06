package server

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pappice/internal/store"
)

func TestInternalContributor(t *testing.T) {
	for _, authMode := range []string{"session", "token"} {
		t.Run(authMode, func(t *testing.T) {
			tracker, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { tracker.Close() })
			admin, err := tracker.CreateFirstAdmin(store.CreateUser{Email: "admin@example.test", Password: "correct horse"})
			if err != nil {
				t.Fatal(err)
			}
			contributor, err := tracker.CreateUser(store.CreateUser{DisplayName: "Support Bot", Email: "bot@example.test", Role: "staff", Password: "correct horse"})
			if err != nil {
				t.Fatal(err)
			}
			products, err := tracker.ListProducts(admin)
			if err != nil {
				t.Fatal(err)
			}
			productID := products[0].ID
			setRole := func(role string) {
				t.Helper()
				if _, err := tracker.UpsertProductMember(productID, store.UpsertProductMember{UserID: contributor.ID, Role: role}); err != nil {
					t.Fatal(err)
				}
			}
			setRole("internal_contributor")
			ticket, err := tracker.CreateTicket(store.CreateTicket{ProductID: productID, Title: "Needs investigation", Description: "Original description", ActorUserID: admin.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tracker.SaveTicket(store.SaveTicketInput{
				TicketID: ticket.ID, ActorUserID: admin.ID,
				Patch:   store.UpdateTicket{Status: new("closed")},
				Comment: &store.AddComment{Body: "Existing internal note", Visibility: "internal"},
			}); err != nil {
				t.Fatal(err)
			}
			// Ignore creation and status history when checking the unread note count.
			execStoreSQL(t, tracker, `UPDATE tickets SET created_at = ? WHERE id = ?`, pastTimestamp(), ticket.ID)
			execStoreSQL(t, tracker, `UPDATE ticket_status_changes SET created_at = ? WHERE ticket_id = ?`, pastTimestamp(), ticket.ID)
			if err := tracker.MarkTicketRead(ticket.ID, contributor.ID, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			uploadDir := t.TempDir()
			app := NewServer(tracker, Options{UploadDir: uploadDir})
			session, csrf, _, err := tracker.CreateSession(contributor.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, token, err := tracker.CreateAPIToken(contributor.ID, store.CreateAPIToken{Name: "automation"})
			if err != nil {
				t.Fatal(err)
			}
			send := func(method, path, contentType, body string, want int) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(method, "https://pappice.test"+path, strings.NewReader(body))
				req.Header.Set("Content-Type", contentType)
				if authMode == "token" {
					req.Header.Set("Authorization", "Bearer "+token)
				} else {
					req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
					req.Header.Set("Origin", "https://pappice.test")
					req.Header.Set("X-Pappice-CSRF", csrf)
				}
				w := httptest.NewRecorder()
				app.ServeHTTP(w, req)
				if w.Code != want {
					t.Fatalf("%s %s: status = %d, want %d, body = %s", method, path, w.Code, want, w.Body)
				}
				return w
			}
			path := "/api/tickets/" + itoa(ticket.ID)
			productPath := "/api/products/" + itoa(productID)
			for _, route := range []string{path, "/api/tickets/key/" + ticket.Key} {
				w := send("GET", route, "", "", 200)
				if !strings.Contains(w.Body.String(), "Existing internal note") || !strings.Contains(w.Body.String(), `"unread_count":1`) {
					t.Fatalf("internal note or unread count missing: %s", w.Body)
				}
			}
			w := send("GET", "/api/tickets?status=closed&unread=1&q=investigation", "", "", 200)
			if !strings.Contains(w.Body.String(), ticket.Title) || !strings.Contains(w.Body.String(), `"unread_count":1`) {
				t.Fatalf("unread ticket missing from filtered list: %s", w.Body)
			}

			beforeEvents := len(mustDomainEvents(t, tracker, 100))
			for _, method := range []string{"POST", "PATCH"} {
				endpoint, body := path+"/comments", `{"body":"Internal suggestion","visibility":"internal"}`
				status := 201
				if method == "PATCH" {
					endpoint, body, status = path, `{"comment":`+body+`}`, 200
				}
				send(method, endpoint, "application/json", body, status)
			}
			for _, mutation := range []struct{ method, route, body string }{
				{"POST", path + "/comments", `{"body":"Public reply","visibility":"public"}`},
				{"POST", path + "/comments", `{"body":"Default visibility"}`},
				{"PATCH", path, `{"comment":{"body":"Public reply","visibility":"public"}}`},
				{"PATCH", path, `{"comment":{"body":"Default visibility"}}`},
				{"PATCH", path, `{"title":"Changed"}`},
				{"PATCH", path, `{"description":"Changed"}`},
				{"PATCH", path, `{"priority":"urgent"}`},
				{"PATCH", path, `{"status":"open"}`},
				{"PATCH", path, `{"assignee_user_id":0}`},
				{"PATCH", path, `{"status":"open","comment":{"body":"Mixed mutation","visibility":"internal"}}`},
				{"POST", "/api/tickets", `{"product_id":` + itoa(productID) + `,"title":"New ticket"}`},
				{"POST", productPath + "/tickets", `{"title":"New ticket"}`},
				{"DELETE", path, ``},
				{"PATCH", productPath, `{"name":"Changed"}`},
				{"POST", productPath + "/members", `{"user_id":` + itoa(contributor.ID) + `,"role":"staff"}`},
				{"GET", productPath + "/webhooks", ``},
				{"POST", productPath + "/webhooks", `{"name":"Unwanted hook","url":"https://example.test/hook"}`},
			} {
				send(mutation.method, mutation.route, "application/json", mutation.body, 403)
			}
			// Check both multipart paths, including attachment-only notes and omitted visibility.
			for _, method := range []string{"POST", "PATCH"} {
				for _, visibility := range []string{"internal", "public", ""} {
					var body bytes.Buffer
					writer := multipart.NewWriter(&body)
					if visibility != "" {
						if err := writer.WriteField("visibility", visibility); err != nil {
							t.Fatal(err)
						}
					}
					part, err := writer.CreateFormFile("attachments", "context.txt")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := io.WriteString(part, "Private attachment"); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					endpoint, want := path+"/comments", 403
					if method == "PATCH" {
						endpoint = path
					}
					if visibility == "internal" {
						want = 201
						if method == "PATCH" {
							want = 200
						}
					}
					send(method, endpoint, writer.FormDataContentType(), body.String(), want)
				}
			}
			updated, err := tracker.GetTicket(ticket.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(updated.Comments) != 5 || updated.Title != ticket.Title || updated.Description != ticket.Description || updated.Priority != ticket.Priority || updated.AssigneeUserID != 0 || updated.Status != "closed" || len(updated.StatusChanges) != 1 {
				t.Fatalf("unexpected ticket changes: %#v", updated)
			}
			for _, comment := range updated.Comments[1:] {
				if comment.Visibility != "internal" || comment.AuthorUserID != contributor.ID || comment.Author != "Support Bot" {
					t.Fatalf("wrong note visibility or identity: %#v", comment)
				}
			}
			if events := mustDomainEvents(t, tracker, 100); len(events) != beforeEvents {
				t.Fatalf("internal notes or rejected mutations emitted notification events: %#v", events)
			}
			attachmentPath := "/api/attachments/" + itoa(updated.Comments[3].Attachments[0].ID)
			if w := send("GET", attachmentPath, "", "", 200); w.Body.String() != "Private attachment" {
				t.Fatalf("attachment = %s", w.Body)
			}
			files := 0
			if err := filepath.WalkDir(uploadDir, func(_ string, entry fs.DirEntry, err error) error {
				if err == nil && !entry.IsDir() {
					files++
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if files != 2 {
				t.Fatalf("stored files = %d, want 2", files)
			}
			// Contributors cannot be assigned tickets, even by an administrator.
			if _, err := tracker.SaveTicket(store.SaveTicketInput{TicketID: ticket.ID, ActorUserID: admin.ID, Patch: store.UpdateTicket{AssigneeUserID: &contributor.ID}}); err == nil {
				t.Fatal("contributor was accepted as an assignee")
			}
			// Existing credentials follow membership changes immediately.
			setRole("viewer")
			if w := send("GET", path, "", "", 200); strings.Contains(w.Body.String(), "Existing internal note") {
				t.Fatalf("viewer sees internal notes: %s", w.Body)
			}
			send("GET", attachmentPath, "", "", 404)
			send("POST", path+"/comments", "application/json", `{"body":"No longer allowed","visibility":"internal"}`, 403)
			setRole("staff")
			send("POST", path+"/comments", "application/json", `{"body":"Now public"}`, 201)
			setRole("internal_contributor")
			send("POST", path+"/comments", "application/json", `{"body":"Public again"}`, 403)
			if err := tracker.DeleteProductMember(productID, contributor.ID, store.EventContext{}); err != nil {
				t.Fatal(err)
			}
			send("GET", path, "", "", 404)
		})
	}
}

func TestInternalContributorGlobalRoleBoundaries(t *testing.T) {
	tracker, server, client := newTestServer(t)
	cookie, csrf := setupAdmin(t, client, server.URL)
	resp, body := doJSON(t, client, "GET", server.URL+"/api/products", nil, cookie, "", "")
	requireStatus(t, resp, body, 200)
	productID := decodeFirstProductID(t, body)
	customerID := createUser(t, client, server.URL, cookie, csrf, map[string]any{"email": "customer@example.test", "role": "customer", "password": "correct horse"})
	addProductMember(t, client, server.URL, cookie, csrf, productID, customerID, "customer")
	ticket, err := tracker.CreateTicket(store.CreateTicket{ProductID: productID, Title: "Customer ticket", ActorUserID: customerID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.SaveTicket(store.SaveTicketInput{TicketID: ticket.ID, ActorUserID: 1, Comment: &store.AddComment{Body: "Private note", Visibility: "internal"}}); err != nil {
		t.Fatal(err)
	}
	addProductMember(t, client, server.URL, cookie, csrf, productID, customerID, "internal_contributor")
	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")
	path := server.URL + "/api/tickets/" + itoa(ticket.ID)
	resp, body = doJSON(t, client, "GET", path, nil, customerCookie, "", "")
	requireStatus(t, resp, body, 200)
	if bytes.Contains(body, []byte("Private note")) {
		t.Fatalf("customer sees internal note: %s", body)
	}
	resp, body = doJSON(t, client, "POST", path+"/comments", map[string]any{"body": "No staff privileges", "visibility": "internal"}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, 403)
	addProductMember(t, client, server.URL, cookie, csrf, productID, 1, "internal_contributor")
	resp, body = doJSON(t, client, "PATCH", path, map[string]any{"title": "Admin still manages tickets"}, cookie, csrf, server.URL)
	requireStatus(t, resp, body, 200)
	var result store.Ticket
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Title != "Admin still manages tickets" || len(result.Comments) != 1 {
		t.Fatalf("admin lost access: %#v", result)
	}
}
