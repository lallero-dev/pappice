package browser

import (
	"encoding/json"
	"net/http"
	"testing"

	"pappice/demo/internal/data"
)

func TestPrivateInstance(t *testing.T) {
	first, err := New("https://example.test/demo/")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	second, err := New("https://example.test/demo/")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	login, _ := json.Marshal(map[string]string{"email": "admin@example.test", "password": data.Password})
	response := first.Request(Request{
		Method: "POST", Path: "/api/login", Body: string(login),
		Headers: map[string]string{"Content-Type": "application/json"},
	})
	if response.Status != http.StatusOK {
		t.Fatalf("login = %d: %s", response.Status, response.Body)
	}
	if _, ok := response.Headers["Set-Cookie"]; ok {
		t.Fatal("local session cookie leaked to the page")
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal([]byte(response.Body), &session); err != nil || session.CSRF == "" {
		t.Fatalf("login CSRF = %q: %v", session.CSRF, err)
	}
	mutation := Request{Method: "POST", Path: "/api/products", Body: `{"key":"LOCAL","name":"Private product"}`, Headers: map[string]string{"Content-Type": "application/json"}}
	if response := first.Request(mutation); response.Status != http.StatusForbidden {
		t.Fatalf("mutation without CSRF = %d: %s", response.Status, response.Body)
	}
	mutation.Headers["X-Pappice-CSRF"] = session.CSRF
	if response := first.Request(mutation); response.Status != http.StatusCreated {
		t.Fatalf("create product = %d: %s", response.Status, response.Body)
	}
	// Both stores have the same demo accounts, but neither sessions nor data cross.
	if response := second.Request(Request{Method: "GET", Path: "/api/products"}); response.Status != http.StatusUnauthorized {
		t.Fatalf("second instance inherited authentication: %s", response.Body)
	}
	for _, app := range []*App{first, second} {
		user, err := app.store.Authenticate("admin@example.test", data.Password)
		if err != nil {
			t.Fatal(err)
		}
		products, err := app.store.ListProducts(user)
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if app == first {
			want = 2
		}
		if len(products) != want {
			t.Fatalf("products = %d, want %d", len(products), want)
		}
	}
	logout := Request{Method: "POST", Path: "/api/logout", Headers: mutation.Headers}
	if response := first.Request(logout); response.Status != http.StatusOK {
		t.Fatalf("logout = %d: %s", response.Status, response.Body)
	}
	if response := first.Request(Request{Method: "GET", Path: "/api/products"}); response.Status != http.StatusUnauthorized {
		t.Fatalf("logout retained the local cookie: %s", response.Body)
	}
}

func TestUnavailableBrowserOperations(t *testing.T) {
	app, err := New("https://example.test/demo/")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, path := range []string{"/api/webhooks/1/test", "/api/attachments/1"} {
		response := app.Request(Request{Method: "GET", Path: path})
		if response.Status != http.StatusNotImplemented {
			t.Errorf("%s = %d: %s", path, response.Status, response.Body)
		}
	}
	for _, path := range []string{"https://example.test/api/users", "/static/app.js", "//example.test/api/users"} {
		if response := app.Request(Request{Method: "GET", Path: path}); response.Status != http.StatusBadRequest {
			t.Errorf("external or non-API path %s = %d", path, response.Status)
		}
	}
}

func TestMalformedRequest(t *testing.T) {
	app, err := New("https://example.test/demo/")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, request := range []Request{
		{Method: "GET /", Path: "/api/health"},
		{Method: "GET\n", Path: "/api/health"},
		{Method: "GET", Path: "/api/%"},
	} {
		if response := app.Request(request); response.Status != http.StatusBadRequest {
			t.Errorf("malformed request %#v = %d", request, response.Status)
		}
	}
	if response := app.Request(Request{Method: "GET", Path: "/api/health"}); response.Status != http.StatusOK {
		t.Fatalf("healthy request after malformed input = %d: %s", response.Status, response.Body)
	}
}
