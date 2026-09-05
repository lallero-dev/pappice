package server

import (
	"net/http"
	"strings"

	"pappice/internal/store"
)

func (s *Server) handleEmailNotifications(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	limit, offset := paginationParams(r, 25, 100)
	page, err := s.store.ListEmailNotificationsPage(store.EmailNotificationFilter{
		Status: r.URL.Query().Get("status"),
		Query:  r.URL.Query().Get("q"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	stats, err := s.store.EmailNotificationStats()
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"notifications":              page.Notifications,
		"total":                      page.Total,
		"limit":                      page.Limit,
		"offset":                     page.Offset,
		"enabled":                    s.options.EmailNotifications,
		"notification_delay_seconds": int(s.options.NotificationDelay.Seconds()),
		"stats":                      stats,
	})
}

func (s *Server) handleEmailNotificationByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	rest := trimRoutePrefix(r.URL.Path, "/api/email-notifications/")
	if rest == "test" {
		s.handleEmailNotificationTest(w, r, auth)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "retry" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	id, ok := parsePositiveID(w, parts[0], "invalid email notification id")
	if !ok {
		return
	}
	notification, err := s.store.RetryEmailNotification(id, s.eventContext(r, auth.User))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, map[string]any{"notification": notification})
}

func (s *Server) handleEmailNotificationTest(w http.ResponseWriter, r *http.Request, auth authContext) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.options.EmailNotifications {
		respondError(w, http.StatusConflict, "email notifications are not configured")
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	recipientEmail := strings.TrimSpace(input.Email)
	if recipientEmail == "" {
		recipientEmail = strings.TrimSpace(auth.User.Email)
	}
	if recipientEmail == "" {
		respondError(w, http.StatusBadRequest, "test recipient email is required")
		return
	}
	recipientName := defaultString(auth.User.DisplayName, auth.User.Email)
	subject := "Pappice test email"
	bodyText := "This is a no-reply test email from Pappice.\n\nIf you received this message, SMTP delivery is working."
	bodyHTML := "<!doctype html><meta charset=\"utf-8\"><p>This is a no-reply test email from Pappice.</p><p>If you received this message, SMTP delivery is working.</p>"
	created, err := s.store.EnqueueEmailNotificationsWithEvent([]store.CreateEmailNotification{{
		RecipientEmail: recipientEmail,
		RecipientName:  recipientName,
		Event:          "email.test",
		Subject:        subject,
		BodyText:       bodyText,
		BodyHTML:       bodyHTML,
	}}, s.eventContext(r, auth.User), "email_notification.test_queued", "email_notification", map[string]any{"recipient": recipientEmail})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusCreated, map[string]any{"notification": created[0]})
}
