package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) TicketEmailRecipients(event string, ticket Ticket, actor User) []EmailRecipient {
	recipients := make(map[int64]EmailRecipient)
	add := func(recipient EmailRecipient) {
		if recipient.UserID == 0 || recipient.UserID == actor.ID || strings.TrimSpace(recipient.Email) == "" {
			return
		}
		if recipient.Role == "customer" {
			return
		}
		recipients[recipient.UserID] = recipient
	}

	switch event {
	case "ticket.created":
		for _, recipient := range s.productManagerEmailRecipients(ticket.ProductID) {
			add(recipient)
		}
	case "ticket.updated", "ticket.commented":
		if recipient, ok := s.emailRecipientByIdentity(ticket.Reporter); ok {
			add(recipient)
		}
		if recipient, ok := s.emailRecipientByIdentity(ticket.Assignee); ok {
			add(recipient)
		}
	case "ticket.assigned":
		if recipient, ok := s.emailRecipientByIdentity(ticket.Assignee); ok {
			add(recipient)
		}
	}

	result := make([]EmailRecipient, 0, len(recipients))
	for _, recipient := range recipients {
		result = append(result, recipient)
	}
	return result
}

func (s *Store) EnqueueEmailNotifications(inputs []CreateEmailNotification) ([]EmailNotification, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	created, err := enqueueEmailNotificationsTx(tx, inputs, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Store) EnqueueEmailNotificationsWithEvent(inputs []CreateEmailNotification, event EventContext, eventType, targetType string, details map[string]any) ([]EmailNotification, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	created, err := enqueueEmailNotificationsTx(tx, inputs, now)
	if err != nil {
		return nil, err
	}
	if len(created) > 0 {
		target := created[0]
		if err := insertAppEventTx(tx, now, event, eventType, targetType, target.ID, target.Subject, details, nil); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

func enqueueEmailNotificationsTx(tx *sql.Tx, inputs []CreateEmailNotification, now time.Time) ([]EmailNotification, error) {
	created := make([]EmailNotification, 0, len(inputs))
	for _, input := range inputs {
		email, err := normalizeEmail(input.RecipientEmail)
		if err != nil {
			return nil, err
		}
		subject := strings.TrimSpace(input.Subject)
		bodyText := strings.TrimSpace(input.BodyText)
		if email == "" || subject == "" || bodyText == "" {
			return nil, fmt.Errorf("%w: email, subject, and body are required", ErrValidation)
		}
		notification := EmailNotification{
			ProductID:      input.ProductID,
			TicketID:       input.TicketID,
			UserID:         input.UserID,
			RecipientEmail: email,
			RecipientName:  strings.TrimSpace(input.RecipientName),
			Event:          strings.TrimSpace(input.Event),
			Subject:        subject,
			BodyText:       bodyText,
			BodyHTML:       strings.TrimSpace(input.BodyHTML),
			Status:         "pending",
			NextAttemptAt:  now,
			CreatedAt:      now,
		}
		if notification.RecipientName == "" {
			notification.RecipientName = email
		}
		if notification.Event == "" {
			return nil, fmt.Errorf("%w: event is required", ErrValidation)
		}
		if !isValid(validEmailEvents, notification.Event) {
			return nil, fmt.Errorf("%w: invalid notification event %q", ErrValidation, notification.Event)
		}
		if !input.SendAfter.IsZero() {
			notification.NextAttemptAt = input.SendAfter.UTC()
		}
		if input.Coalesce {
			existingID, ok, err := pendingEmailNotificationIDTx(tx, notification)
			if err != nil {
				return nil, err
			}
			if ok {
				_, err := tx.Exec(`
					UPDATE email_notifications
					SET event = ?, subject = ?, body_text = ?, body_html = ?, status = 'pending',
					    attempts = 0, next_attempt_at = ?, locked_until = NULL, last_error = ''
					WHERE id = ?`,
					notification.Event, notification.Subject, notification.BodyText, notification.BodyHTML,
					formatTime(notification.NextAttemptAt), existingID,
				)
				if err != nil {
					return nil, normalizeSQLError(err)
				}
				updated, err := getEmailNotificationTx(tx, existingID)
				if err != nil {
					return nil, err
				}
				created = append(created, updated)
				continue
			}
		}
		result, err := tx.Exec(`
			INSERT INTO email_notifications (
				product_id, ticket_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
				status, attempts, next_attempt_at, created_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
			nullZero(notification.ProductID), nullZero(notification.TicketID), nullZero(notification.UserID), notification.RecipientEmail,
			notification.RecipientName, notification.Event, notification.Subject, notification.BodyText, notification.BodyHTML,
			formatTime(notification.NextAttemptAt), formatTime(notification.CreatedAt),
		)
		if err != nil {
			return nil, normalizeSQLError(err)
		}
		notification.ID, _ = result.LastInsertId()
		created = append(created, notification)
	}
	return created, nil
}

func (s *Store) ClaimEmailNotifications(limit int, leaseFor time.Duration) ([]EmailNotification, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	if leaseFor <= 0 {
		leaseFor = time.Minute
	}
	now := time.Now().UTC()
	nowText := formatTime(now)
	lockedUntil := formatTime(now.Add(leaseFor))

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT id
		FROM email_notifications
		WHERE (status = 'pending' AND next_attempt_at <= ?)
		   OR (status = 'sending' AND locked_until IS NOT NULL AND locked_until <= ?)
		ORDER BY created_at
		LIMIT ?`, nowText, nowText, limit)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	claimed := make([]EmailNotification, 0, len(ids))
	for _, id := range ids {
		result, err := tx.Exec(`
			UPDATE email_notifications
			SET status = 'sending', locked_until = ?
			WHERE id = ?
			  AND ((status = 'pending' AND next_attempt_at <= ?)
			    OR (status = 'sending' AND locked_until IS NOT NULL AND locked_until <= ?))`,
			lockedUntil, id, nowText, nowText,
		)
		if err != nil {
			return nil, err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			continue
		}
		notification, err := getEmailNotificationTx(tx, id)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, notification)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) MarkEmailSent(id int64) error {
	now := time.Now().UTC()
	result, err := s.db.Exec(`
		UPDATE email_notifications
		SET status = 'sent', sent_at = ?, locked_until = NULL, last_error = ''
		WHERE id = ?`,
		formatTime(now), id,
	)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkEmailFailed(id int64, sendErr error, maxAttempts int) error {
	if maxAttempts < 1 {
		maxAttempts = 5
	}
	notification, err := s.GetEmailNotification(id)
	if err != nil {
		return err
	}
	attempts := notification.Attempts + 1
	status := "pending"
	delay := emailRetryDelay(attempts)
	nextAttempt := time.Now().UTC().Add(delay)
	if attempts >= maxAttempts {
		status = "failed"
		nextAttempt = time.Now().UTC()
	}
	message := "send failed"
	if sendErr != nil {
		message = sendErr.Error()
	}
	message = truncateString(message, 1000)
	result, err := s.db.Exec(`
		UPDATE email_notifications
		SET status = ?, attempts = ?, next_attempt_at = ?, locked_until = NULL, last_error = ?
		WHERE id = ?`,
		status, attempts, formatTime(nextAttempt), message, id,
	)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetEmailNotification(id int64) (EmailNotification, error) {
	row := s.db.QueryRow(`
		SELECT id, product_id, ticket_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
		       status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		FROM email_notifications
		WHERE id = ?`, id)
	notification, err := scanEmailNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailNotification{}, ErrNotFound
	}
	return notification, err
}

func (s *Store) ListEmailNotifications(limit int) []EmailNotification {
	return s.ListEmailNotificationsPage(EmailNotificationFilter{Limit: limit}).Notifications
}

func (s *Store) ListEmailNotificationsPage(filter EmailNotificationFilter) EmailNotificationPage {
	limit, offset := normalizePage(filter.Limit, filter.Offset, 25, 100)
	where, args := emailNotificationWhere(filter)
	page := EmailNotificationPage{Limit: limit, Offset: offset}
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM email_notifications `+where, args...).Scan(&page.Total)

	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, limit, offset)
	rows, err := s.db.Query(`
		SELECT id, product_id, ticket_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
		       status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		FROM email_notifications
		`+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return page
	}
	defer rows.Close()

	page.Notifications = make([]EmailNotification, 0)
	for rows.Next() {
		notification, err := scanEmailNotification(rows)
		if err == nil {
			page.Notifications = append(page.Notifications, notification)
		}
	}
	return page
}

func emailNotificationWhere(filter EmailNotificationFilter) (string, []any) {
	clauses := make([]string, 0, 2)
	args := make([]any, 0)
	status := strings.ToLower(strings.TrimSpace(filter.Status))
	if status != "" && isValid(validEmailNotificationStatuses, status) {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	query := strings.TrimSpace(filter.Query)
	if query != "" {
		like := "%" + query + "%"
		clauses = append(clauses, `(recipient_email LIKE ? OR recipient_name LIKE ? OR event LIKE ? OR subject LIKE ? OR last_error LIKE ?)`)
		args = append(args, like, like, like, like, like)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func (s *Store) EmailNotificationStats() EmailNotificationStats {
	var stats EmailNotificationStats
	rows, err := s.db.Query(`SELECT status, count(*) FROM email_notifications GROUP BY status`)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		stats.Total += count
		switch status {
		case "pending":
			stats.Pending = count
		case "sending":
			stats.Sending = count
		case "sent":
			stats.Sent = count
		case "failed":
			stats.Failed = count
		}
	}
	var sentAt sql.NullString
	var lastError sql.NullString
	_ = s.db.QueryRow(`SELECT sent_at FROM email_notifications WHERE sent_at IS NOT NULL ORDER BY sent_at DESC LIMIT 1`).Scan(&sentAt)
	if sentAt.Valid {
		parsed := parseTime(sentAt.String)
		stats.LastSentAt = &parsed
	}
	_ = s.db.QueryRow(`SELECT last_error FROM email_notifications WHERE last_error <> '' ORDER BY created_at DESC LIMIT 1`).Scan(&lastError)
	if lastError.Valid {
		stats.LastError = lastError.String
	}
	return stats
}

func (s *Store) RetryEmailNotification(id int64, event ...EventContext) (EmailNotification, error) {
	if id < 1 {
		return EmailNotification{}, ErrNotFound
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return EmailNotification{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`
		UPDATE email_notifications
		SET status = 'pending', attempts = 0, next_attempt_at = ?, locked_until = NULL, last_error = ''
		WHERE id = ? AND status = 'failed'`,
		formatTime(now), id,
	)
	if err != nil {
		return EmailNotification{}, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return EmailNotification{}, ErrNotFound
	}
	notification, err := getEmailNotificationTx(tx, id)
	if err != nil {
		return EmailNotification{}, err
	}
	if err := insertAppEventTx(tx, now, firstEventContext(event), "email_notification.retried", "email_notification", notification.ID, notification.Subject, map[string]any{"recipient": notification.RecipientEmail}, nil); err != nil {
		return EmailNotification{}, err
	}
	if err := tx.Commit(); err != nil {
		return EmailNotification{}, err
	}
	return notification, nil
}

func (s *Store) productManagerEmailRecipients(productID int64) []EmailRecipient {
	rows, err := s.db.Query(`
		SELECT DISTINCT u.id, u.display_name, u.email, u.role
		FROM users u
		LEFT JOIN product_members pm ON pm.user_id = u.id AND pm.product_id = ?
		WHERE u.disabled = 0
		  AND u.email IS NOT NULL
		  AND trim(u.email) <> ''
		  AND (u.role = 'admin' OR pm.role = 'manager')
		ORDER BY u.email`, productID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	recipients := make([]EmailRecipient, 0)
	for rows.Next() {
		recipient, err := scanEmailRecipient(rows)
		if err == nil {
			recipients = append(recipients, recipient)
		}
	}
	return recipients
}

func (s *Store) emailRecipientByIdentity(identity string) (EmailRecipient, bool) {
	identity = strings.ToLower(strings.TrimSpace(identity))
	if identity == "" {
		return EmailRecipient{}, false
	}
	row := s.db.QueryRow(`
		SELECT id, display_name, email, role
		FROM users
		WHERE lower(email) = ?
		  AND disabled = 0
		  AND email IS NOT NULL
		  AND trim(email) <> ''`, identity)
	recipient, err := scanEmailRecipient(row)
	return recipient, err == nil
}

func scanEmailRecipient(rows scanner) (EmailRecipient, error) {
	var recipient EmailRecipient
	if err := rows.Scan(&recipient.UserID, &recipient.DisplayName, &recipient.Email, &recipient.Role); err != nil {
		return EmailRecipient{}, err
	}
	return recipient, nil
}

func pendingEmailNotificationIDTx(tx *sql.Tx, notification EmailNotification) (int64, bool, error) {
	var row *sql.Row
	if notification.TicketID > 0 {
		row = tx.QueryRow(`
			SELECT id
			FROM email_notifications
			WHERE status = 'pending'
			  AND ticket_id = ?
			  AND lower(recipient_email) = lower(?)
			ORDER BY created_at DESC
			LIMIT 1`, notification.TicketID, notification.RecipientEmail)
	} else {
		row = tx.QueryRow(`
			SELECT id
			FROM email_notifications
			WHERE status = 'pending'
			  AND ticket_id IS NULL
			  AND lower(recipient_email) = lower(?)
			  AND event = ?
			ORDER BY created_at DESC
			LIMIT 1`, notification.RecipientEmail, notification.Event)
	}
	var id int64
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return id, true, nil
}

func getEmailNotificationTx(tx *sql.Tx, id int64) (EmailNotification, error) {
	row := tx.QueryRow(`
		SELECT id, product_id, ticket_id, user_id, recipient_email, recipient_name, event, subject, body_text, body_html,
		       status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		FROM email_notifications
		WHERE id = ?`, id)
	notification, err := scanEmailNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailNotification{}, ErrNotFound
	}
	return notification, err
}

func scanEmailNotification(rows scanner) (EmailNotification, error) {
	var notification EmailNotification
	var productID, ticketID, userID sql.NullInt64
	var nextAttempt, created string
	var lockedUntil, sentAt sql.NullString
	if err := rows.Scan(
		&notification.ID, &productID, &ticketID, &userID, &notification.RecipientEmail,
		&notification.RecipientName, &notification.Event, &notification.Subject, &notification.BodyText, &notification.BodyHTML,
		&notification.Status, &notification.Attempts, &nextAttempt, &lockedUntil, &notification.LastError, &created, &sentAt,
	); err != nil {
		return EmailNotification{}, err
	}
	if productID.Valid {
		notification.ProductID = productID.Int64
	}
	if ticketID.Valid {
		notification.TicketID = ticketID.Int64
	}
	if userID.Valid {
		notification.UserID = userID.Int64
	}
	notification.NextAttemptAt = parseTime(nextAttempt)
	notification.LockedUntil = parseNullTime(lockedUntil)
	notification.CreatedAt = parseTime(created)
	notification.SentAt = parseNullTime(sentAt)
	return notification, nil
}

func emailRetryDelay(attempts int) time.Duration {
	attempts = min(max(attempts, 1), 6)
	return time.Duration(1<<(attempts-1)) * time.Minute
}
