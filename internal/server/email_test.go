package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pappice/internal/store"
)

func TestAccountLinkProjectionResolvesUserByID(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "pappice.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	admin, err := tracker.CreateFirstAdmin(store.CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	user, _, _, err := tracker.CreateUserWithSetupLink(store.CreateUser{
		Email: "old@example.test",
		Role:  "staff",
		Event: store.EventContext{Enabled: true, Actor: store.EventActorFromUser(admin)},
	}, time.Hour)
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}
	newEmail := "new@example.test"
	newName := "Current Name"
	if _, err := tracker.UpdateUser(user.ID, store.UpdateUser{Email: &newEmail, DisplayName: &newName}); err != nil {
		t.Fatalf("update pending user: %v", err)
	}
	events := mustDomainEvents(t, tracker, 10)
	if len(events) != 1 {
		t.Fatalf("domain events = %#v", events)
	}

	server := &Server{store: tracker, options: Options{EmailNotifications: true}}
	projection, err := server.domainEventProjection(events[0])
	if err != nil {
		t.Fatalf("project account event: %v", err)
	}
	if len(projection.EmailNotifications) != 1 || projection.EmailNotifications[0].UserID != user.ID ||
		projection.EmailNotifications[0].RecipientEmail != newEmail || projection.EmailNotifications[0].RecipientName != newName {
		t.Fatalf("email notifications = %#v", projection.EmailNotifications)
	}
	if err := tracker.DeleteUser(user.ID, store.EventContext{}); err != nil {
		t.Fatalf("delete pending user: %v", err)
	}
	projection, err = server.domainEventProjection(events[0])
	if err != nil {
		t.Fatalf("project deleted account event: %v", err)
	}
	if len(projection.EmailNotifications) != 0 {
		t.Fatalf("deleted account notifications = %#v", projection.EmailNotifications)
	}
}

func TestRequesterEmailContentUsesReadableLayout(t *testing.T) {
	server := &Server{options: Options{PublicURL: "https://tracker.example.test"}}
	ticket := store.Ticket{
		Key:        "PME-1",
		ProductKey: "PME",
		Title:      "Need <help>",
		Status:     "closed",
		Comments: []store.Comment{{
			Author:     "Alice",
			Body:       "Please try the updated setup.\nIt should work now.",
			Visibility: "public",
		}},
	}

	subject, textBody, htmlBody := server.requesterEmailContent("ticket.commented", ticket, "Alice")

	if subject != "[PME-1] Ticket update: Need <help>" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"Alice replied to your ticket.",
		"Ticket: PME-1",
		"Status: Closed",
		"Latest public reply from Alice:",
		"Open your ticket:\nhttps://tracker.example.test/",
		"Replies to this email are not read.",
	} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("text body missing %q:\n%s", want, textBody)
		}
	}
	for _, want := range []string{
		"Pappice customer support",
		"Need &lt;help&gt;",
		"Latest public reply",
		"from Alice",
		`<table role="presentation"`,
		"Please try the updated setup.<br>It should work now.",
	} {
		if !strings.Contains(htmlBody, want) {
			t.Fatalf("html body missing %q:\n%s", want, htmlBody)
		}
	}
	if strings.Contains(htmlBody, "Need <help>") {
		t.Fatalf("html body did not escape title:\n%s", htmlBody)
	}
}

func TestTicketEmailContentUsesReadableLayout(t *testing.T) {
	server := &Server{options: Options{PublicURL: "https://tracker.example.test"}}
	ticket := store.Ticket{
		Key:           "PME-2",
		ProductKey:    "PME",
		ProductName:   "Pappice",
		Title:         "Cannot sign in",
		Description:   "Login fails after password reset.",
		Status:        "open",
		Priority:      "urgent",
		AssigneeEmail: "dev@example.test",
		RequesterName: "Customer",
	}
	actor := store.EventActor{DisplayName: "Paolo", Email: "paolo@example.test"}

	subject, textBody, htmlBody := server.ticketEmailContent("ticket.assigned", ticket, actor)

	if subject != "[PME-2] Ticket update: Cannot sign in" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"Paolo assigned PME-2.",
		"Product: Pappice",
		"Priority: urgent",
		"Description:\nLogin fails after password reset.",
		"Open in Pappice:\nhttps://tracker.example.test/",
	} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("text body missing %q:\n%s", want, textBody)
		}
	}
	for _, want := range []string{
		"Pappice staff notification",
		"Pappice",
		"Login fails after password reset.",
		`href="https://tracker.example.test/"`,
	} {
		if !strings.Contains(htmlBody, want) {
			t.Fatalf("html body missing %q:\n%s", want, htmlBody)
		}
	}
}

