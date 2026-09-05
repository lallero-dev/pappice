package server

import (
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"

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
