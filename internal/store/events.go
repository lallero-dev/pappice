package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type EventActor struct {
	UserID      int64  `json:"id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Role        string `json:"role,omitempty"`
}

type EventContext struct {
	Enabled bool
	Actor   EventActor
	IP      string
}

type DomainEvent struct {
	ID               int64      `json:"id"`
	Type             string     `json:"type"`
	ProductID        int64      `json:"product_id,omitempty"`
	TicketID         int64      `json:"ticket_id,omitempty"`
	ActorUserID      int64      `json:"actor_user_id,omitempty"`
	ActorDisplayName string     `json:"actor_display_name,omitempty"`
	ActorEmail       string     `json:"actor_email,omitempty"`
	ActorRole        string     `json:"actor_role,omitempty"`
	PayloadJSON      string     `json:"payload_json"`
	Status           string     `json:"status"`
	Attempts         int        `json:"attempts"`
	LockedUntil      *time.Time `json:"locked_until,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ProcessedAt      *time.Time `json:"processed_at,omitempty"`
}

type CreateDomainEvent struct {
	Type        string
	ProductID   int64
	TicketID    int64
	Actor       EventActor
	PayloadJSON string
}

type TicketEventPayload struct {
	Source            string `json:"source,omitempty"`
	HasPatch          bool   `json:"has_patch,omitempty"`
	PublicComment     bool   `json:"public_comment,omitempty"`
	AssignmentChanged bool   `json:"assignment_changed,omitempty"`
	OnlyAssigneePatch bool   `json:"only_assignee_patch,omitempty"`
	PreviousStatus    string `json:"previous_status,omitempty"`
	CurrentStatus     string `json:"current_status,omitempty"`
	PreviousAssignee  string `json:"previous_assignee,omitempty"`
	CurrentAssignee   string `json:"current_assignee,omitempty"`
	CommentID         int64  `json:"comment_id,omitempty"`
	CommentVisibility string `json:"comment_visibility,omitempty"`
}

type AppEventPayload struct {
	TargetType  string                   `json:"target_type,omitempty"`
	TargetID    int64                    `json:"target_id,omitempty"`
	TargetName  string                   `json:"target_name,omitempty"`
	IP          string                   `json:"ip,omitempty"`
	Details     map[string]any           `json:"details,omitempty"`
	AccountLink *AccountLinkEventPayload `json:"account_link,omitempty"`
}

type AccountLinkEventPayload struct {
	UserID    int64     `json:"user_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func EventActorFromUser(user User) EventActor {
	return EventActor{
		UserID:      user.ID,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Role:        normalizeGlobalRole(user.Role),
	}
}

func (event DomainEvent) Actor() EventActor {
	return EventActor{
		UserID:      event.ActorUserID,
		DisplayName: event.ActorDisplayName,
		Email:       event.ActorEmail,
		Role:        normalizeGlobalRole(event.ActorRole),
	}
}

func accountLinkEventPayload(user User, token string, expiresAt time.Time) *AccountLinkEventPayload {
	return &AccountLinkEventPayload{
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: expiresAt,
	}
}

func (s *Store) CreateDomainEvent(input CreateDomainEvent) (DomainEvent, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return DomainEvent{}, err
	}
	defer tx.Rollback()
	id, err := insertDomainEventTx(tx, input, time.Now().UTC())
	if err != nil {
		return DomainEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return DomainEvent{}, err
	}
	return s.GetDomainEvent(id)
}

func (s *Store) GetDomainEvent(id int64) (DomainEvent, error) {
	row := s.db.QueryRow(domainEventSelectSQL+` WHERE id = ?`, id)
	event, err := scanDomainEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DomainEvent{}, ErrNotFound
	}
	return event, err
}

func (s *Store) ListDomainEvents(limit int) ([]DomainEvent, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(domainEventSelectSQL+` ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDomainEvents(rows)
}