func TestAdminProductTicketCommentAndNotificationFlow(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/products", map[string]any{
		"key":         "OPS",
		"name":        "Operations",
		"description": "Ops product",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	productID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/products/"+itoa(productID), map[string]any{
		"name":        "Operations Desk",
		"description": "Client operations",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if got := decodeString(t, body, "name"); got != "Operations Desk" {
		t.Fatalf("product name = %q body=%s", got, body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(productID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"password": "correct horse",
		"email":    "dev@example.test",
		"role":     "staff",
	})
	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"password": "correct horse",
		"email":    "customer@example.test",
		"role":     "customer",
	})
	disabledID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"password": "correct horse",
		"email":    "disabled@example.test",
		"role":     "staff",
	})
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/users/"+itoa(disabledID), map[string]any{"disabled": true}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodDelete, server.URL+"/api/users/"+itoa(disabledID), nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/users", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)

	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, devID, "staff")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/products/"+itoa(productID)+"/members", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("staff")) || !bytes.Contains(body, []byte("customer")) {
		t.Fatalf("members missing roles: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Dashboard fails",
		"description": "The dashboard cannot load",
		"priority":    "high",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")
	ticketKey := decodeString(t, body, "key")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/key/"+ticketKey, nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if decodeInt64(t, body, "id") != ticketID {
		t.Fatalf("ticket by key returned wrong ticket: %s", body)
	}

	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(ticketID), nil, customerCookie, "", "")
	requireStatus(t, resp, body, http.StatusNotFound)

	devCookie, devCSRF := loginUser(t, client, server.URL, "dev", "correct horse")
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(ticketID)+"/comments", map[string]any{
		"body":       "I can reproduce this",
		"visibility": "public",
	}, devCookie, devCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(ticketID)+"/comments", map[string]any{
		"body":       "Customer tries an internal note",
		"visibility": "internal",
	}, customerCookie, customerCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusNotFound)
	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets/"+itoa(ticketID)+"/comments", map[string]any{
		"body":       "Internal triage",
		"visibility": "internal",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"assignee_user_id": devID,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets?product_id="+itoa(productID)+"&status=open&assignee_user_id="+itoa(devID)+"&q=dashboard", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Dashboard fails")) {
		t.Fatalf("filtered tickets missing ticket: %s", body)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(ticketID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Internal triage")) || !bytes.Contains(body, []byte(`"visibility":"internal"`)) {
		t.Fatalf("ticket body missing internal note: %s", body)
	}
	waitForDomainEvents(t, tracker)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("ticket.assigned")) ||
		!bytes.Contains(body, []byte("Ticket update")) ||
		!bytes.Contains(body, []byte("dev@example.test")) {
		t.Fatalf("email outbox missing grouped update notification: %s", body)
	}
}

