package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func insertAuditEventTx(tx *sql.Tx, input CreateAuditEvent, now time.Time) (AuditEvent, error) {
	action := strings.TrimSpace(input.Action)
	targetType := strings.TrimSpace(input.TargetType)
	if action == "" || targetType == "" {
		return AuditEvent{}, fmt.Errorf("%w: audit action and target type are required", ErrValidation)
	}
	event := AuditEvent{
		DomainEventID: input.DomainEventID,
		ActorUserID:   input.ActorUserID,
		ActorEmail:    strings.TrimSpace(input.ActorEmail),
		Action:        action,
		TargetType:    targetType,
		TargetID:      input.TargetID,
		TargetName:    strings.TrimSpace(input.TargetName),
		IP:            strings.TrimSpace(input.IP),
		DetailsJSON:   strings.TrimSpace(input.DetailsJSON),
		CreatedAt:     now.UTC(),
	}
	if event.ActorEmail == "" {
		event.ActorEmail = "system"
	}
	result, err := tx.Exec(`
		INSERT INTO audit_events (domain_event_id, actor_user_id, actor_email, action, target_type, target_id, target_name, ip, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.DomainEventID, nullZero(event.ActorUserID), event.ActorEmail, event.Action, event.TargetType, nullZero(event.TargetID),
		event.TargetName, event.IP, event.DetailsJSON, formatTime(event.CreatedAt),
	)
	if err != nil {
		return AuditEvent{}, normalizeSQLError(err)
	}
	event.ID, err = insertedID(result)
	if err != nil {
		return AuditEvent{}, err
	}
	return event, nil
}

func (s *Store) ListAuditEventsPage(filter AuditEventFilter) (AuditEventPage, error) {
	limit, offset := normalizePage(filter.Limit, filter.Offset, 25, 100)
	where, args := auditEventWhere(filter)
	page := AuditEventPage{Limit: limit, Offset: offset}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_events `+where, args...).Scan(&page.Total); err != nil {
		return AuditEventPage{}, err
	}

	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, limit, offset)
	rows, err := s.db.Query(`
		SELECT id, domain_event_id, actor_user_id, actor_email, action, target_type, target_id, target_name, ip, details_json, created_at
		FROM audit_events
		`+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return AuditEventPage{}, err
	}
	defer rows.Close()

	page.Events = make([]AuditEvent, 0, limit)
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return AuditEventPage{}, err
		}
		page.Events = append(page.Events, event)
	}
	return page, rows.Err()
}

func auditEventWhere(filter AuditEventFilter) (string, []any) {
	query := strings.TrimSpace(filter.Query)
	if query == "" {
		return "", nil
	}
	like := "%" + query + "%"
	return `WHERE (actor_email LIKE ? OR action LIKE ? OR target_type LIKE ? OR target_name LIKE ? OR ip LIKE ? OR details_json LIKE ?)`,
		[]any{like, like, like, like, like, like}
}

func scanAuditEvent(rows scanner) (AuditEvent, error) {
	var event AuditEvent
	var domainEventID sql.NullInt64
	var actorID sql.NullInt64
	var targetID sql.NullInt64
	var created dbTime
	if err := rows.Scan(
		&event.ID, &domainEventID, &actorID, &event.ActorEmail, &event.Action, &event.TargetType, &targetID,
		&event.TargetName, &event.IP, &event.DetailsJSON, &created,
	); err != nil {
		return AuditEvent{}, err
	}
	if domainEventID.Valid {
		event.DomainEventID = domainEventID.Int64
	}
	if actorID.Valid {
		event.ActorUserID = actorID.Int64
	}
	if targetID.Valid {
		event.TargetID = targetID.Int64
	}
	event.CreatedAt = created.Time
	return event, nil
}
