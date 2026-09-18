package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"pappice/internal/store"
)

func TestFailedUploadCleanupPreservesCommittedAttachment(t *testing.T) {
	_, server, client := newTestServer(t, Options{UploadDir: t.TempDir()})
	cookie, csrf := setupAdmin(t, client, server.URL)
	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, cookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	const content = "identical attachment content"
	source, err := os.CreateTemp(t.TempDir(), "source-*")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	// Pause request A after writing its file, then let request B commit the same content.
	app := server.Config.Handler.(*Server)
	upload, err := app.saveUploadedFile(source, &multipart.FileHeader{Filename: "shared.txt"})
	if err != nil {
		t.Fatal(err)
	}
	resp, body = doMultipart(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]string{
		"product_id": itoa(productID),
		"title":      "Committed attachment",
	}, []testUpload{{Field: "attachments", Filename: "shared.txt", Body: content}}, cookie, csrf, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	var ticket store.Ticket
	if err := json.Unmarshal(body, &ticket); err != nil {
		t.Fatal(err)
	}
	if len(ticket.Attachments) != 1 {
		t.Fatalf("attachments = %#v", ticket.Attachments)
	}

	// Request A fails. Its cleanup must leave B's committed attachment readable.
	cleanupStoredUploads([]storedUpload{upload})
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/attachments/"+itoa(ticket.Attachments[0].ID), nil, cookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if string(body) != content {
		t.Fatalf("attachment content = %q, want %q", body, content)
	}
	if _, err := os.Stat(upload.Path); !os.IsNotExist(err) {
		t.Fatalf("failed upload remains: %v", err)
	}
}

func TestRejectedUploadsAreRemoved(t *testing.T) {
	uploadDir := t.TempDir()
	_, server, client := newTestServer(t, Options{UploadDir: uploadDir})
	cookie, csrf := setupAdmin(t, client, server.URL)
	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, cookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	for _, test := range []struct {
		name  string
		title string
		files []testUpload
	}{
		{"invalid ticket", "", []testUpload{{Field: "attachments", Filename: "log.txt", Body: "log content"}}},
		{"invalid second file", "Ticket", []testUpload{
			{Field: "attachments", Filename: "log.txt", Body: "log content"},
			{Field: "attachments", Filename: "page.html", Body: "<!DOCTYPE html><html>blocked</html>"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp, body := doMultipart(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]string{
				"product_id": itoa(productID),
				"title":      test.title,
			}, test.files, cookie, csrf, server.URL)
			requireStatus(t, resp, body, http.StatusBadRequest)
			if err := filepath.WalkDir(uploadDir, func(path string, entry fs.DirEntry, err error) error {
				if err == nil && !entry.IsDir() {
					t.Errorf("rejected upload left file %s", path)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTicketAttachmentsVisibilityAndDownload(t *testing.T) {
	_, server, client := newTestServer(t, Options{UploadDir: t.TempDir()})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
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

func TestAttachmentFilePathRejectsTraversal(t *testing.T) {
	uploadDir := t.TempDir()
	server := NewServer(nil, Options{UploadDir: uploadDir})

	for _, storageKey := range []string{"..", "../secret.txt", "/secret.txt"} {
		t.Run(storageKey, func(t *testing.T) {
			path, err := server.attachmentFilePath(storageKey)
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("attachmentFilePath(%q) = %q, %v; want not exist", storageKey, path, err)
			}
		})
	}

	path, err := server.attachmentFilePath("ab/cd/hash")
	if err != nil {
		t.Fatalf("valid attachment path: %v", err)
	}
	if path != filepath.Join(uploadDir, "ab", "cd", "hash") {
		t.Fatalf("valid attachment path = %q", path)
	}
}

func TestSanitizeAttachmentFilenamePreservesUnicodeAndExtension(t *testing.T) {
	filename := sanitizeAttachmentFilename(strings.Repeat("è", 100) + ".png")
	if !strings.HasSuffix(filename, ".png") {
		t.Fatalf("sanitized filename = %q, want preserved extension", filename)
	}
	if len(filename) > 180 || !utf8.ValidString(filename) {
		t.Fatalf("sanitized filename = %q, bytes=%d, want valid UTF-8 within limit", filename, len(filename))
	}
}

func TestMultipartTicketPatchUpdatesFieldsCommentAndAttachments(t *testing.T) {
	uploadDir := t.TempDir()
	tracker, server, client := newTestServer(t, Options{UploadDir: uploadDir})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"email":    "patchdev@example.test",
		"password": "correct horse",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, devID, "staff")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Multipart patch source",
		"description": "Original description",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doMultipart(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]string{
		"title":            "Multipart patched ticket",
		"description":      "Updated through a multipart save",
		"status":           "closed",
		"priority":         "high",
		"assignee_user_id": itoa(devID),
		"body":             "Patch evidence attached",
		"visibility":       "public",
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
		patched.Status != "closed" ||
		patched.Priority != "high" ||
		patched.AssigneeUserID != devID || patched.AssigneeEmail != "patchdev@example.test" {
		t.Fatalf("multipart patch did not update ticket fields: %#v", patched)
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
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)
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
