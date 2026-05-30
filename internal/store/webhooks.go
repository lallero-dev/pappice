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

	hooks := make([]Webhook, 0)
	for _, hook := range scanWebhooks(rows) {
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
