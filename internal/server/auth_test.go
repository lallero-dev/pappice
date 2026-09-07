package server

import (
	"net/http"
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
