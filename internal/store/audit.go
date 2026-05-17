package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) RecordAuditEvent(input CreateAuditEvent) (AuditEvent, error) {
	action := strings.TrimSpace(input.Action)
	targetType := strings.TrimSpace(input.TargetType)
	if action == "" || targetType == "" {
		return AuditEvent{}, fmt.Errorf("%w: audit action and target type are required", ErrValidation)
	}
	event := AuditEvent{
		ActorUserID:   input.ActorUserID,
		ActorUsername: strings.TrimSpace(input.ActorUsername),
		Action:        action,
		TargetType:    targetType,
		TargetID:      input.TargetID,
		TargetName:    strings.TrimSpace(input.TargetName),
		IP:            strings.TrimSpace(input.IP),
		DetailsJSON:   strings.TrimSpace(input.DetailsJSON),
		CreatedAt:     time.Now().UTC(),
	}
	if event.ActorUsername == "" {
		event.ActorUsername = "system"
	}
	result, err := s.db.Exec(`
		INSERT INTO audit_events (actor_user_id, actor_username, action, target_type, target_id, target_name, ip, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullZero(event.ActorUserID), event.ActorUsername, event.Action, event.TargetType, nullZero(event.TargetID),
		event.TargetName, event.IP, event.DetailsJSON, formatTime(event.CreatedAt),
	)
	if err != nil {
		return AuditEvent{}, normalizeSQLError(err)
	}
	event.ID, _ = result.LastInsertId()
	return event, nil
}

func (s *Store) ListAuditEvents(limit int) []AuditEvent {
	return s.ListAuditEventsPage(AuditEventFilter{Limit: limit}).Events
}

func (s *Store) ListAuditEventsPage(filter AuditEventFilter) AuditEventPage {
	limit, offset := normalizePage(filter.Limit, filter.Offset, 25, 100)
	where, args := auditEventWhere(filter)
	page := AuditEventPage{Limit: limit, Offset: offset}
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events `+where, args...).Scan(&page.Total)

	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, limit, offset)
	rows, err := s.db.Query(`
		SELECT id, actor_user_id, actor_username, action, target_type, target_id, target_name, ip, details_json, created_at
		FROM audit_events
		`+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return page
	}
	defer rows.Close()

	page.Events = make([]AuditEvent, 0)
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err == nil {
			page.Events = append(page.Events, event)
		}
	}
	return page
}

func auditEventWhere(filter AuditEventFilter) (string, []any) {
	query := strings.TrimSpace(filter.Query)
	if query == "" {
		return "", nil
	}
	like := "%" + query + "%"
	return `WHERE (actor_username LIKE ? OR action LIKE ? OR target_type LIKE ? OR target_name LIKE ? OR ip LIKE ? OR details_json LIKE ?)`,
		[]any{like, like, like, like, like, like}
}

func scanAuditEvent(rows scanner) (AuditEvent, error) {
	var event AuditEvent
	var actorID sql.NullInt64
	var targetID sql.NullInt64
	var created string
	if err := rows.Scan(
		&event.ID, &actorID, &event.ActorUsername, &event.Action, &event.TargetType, &targetID,
		&event.TargetName, &event.IP, &event.DetailsJSON, &created,
	); err != nil {
		return AuditEvent{}, err
	}
	if actorID.Valid {
		event.ActorUserID = actorID.Int64
	}
	if targetID.Valid {
		event.TargetID = targetID.Int64
	}
	event.CreatedAt = parseTime(created)
	return event, nil
}
