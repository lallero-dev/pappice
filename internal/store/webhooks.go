package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"pappice/internal/security"
)

func (s *Store) CreateWebhook(input CreateWebhook) (Webhook, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Webhook{}, err
	}
	defer tx.Rollback()
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	events, err := normalizeEvents(input.Events)
	if err != nil {
		return Webhook{}, err
	}
	if input.ProductID != nil {
		if _, err := getProductTx(tx, *input.ProductID); err != nil {
			return Webhook{}, err
		}
	}
	now := time.Now().UTC()
	hook := Webhook{
		ProductID: input.ProductID,
		Name:      strings.TrimSpace(input.Name),
		URL:       strings.TrimSpace(input.URL),
		Secret:    strings.TrimSpace(input.Secret),
		Events:    events,
		Enabled:   enabled,
		CreatedAt: now,
	}
	hook.UpdatedAt = hook.CreatedAt
	if hook.Name == "" {
		return Webhook{}, fmt.Errorf("%w: webhook name is required", ErrValidation)
	}
	if err := validateWebhookURL(hook.URL); err != nil {
		return Webhook{}, err
	}
	if hook.Secret == "" {
		secret, err := security.RandomToken()
		if err != nil {
			return Webhook{}, err
		}
		hook.Secret = secret
	}
	eventsJSON, _ := json.Marshal(hook.Events)
	result, err := tx.Exec(`
		INSERT INTO webhooks (product_id, name, url, secret, events_json, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableInt64(hook.ProductID), hook.Name, hook.URL, hook.Secret, string(eventsJSON), boolInt(hook.Enabled),
		formatTime(hook.CreatedAt), formatTime(hook.UpdatedAt),
	)
	if err != nil {
		return Webhook{}, err
	}
	hook.ID, _ = result.LastInsertId()
	if err := insertAppEventTx(tx, now, input.Event, "webhook.created", "webhook", hook.ID, hook.Name, webhookEventDetails(hook), nil); err != nil {
		return Webhook{}, err
	}
	if err := tx.Commit(); err != nil {
		return Webhook{}, err
	}
	return copyWebhook(hook), nil
}

func (s *Store) ListWebhooks(productID *int64) []Webhook {
	query := `
		SELECT id, product_id, name, url, secret, events_json, enabled, created_at, updated_at, last_status, last_error, last_delivered_at
		FROM webhooks`
	args := []any{}
	if productID == nil {
		query += ` WHERE product_id IS NULL`
	} else {
		query += ` WHERE product_id = ?`
		args = append(args, *productID)
	}
	query += ` ORDER BY id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanWebhooks(rows)
}

func (s *Store) GetWebhook(id int64) (Webhook, error) {
	return getWebhookWithQuery(s.db, id)
}

type rowQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func getWebhookWithQuery(queryer rowQueryer, id int64) (Webhook, error) {
	row := queryer.QueryRow(webhookSelectSQL+` WHERE id = ?`, id)
	hook, err := scanWebhook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Webhook{}, ErrNotFound
	}
	return hook, err
}

func getWebhookTx(tx *sql.Tx, id int64) (Webhook, error) {
	return getWebhookWithQuery(tx, id)
}