func TestEmailNotificationAdminTools(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/email-notifications/test", map[string]any{}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	var created struct {
		Notification store.EmailNotification `json:"notification"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode test email: %v", err)
	}
	if created.Notification.ID == 0 || created.Notification.Event != "email.test" || created.Notification.RecipientEmail != "admin@example.test" {
		t.Fatalf("test email notification = %#v", created.Notification)
	}

	if err := tracker.MarkEmailFailed(created.Notification.ID, errors.New("smtp unavailable"), 1); err != nil {
		t.Fatalf("mark test email failed: %v", err)
	}
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte(`"failed":1`)) || !bytes.Contains(body, []byte("smtp unavailable")) {
		t.Fatalf("email outbox missing failed overview: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/email-notifications/"+itoa(created.Notification.ID)+"/retry", nil, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	var retried struct {
		Notification store.EmailNotification `json:"notification"`
	}
	if err := json.Unmarshal(body, &retried); err != nil {
		t.Fatalf("decode retried email: %v", err)
	}
	if retried.Notification.Status != "pending" || retried.Notification.Attempts != 0 || retried.Notification.LastError != "" {
		t.Fatalf("retried notification = %#v", retried.Notification)
	}
}

func TestTicketSaveGroupsPatchAndCommentEmail(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{EmailNotifications: true})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"password": "correct horse",
		"email":    "dev@example.test",
		"role":     "staff",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, devID, "staff")

	resp, body = doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
		"product_id":  productID,
		"title":       "Grouped save",
		"description": "Needs one email",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusCreated)
	ticketID := decodeInt64(t, body, "id")

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"status":           "closed",
		"assignee_user_id": devID,
		"comment": map[string]any{
			"body":       "This should roll back",
			"visibility": "private",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusBadRequest)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/tickets/"+itoa(ticketID), nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if bytes.Contains(body, []byte("This should roll back")) || bytes.Contains(body, []byte(`"status":"closed"`)) {
		t.Fatalf("failed grouped save was not rolled back: %s", body)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(ticketID), map[string]any{
		"status":           "closed",
		"assignee_user_id": devID,
		"comment": map[string]any{
			"body":       "Taking this now",
			"visibility": "public",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("Taking this now")) || !bytes.Contains(body, []byte(`"status":"closed"`)) {
		t.Fatalf("grouped ticket save response = %s", body)
	}

	waitForDomainEvents(t, tracker)
	resp, body = doJSON(t, client, http.MethodGet, server.URL+"/api/email-notifications", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	if !bytes.Contains(body, []byte("ticket.updated")) ||
		!bytes.Contains(body, []byte("Ticket update")) ||
		!bytes.Contains(body, []byte("dev@example.test")) ||
		!bytes.Contains(body, []byte("Taking this now")) {
		t.Fatalf("email outbox missing grouped ticket update: %s", body)
	}
}

func TestRequesterNotificationPolicy(t *testing.T) {
	tracker, server, client := newTestServer(t, Options{
		EmailNotifications: true,
		PublicURL:          "https://tracker.example.test",
	})
	adminCookie, adminCSRF := setupAdmin(t, client, server.URL)

	resp, body := doJSON(t, client, http.MethodGet, server.URL+"/api/products", nil, adminCookie, "", "")
	requireStatus(t, resp, body, http.StatusOK)
	productID := decodeFirstProductID(t, body)

	devID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"password": "correct horse",
		"email":    "dev@example.test",
		"role":     "staff",
	})
	customerID := createUser(t, client, server.URL, adminCookie, adminCSRF, map[string]any{
		"display_name": "Customer",
		"email":        "customer@example.test",
		"password":     "correct horse",
		"role":         "customer",
	})
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, devID, "staff")
	addProductMember(t, client, server.URL, adminCookie, adminCSRF, productID, customerID, "customer")

	customerCookie, customerCSRF := loginUser(t, client, server.URL, "customer", "correct horse")
	createTicket := func(title string) int64 {
		t.Helper()
		resp, body := doJSON(t, client, http.MethodPost, server.URL+"/api/tickets", map[string]any{
			"product_id":  productID,
			"title":       title,
			"description": "Customer needs help",
		}, customerCookie, customerCSRF, server.URL)
		requireStatus(t, resp, body, http.StatusCreated)
		return decodeInt64(t, body, "id")
	}

	internalChangeID := createTicket("Internal-only change")
	notification := requireNotificationForTicketEmail(t, tracker, internalChangeID, "customer@example.test")
	if notification.Event != "ticket.created" {
		t.Fatalf("initial requester notification = %#v", notification)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(internalChangeID), map[string]any{
		"priority":         "urgent",
		"assignee_user_id": devID,
		"comment": map[string]any{
			"body":       "Internal triage details",
			"visibility": "internal",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	notification = requireNotificationForTicketEmail(t, tracker, internalChangeID, "customer@example.test")
	if notification.Event != "ticket.created" || strings.Contains(notification.BodyText, "Internal triage details") {
		t.Fatalf("internal-only change should not notify requester: %#v", notification)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(internalChangeID), map[string]any{
		"status": "closed",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	notification = requireNotificationForTicketEmail(t, tracker, internalChangeID, "customer@example.test")
	if notification.Event != "ticket.updated" || !strings.Contains(notification.BodyText, "Status: Closed") {
		t.Fatalf("closed status should notify requester: %#v", notification)
	}

	publicReplyID := createTicket("Grouped public reply")
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(publicReplyID), map[string]any{
		"assignee_user_id": devID,
		"comment": map[string]any{
			"body":       "Visible staff reply",
			"visibility": "public",
		},
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	notification = requireNotificationForTicketEmail(t, tracker, publicReplyID, "customer@example.test")
	if notification.Event != "ticket.commented" ||
		!strings.Contains(notification.BodyText, "Visible staff reply") ||
		strings.Contains(notification.BodyText, "Status: Closed") {
		t.Fatalf("public reply should be the requester-facing event: %#v", notification)
	}

	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/users/"+itoa(customerID), map[string]any{
		"disabled": true,
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	resp, body = doJSON(t, client, http.MethodPatch, server.URL+"/api/tickets/"+itoa(publicReplyID), map[string]any{
		"status": "closed",
	}, adminCookie, adminCSRF, server.URL)
	requireStatus(t, resp, body, http.StatusOK)
	for _, notification := range mustEmailNotifications(t, tracker, 100) {
		if notification.TicketID == publicReplyID && notification.UserID == customerID {
			t.Fatalf("disabled requester notification = %#v", notification)
		}
	}
}
