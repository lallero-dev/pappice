// Package browser adapts Pappice's HTTP handlers to an in-memory instance.
package browser

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"pappice/demo/internal/data"
	"pappice/internal/server"
	"pappice/internal/store"
)

// This origin exists only inside the worker; no HTTP listener is started.
const origin = "https://pappice.browser"

type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type App struct {
	store   *store.Store
	handler http.Handler
	jar     http.CookieJar
	cancel  context.CancelFunc
	workers sync.WaitGroup
}

func New(pageURL string) (*App, error) {
	tracker, err := store.Open(":memory:")
	if err != nil {
		return nil, fmt.Errorf("open browser database: %w", err)
	}
	if _, err := data.Seed(tracker); err != nil {
		tracker.Close()
		return nil, fmt.Errorf("seed browser database: %w", err)
	}
	handler := server.NewServer(tracker, server.Options{
		PublicURL: strings.SplitN(pageURL, "#", 2)[0] + "#",
		Branding:  server.Branding{Subtitle: "browser demo"},
		Logger:    log.Default(),
		UploadDir: "/browser/uploads",
		BackupDir: "/browser/backups",
	})
	jar, _ := cookiejar.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	app := &App{store: tracker, handler: handler, jar: jar, cancel: cancel}
	app.workers.Go(func() { handler.RunEventDispatcher(ctx, time.Second) })
	return app, nil
}

func (a *App) Close() error {
	a.cancel()
	a.workers.Wait()
	return a.store.Close()
}

// Request dispatches locally and keeps session cookies inside this instance.
// The browser never sends credentials or ticket data to the static host.
func (a *App) Request(input Request) Response {
	path, err := url.ParseRequestURI(input.Path)
	if err != nil || path.IsAbs() || path.Host != "" || !strings.HasPrefix(path.Path, "/api/") {
		return Response{Status: http.StatusBadRequest, Body: `{"error":"invalid local API path"}`}
	}
	r, err := http.NewRequest(input.Method, origin+input.Path, strings.NewReader(input.Body))
	if err != nil {
		return Response{Status: http.StatusBadRequest, Body: `{"error":"invalid local API request"}`}
	}
	// Model a same-origin HTTPS request without opening a network connection.
	r.TLS = &tls.ConnectionState{}
	r.RemoteAddr = "127.0.0.1:0"
	r.RequestURI = r.URL.RequestURI()
	for key, value := range input.Headers {
		r.Header.Set(key, value)
	}
	r.Header.Set("Origin", origin)
	r.Header.Del("Cookie")
	for _, cookie := range a.jar.Cookies(r.URL) {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	// Configuration and history work normally. Only unsupported I/O is rejected.
	var unavailable string
	switch {
	case strings.HasPrefix(path.Path, "/api/webhooks/") && strings.HasSuffix(path.Path, "/test"):
		unavailable = `{"error":"Outgoing webhook delivery is unavailable in the browser demo. Webhook configuration is stored locally."}`
	case strings.HasPrefix(path.Path, "/api/attachments/"), strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/"):
		unavailable = `{"error":"File uploads and downloads are unavailable in the browser demo."}`
	}
	if unavailable != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		w.WriteString(unavailable)
	} else {
		a.handler.ServeHTTP(w, r)
	}
	a.jar.SetCookies(r.URL, w.Result().Cookies())
	headers := make(map[string]string)
	for key := range w.Header() {
		if key != "Set-Cookie" {
			headers[key] = w.Header().Get(key)
		}
	}
	return Response{Status: w.Code, Headers: headers, Body: w.Body.String()}
}