func (s *Store) ListWebhooksForEvent(event string, productID int64) []Webhook {
	rows, err := s.db.Query(`
		SELECT id, product_id, name, url, secret, events_json, enabled, created_at, updated_at, last_status, last_error, last_delivered_at
		FROM webhooks
		WHERE enabled = 1 AND (product_id IS NULL OR product_id = ?)
		ORDER BY product_id IS NOT NULL DESC, id`, productID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	scannedRows := scanWebhooks(rows)
	hooks := make([]Webhook, 0, len(scannedRows))
	for _, hook := range scannedRows {
		if eventMatches(hook.Events, event) {
			hooks = append(hooks, hook)
		}
	}
	return hooks
}

func (s *Store) UpdateWebhook(id int64, patch UpdateWebhook) (Webhook, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Webhook{}, err
	}
	defer tx.Rollback()
	hook, err := getWebhookTx(tx, id)
	if err != nil {
		return Webhook{}, err
	}
	if patch.Name != nil {
		hook.Name = strings.TrimSpace(*patch.Name)
		if hook.Name == "" {
			return Webhook{}, fmt.Errorf("%w: webhook name is required", ErrValidation)
		}
	}
	if patch.URL != nil {
		hook.URL = strings.TrimSpace(*patch.URL)
		if err := validateWebhookURL(hook.URL); err != nil {
			return Webhook{}, err
		}
	}
	if patch.Secret != nil {
		hook.Secret = strings.TrimSpace(*patch.Secret)
	}
	if patch.Events != nil {
		events, err := normalizeEvents(*patch.Events)
		if err != nil {
			return Webhook{}, err
		}
		hook.Events = events
	}
	if patch.Enabled != nil {
		hook.Enabled = *patch.Enabled
	}
	hook.UpdatedAt = time.Now().UTC()
	eventsJSON, _ := json.Marshal(hook.Events)
	_, err = tx.Exec(`
		UPDATE webhooks
		SET name = ?, url = ?, secret = ?, events_json = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		hook.Name, hook.URL, hook.Secret, string(eventsJSON), boolInt(hook.Enabled), formatTime(hook.UpdatedAt), hook.ID,
	)
	if err != nil {
		return Webhook{}, err
	}
	if err := insertAppEventTx(tx, hook.UpdatedAt, patch.Event, "webhook.updated", "webhook", hook.ID, hook.Name, webhookEventDetails(hook), nil); err != nil {
		return Webhook{}, err
	}
	if err := tx.Commit(); err != nil {
		return Webhook{}, err
	}
	return s.GetWebhook(id)
}

func (s *Store) RotateWebhookSecret(id int64, event ...EventContext) (Webhook, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Webhook{}, "", err
	}
	defer tx.Rollback()
	hook, err := getWebhookTx(tx, id)
	if err != nil {
		return Webhook{}, "", err
	}
	secret, err := security.RandomToken()
	if err != nil {
		return Webhook{}, "", err
	}
	now := time.Now().UTC()
	result, err := tx.Exec(`UPDATE webhooks SET secret = ?, updated_at = ? WHERE id = ?`, secret, formatTime(now), id)
	if err != nil {
		return Webhook{}, "", err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Webhook{}, "", ErrNotFound
	}
	hook.Secret = secret
	hook.UpdatedAt = now
	if err := insertAppEventTx(tx, now, firstEventContext(event), "webhook.secret_rotated", "webhook", hook.ID, hook.Name, nil, nil); err != nil {
		return Webhook{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Webhook{}, "", err
	}
	hook, err = s.GetWebhook(id)
	if err != nil {
		return Webhook{}, "", err
	}
	return hook, secret, nil
}

func (s *Store) DeleteWebhook(id int64, event ...EventContext) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	hook, err := getWebhookTx(tx, id)
	if err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM webhooks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	if err := insertAppEventTx(tx, time.Now().UTC(), firstEventContext(event), "webhook.deleted", "webhook", hook.ID, hook.Name, webhookEventDetails(hook), nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordDelivery(delivery WebhookDelivery) error {
	now := time.Now().UTC()
	result, err := s.db.Exec(`
		INSERT INTO webhook_deliveries (webhook_id, product_id, event, ticket_id, status_code, error, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		delivery.WebhookID, nullableInt64(delivery.ProductID), delivery.Event, nullZero(delivery.TicketID),
		nullZero(int64(delivery.StatusCode)), delivery.Error, delivery.DurationMS, formatTime(now),
	)
	if err != nil {
		return err
	}
	delivery.ID, _ = result.LastInsertId()
	delivery.CreatedAt = now
	_, _ = s.db.Exec(`
		UPDATE webhooks
		SET last_status = ?, last_error = ?, last_delivered_at = ?
		WHERE id = ?`,
		delivery.StatusCode, delivery.Error, formatTime(now), delivery.WebhookID,
	)
	_, _ = s.db.Exec(`
		DELETE FROM webhook_deliveries
		WHERE id NOT IN (SELECT id FROM webhook_deliveries ORDER BY created_at DESC LIMIT 200)`)
	return nil
}

func (s *Store) EnqueueWebhookNotifications(inputs []CreateWebhookNotification) ([]WebhookNotification, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	created, err := enqueueWebhookNotificationsTx(tx, inputs, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

func enqueueWebhookNotificationsTx(tx *sql.Tx, inputs []CreateWebhookNotification, now time.Time) ([]WebhookNotification, error) {
	created := make([]WebhookNotification, 0, len(inputs))
	for _, input := range inputs {
		event := strings.TrimSpace(input.Event)
		if event == "" || event == "*" || !isValid(validEvents, event) {
			return nil, fmt.Errorf("%w: invalid webhook notification event %q", ErrValidation, event)
		}
		payload := strings.TrimSpace(input.PayloadJSON)
		if payload == "" {
			payload = "{}"
		}
		nextAttemptAt := now
		if !input.SendAfter.IsZero() {
			nextAttemptAt = input.SendAfter.UTC()
		}
		notification := WebhookNotification{
			WebhookID:     input.WebhookID,
			ProductID:     input.ProductID,
			TicketID:      input.TicketID,
			Event:         event,
			PayloadJSON:   payload,
			Status:        "pending",
			NextAttemptAt: nextAttemptAt,
			CreatedAt:     now,
		}
		if input.Coalesce {
			existingID, ok, err := pendingWebhookNotificationIDTx(tx, notification)
			if err != nil {
				return nil, err
			}
			if ok {
				_, err := tx.Exec(`
					UPDATE webhook_notifications
					SET product_id = ?, event = ?, payload_json = ?, status = 'pending',
					    attempts = 0, next_attempt_at = ?, locked_until = NULL, last_error = ''
					WHERE id = ?`,
					nullableInt64(notification.ProductID), notification.Event, notification.PayloadJSON,
					formatTime(notification.NextAttemptAt), existingID,
				)
				if err != nil {
					return nil, normalizeSQLError(err)
				}
				updated, err := getWebhookNotificationTx(tx, existingID)
				if err != nil {
					return nil, err
				}
				created = append(created, updated)
				continue
			}
		}
		result, err := tx.Exec(`
			INSERT INTO webhook_notifications (
				webhook_id, product_id, ticket_id, event, payload_json, status, attempts, next_attempt_at, created_at
			)
			VALUES (?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
			notification.WebhookID, nullableInt64(notification.ProductID), nullZero(notification.TicketID), notification.Event, notification.PayloadJSON,
			formatTime(nextAttemptAt), formatTime(now),
		)
		if err != nil {
			return nil, normalizeSQLError(err)
		}
		id, _ := result.LastInsertId()
		notification.ID = id
		created = append(created, notification)
	}
	return created, nil
}

func pendingWebhookNotificationIDTx(tx *sql.Tx, notification WebhookNotification) (int64, bool, error) {
	var row *sql.Row
	if notification.TicketID > 0 {
		row = tx.QueryRow(`
			SELECT id
			FROM webhook_notifications
			WHERE status = 'pending'
			  AND webhook_id = ?
			  AND ticket_id = ?
			ORDER BY created_at DESC
			LIMIT 1`, notification.WebhookID, notification.TicketID)
	} else {
		row = tx.QueryRow(`
			SELECT id
			FROM webhook_notifications
			WHERE status = 'pending'
			  AND webhook_id = ?
			  AND ticket_id IS NULL
			  AND event = ?
			ORDER BY created_at DESC
			LIMIT 1`, notification.WebhookID, notification.Event)
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

func (s *Store) ClaimWebhookNotifications(limit int, leaseFor time.Duration) ([]WebhookNotification, error) {
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
		FROM webhook_notifications
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

	claimed := make([]WebhookNotification, 0, len(ids))
	for _, id := range ids {
		result, err := tx.Exec(`
			UPDATE webhook_notifications
			SET status = 'sending', attempts = attempts + 1, locked_until = ?
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
		notification, err := getWebhookNotificationTx(tx, id)
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

func (s *Store) MarkWebhookNotificationSent(id int64) error {
	now := time.Now().UTC()
	result, err := s.db.Exec(`
		UPDATE webhook_notifications
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

func (s *Store) MarkWebhookNotificationFailed(id int64, sendErr error, maxAttempts int) error {
	if maxAttempts < 1 {
		maxAttempts = 5
	}
	notification, err := s.GetWebhookNotification(id)
	if err != nil {
		return err
	}
	status := "pending"
	nextAttempt := time.Now().UTC().Add(emailRetryDelay(notification.Attempts))
	if notification.Attempts >= maxAttempts {
		status = "failed"
		nextAttempt = time.Now().UTC()
	}
	message := "webhook delivery failed"
	if sendErr != nil {
		message = sendErr.Error()
	}
	message = truncateString(message, 1000)
	result, err := s.db.Exec(`
		UPDATE webhook_notifications
		SET status = ?, next_attempt_at = ?, locked_until = NULL, last_error = ?
		WHERE id = ?`,
		status, formatTime(nextAttempt), message, id,
	)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetWebhookNotification(id int64) (WebhookNotification, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return WebhookNotification{}, err
	}
	defer tx.Rollback()
	notification, err := getWebhookNotificationTx(tx, id)
	if err != nil {
		return WebhookNotification{}, err
	}
	if err := tx.Commit(); err != nil {
		return WebhookNotification{}, err
	}
	return notification, nil
}

func (s *Store) ListDeliveries(limit int) []WebhookDelivery {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, webhook_id, product_id, event, ticket_id, status_code, error, duration_ms, created_at
		FROM webhook_deliveries
		ORDER BY created_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var deliveries []WebhookDelivery
	for rows.Next() {
		var delivery WebhookDelivery
		var productID, ticketID, status sql.NullInt64
		var created string
		if err := rows.Scan(&delivery.ID, &delivery.WebhookID, &productID, &delivery.Event, &ticketID, &status, &delivery.Error, &delivery.DurationMS, &created); err == nil {
			if productID.Valid {
				v := productID.Int64
				delivery.ProductID = &v
			}
			if ticketID.Valid {
				delivery.TicketID = ticketID.Int64
			}
			if status.Valid {
				delivery.StatusCode = int(status.Int64)
			}
			delivery.CreatedAt = parseTime(created)
			deliveries = append(deliveries, delivery)
		}
	}
	return deliveries
}

func getWebhookNotificationTx(tx *sql.Tx, id int64) (WebhookNotification, error) {
	row := tx.QueryRow(`
		SELECT id, webhook_id, product_id, ticket_id, event, payload_json, status, attempts, next_attempt_at, locked_until, last_error, created_at, sent_at
		FROM webhook_notifications
		WHERE id = ?`, id)
	notification, err := scanWebhookNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookNotification{}, ErrNotFound
	}
	return notification, err
}

func scanWebhookNotification(rows scanner) (WebhookNotification, error) {
	var notification WebhookNotification
	var productID, ticketID sql.NullInt64
	var nextAttemptAt, createdAt string
	var lockedUntil, sentAt sql.NullString
	if err := rows.Scan(
		&notification.ID, &notification.WebhookID, &productID, &ticketID, &notification.Event, &notification.PayloadJSON,
		&notification.Status, &notification.Attempts, &nextAttemptAt, &lockedUntil, &notification.LastError, &createdAt, &sentAt,
	); err != nil {
		return WebhookNotification{}, err
	}
	if productID.Valid {
		v := productID.Int64
		notification.ProductID = &v
	}
	if ticketID.Valid {
		notification.TicketID = ticketID.Int64
	}
	notification.NextAttemptAt = parseTime(nextAttemptAt)
	notification.LockedUntil = parseNullTime(lockedUntil)
	notification.CreatedAt = parseTime(createdAt)
	notification.SentAt = parseNullTime(sentAt)
	return notification, nil
}

func scanWebhooks(rows *sql.Rows) []Webhook {
	var hooks []Webhook
	for rows.Next() {
		hook, err := scanWebhook(rows)
		if err == nil {
			hooks = append(hooks, hook)
		}
	}
	return hooks
}

const webhookSelectSQL = `
	SELECT id, product_id, name, url, secret, events_json, enabled, created_at, updated_at, last_status, last_error, last_delivered_at
	FROM webhooks`

func scanWebhook(rows scanner) (Webhook, error) {
	var hook Webhook
	var productID sql.NullInt64
	var enabled int
	var eventsJSON string
	var created, updated string
	var lastError, lastDelivered sql.NullString
	if err := rows.Scan(
		&hook.ID, &productID, &hook.Name, &hook.URL, &hook.Secret, &eventsJSON, &enabled,
		&created, &updated, &hook.LastStatus, &lastError, &lastDelivered,
	); err != nil {
		return Webhook{}, err
	}
	if productID.Valid {
		v := productID.Int64
		hook.ProductID = &v
	}
	_ = json.Unmarshal([]byte(eventsJSON), &hook.Events)
	hook.Enabled = enabled != 0
	hook.CreatedAt = parseTime(created)
	hook.UpdatedAt = parseTime(updated)
	if lastError.Valid {
		hook.LastError = lastError.String
	}
	hook.LastDeliveredAt = parseNullTime(lastDelivered)
	return hook, nil
}

func validateWebhookURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: invalid webhook URL", ErrValidation)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: webhook URL must be http or https", ErrValidation)
	}
	return nil
}

func copyWebhook(hook Webhook) Webhook {
	hook.Events = append([]string(nil), hook.Events...)
	return hook
}

func webhookEventDetails(hook Webhook) map[string]any {
	details := map[string]any{"enabled": hook.Enabled}
	if hook.ProductID == nil {
		details["scope"] = "global"
		return details
	}
	details["product_id"] = *hook.ProductID
	return details
}