func (s *Store) ClaimDomainEvents(limit int, leaseFor time.Duration) ([]DomainEvent, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	if leaseFor <= 0 {
		leaseFor = time.Minute
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(domainEventSelectSQL+`
		WHERE status IN ('pending', 'failed')
		   OR (status = 'processing' AND locked_until IS NOT NULL AND locked_until <= ?)
		ORDER BY id
		LIMIT ?`, formatTime(now), limit)
	if err != nil {
		return nil, err
	}
	events, err := scanDomainEvents(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}

	lockedUntil := now.Add(leaseFor)
	for i := range events {
		_, err := tx.Exec(`
			UPDATE domain_events
			SET status = 'processing', attempts = attempts + 1, locked_until = ?, updated_at = ?
			WHERE id = ?`,
			formatTime(lockedUntil), formatTime(now), events[i].ID,
		)
		if err != nil {
			return nil, err
		}
		events[i].Status = "processing"
		events[i].Attempts++
		events[i].LockedUntil = &lockedUntil
		events[i].UpdatedAt = now
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) ApplyDomainEventProjection(eventID int64, projection DomainEventProjection) error {
	if eventID < 1 {
		return ErrNotFound
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	status, err := domainEventStatusTx(tx, eventID)
	if err != nil {
		return err
	}
	if status == "processed" {
		return tx.Commit()
	}
	if projection.Audit != nil {
		audit := *projection.Audit
		audit.DomainEventID = eventID
		if _, err := insertAuditEventTx(tx, audit, now); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	if _, err := enqueueEmailNotificationsTx(tx, projection.EmailNotifications, now); err != nil {
		return err
	}
	if _, err := enqueueWebhookNotificationsTx(tx, projection.WebhookNotifications, now); err != nil {
		return err
	}
	if err := markDomainEventProcessedTx(tx, eventID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func domainEventStatusTx(tx *sql.Tx, id int64) (string, error) {
	var status string
	err := tx.QueryRow(`SELECT status FROM domain_events WHERE id = ?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}

func (s *Store) MarkDomainEventProcessed(id int64) error {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := markDomainEventProcessedTx(tx, id, now); err != nil {
		return err
	}
	return tx.Commit()
}

func markDomainEventProcessedTx(tx *sql.Tx, id int64, now time.Time) error {
	result, err := tx.Exec(`
		UPDATE domain_events
		SET status = 'processed', locked_until = NULL, last_error = '', processed_at = ?, updated_at = ?
		WHERE id = ?`,
		formatTime(now.UTC()), formatTime(now.UTC()), id,
	)
	if err != nil {
		return err
	}
	return requireChangedRow(result)
}

func (s *Store) MarkDomainEventFailed(id int64, err error) error {
	message := ""
	if err != nil {
		message = strings.TrimSpace(err.Error())
	}
	now := time.Now().UTC()
	result, updateErr := s.db.Exec(`
		UPDATE domain_events
		SET status = 'failed', locked_until = NULL, last_error = ?, updated_at = ?
		WHERE id = ?`,
		message, formatTime(now), id,
	)
	if updateErr != nil {
		return updateErr
	}
	return requireChangedRow(result)
}

func (s *Store) PruneProcessedDomainEvents(olderThan time.Time, limit int) (int64, error) {
	if olderThan.IsZero() {
		return 0, fmt.Errorf("%w: cutoff time is required", ErrValidation)
	}
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	result, err := s.db.Exec(`
		DELETE FROM domain_events
		WHERE id IN (
			SELECT id
			FROM domain_events
			WHERE status = 'processed'
			  AND processed_at IS NOT NULL
			  AND processed_at < ?
			ORDER BY processed_at
			LIMIT ?
		)`,
		formatTime(olderThan.UTC()), limit,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func insertDomainEventTx(tx *sql.Tx, input CreateDomainEvent, now time.Time) (int64, error) {
	eventType := strings.TrimSpace(input.Type)
	if !isValid(validDomainEvents, eventType) {
		return 0, fmt.Errorf("%w: invalid domain event %q", ErrValidation, eventType)
	}
	payload := strings.TrimSpace(input.PayloadJSON)
	if payload == "" {
		payload = "{}"
	}
	result, err := tx.Exec(`
		INSERT INTO domain_events (
			type, product_id, ticket_id, actor_user_id, actor_display_name, actor_email, actor_role,
			payload_json, status, attempts, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		eventType, input.ProductID, input.TicketID, input.Actor.UserID,
		strings.TrimSpace(input.Actor.DisplayName), strings.TrimSpace(input.Actor.Email), normalizeGlobalRole(input.Actor.Role),
		payload, formatTime(now), formatTime(now),
	)
	if err != nil {
		return 0, normalizeSQLError(err)
	}
	return insertedID(result)
}

func insertAppEventTx(tx *sql.Tx, now time.Time, ctx EventContext, eventType, targetType string, targetID int64, targetName string, details map[string]any, accountLink *AccountLinkEventPayload) error {
	if !ctx.Enabled {
		return nil
	}
	payload := AppEventPayload{
		TargetType:  targetType,
		TargetID:    targetID,
		TargetName:  targetName,
		IP:          strings.TrimSpace(ctx.IP),
		Details:     details,
		AccountLink: accountLink,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = insertDomainEventTx(tx, CreateDomainEvent{
		Type:        eventType,
		Actor:       ctx.Actor,
		PayloadJSON: string(payloadJSON),
	}, now)
	return err
}

const domainEventSelectSQL = `
	SELECT id, type, product_id, ticket_id, actor_user_id, actor_display_name, actor_email, actor_role,
	       payload_json, status, attempts, locked_until, last_error, created_at, updated_at, processed_at
	FROM domain_events`

func scanDomainEvents(rows *sql.Rows) ([]DomainEvent, error) {
	var events []DomainEvent
	for rows.Next() {
		event, err := scanDomainEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func scanDomainEvent(row scanner) (DomainEvent, error) {
	var event DomainEvent
	var lockedUntil, processedAt nullDBTime
	var createdAt, updatedAt dbTime
	err := row.Scan(
		&event.ID, &event.Type, &event.ProductID, &event.TicketID, &event.ActorUserID,
		&event.ActorDisplayName, &event.ActorEmail, &event.ActorRole, &event.PayloadJSON, &event.Status,
		&event.Attempts, &lockedUntil, &event.LastError, &createdAt, &updatedAt, &processedAt,
	)
	if err != nil {
		return DomainEvent{}, err
	}
	event.LockedUntil = lockedUntil.Time
	event.ProcessedAt = processedAt.Time
	event.CreatedAt = createdAt.Time
	event.UpdatedAt = updatedAt.Time
	return event, nil
}
