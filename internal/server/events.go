package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"pappice/internal/store"
)

type eventLogger interface {
	Printf(format string, args ...any)
}

func (s *Server) RunEventDispatcher(ctx context.Context, interval time.Duration, logger eventLogger) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := s.dispatchPendingEvents(ctx, 25); err != nil && logger != nil {
		logger.Printf("domain event dispatch: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.dispatchPendingEvents(ctx, 25); err != nil && logger != nil {
				logger.Printf("domain event dispatch: %v", err)
			}
		}
	}
}

func (s *Server) dispatchPendingEvents(ctx context.Context, limit int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	events, err := s.store.ClaimDomainEvents(limit, time.Minute)
	if err != nil {
		return err
	}
	var firstErr error
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			_ = s.store.MarkDomainEventFailed(event.ID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := s.dispatchDomainEvent(ctx, event); err != nil {
			_ = s.store.MarkDomainEventFailed(event.ID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := s.store.MarkDomainEventProcessed(event.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) dispatchDomainEvent(_ context.Context, event store.DomainEvent) error {
	if err := s.recordAuditEvent(event); err != nil {
		return err
	}
	if isTicketNotificationEvent(event.Type) {
		return s.dispatchTicketDomainEvent(event)
	}
	return s.dispatchAppDomainEvent(event)
}

func (s *Server) dispatchTicketDomainEvent(event store.DomainEvent) error {
	ticket, err := s.store.GetTicket(event.TicketID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	actor := event.Actor()
	payload := ticketEventPayload(event)
	if err := s.enqueueTicketEventEmails(event.Type, ticket, actor, payload); err != nil {
		return err
	}
	s.emitTicketWebhook(event.Type, ticket, actor)
	return nil
}

func (s *Server) dispatchAppDomainEvent(event store.DomainEvent) error {
	payload := appEventPayloadFromEvent(event)
	if payload.AccountLink == nil {
		return nil
	}
	user := store.User{
		ID:          payload.AccountLink.UserID,
		Username:    payload.AccountLink.Username,
		DisplayName: payload.AccountLink.DisplayName,
		Email:       payload.AccountLink.Email,
	}
	return s.enqueueAccountLinkEmail(payload.AccountLink.Event, user, payload.AccountLink.Token, payload.AccountLink.ExpiresAt)
}

func (s *Server) recordAuditEvent(event store.DomainEvent) error {
	input, ok := s.auditEventInput(event)
	if !ok {
		return nil
	}
	if _, err := s.store.RecordAuditEvent(input); err != nil && !errors.Is(err, store.ErrConflict) {
		return err
	}
	return nil
}

func (s *Server) auditEventInput(event store.DomainEvent) (store.CreateAuditEvent, bool) {
	if isTicketNotificationEvent(event.Type) {
		return s.ticketAuditEventInput(event)
	}
	payload := appEventPayloadFromEvent(event)
	if strings.TrimSpace(payload.TargetType) == "" {
		return store.CreateAuditEvent{}, false
	}
	return store.CreateAuditEvent{
		DomainEventID: event.ID,
		ActorUserID:   event.ActorUserID,
		ActorUsername: event.ActorUsername,
		Action:        event.Type,
		TargetType:    payload.TargetType,
		TargetID:      payload.TargetID,
		TargetName:    payload.TargetName,
		IP:            payload.IP,
		DetailsJSON:   detailsJSON(payload.Details),
	}, true
}

func (s *Server) ticketAuditEventInput(event store.DomainEvent) (store.CreateAuditEvent, bool) {
	ticket, err := s.store.GetTicket(event.TicketID)
	if errors.Is(err, store.ErrNotFound) {
		return store.CreateAuditEvent{}, false
	}
	if err != nil {
		return store.CreateAuditEvent{}, false
	}
	payload := ticketEventPayload(event)
	details := map[string]any{"product_id": ticket.ProductID}
	switch event.Type {
	case "ticket.created":
		details["source"] = payload.Source
	case "ticket.updated":
		details["previous_status"] = payload.PreviousStatus
		details["current_status"] = payload.CurrentStatus
		details["status_changed"] = payload.StatusChanged
		details["assignment_changed"] = payload.AssignmentChanged
	case "ticket.assigned":
		details["previous_assignee"] = payload.PreviousAssignee
		details["current_assignee"] = payload.CurrentAssignee
	case "ticket.commented":
		details["comment_id"] = payload.CommentID
		details["visibility"] = payload.CommentVisibility
	}
	return store.CreateAuditEvent{
		DomainEventID: event.ID,
		ActorUserID:   event.ActorUserID,
		ActorUsername: event.ActorUsername,
		Action:        event.Type,
		TargetType:    "ticket",
		TargetID:      ticket.ID,
		TargetName:    ticket.Key,
		DetailsJSON:   detailsJSON(details),
	}, true
}

func ticketEventPayload(event store.DomainEvent) store.TicketEventPayload {
	var payload store.TicketEventPayload
	_ = json.Unmarshal([]byte(event.PayloadJSON), &payload)
	return payload
}

func appEventPayloadFromEvent(event store.DomainEvent) store.AppEventPayload {
	var payload store.AppEventPayload
	_ = json.Unmarshal([]byte(event.PayloadJSON), &payload)
	return payload
}

func (s *Server) enqueueTicketEventEmails(event string, ticket store.Ticket, actor store.User, payload store.TicketEventPayload) error {
	switch event {
	case "ticket.created":
		if err := s.enqueueTicketEmails(event, ticket, actor); err != nil {
			return err
		}
		if payload.RequesterCreatedCopy {
			return s.enqueueRequesterEmail(event, ticket, "Pappice Support")
		}
	case "ticket.updated":
		if payload.HasPatch {
			if err := s.enqueueTicketEmails(event, ticket, actor); err != nil {
				return err
			}
		}
		if payload.StatusChanged && payload.TerminalStatus && !s.isSupportTicketRequester(actor, ticket) {
			return s.enqueueRequesterEmail(event, ticket, defaultString(actor.DisplayName, actor.Username))
		}
	case "ticket.assigned":
		if payload.AssignmentChanged && payload.OnlyAssigneePatch && !payload.PublicComment {
			return s.enqueueTicketEmails(event, ticket, actor)
		}
	case "ticket.commented":
		if payload.PublicComment && !payload.HasPatch {
			if err := s.enqueueTicketEmails(event, ticket, actor); err != nil {
				return err
			}
		}
		if payload.PublicComment && !s.isSupportTicketRequester(actor, ticket) {
			return s.enqueueRequesterEmail(event, ticket, defaultString(actor.DisplayName, actor.Username))
		}
	}
	return nil
}

func detailsJSON(details map[string]any) string {
	if len(details) == 0 {
		return ""
	}
	data, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(data)
}

func isTicketNotificationEvent(event string) bool {
	switch event {
	case "ticket.created", "ticket.updated", "ticket.commented", "ticket.assigned":
		return true
	default:
		return false
	}
}
