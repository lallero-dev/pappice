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

const domainEventPruneInterval = time.Hour

func (s *Server) RunEventDispatcher(ctx context.Context, interval time.Duration, logger eventLogger) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := s.dispatchPendingEvents(ctx, 25); err != nil && logger != nil {
		logger.Printf("domain event dispatch: %v", err)
	}
	s.pruneProcessedDomainEvents(logger)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(domainEventPruneInterval)
	defer pruneTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.dispatchPendingEvents(ctx, 25); err != nil && logger != nil {
				logger.Printf("domain event dispatch: %v", err)
			}
		case <-pruneTicker.C:
			s.pruneProcessedDomainEvents(logger)
		}
	}
}

func (s *Server) pruneProcessedDomainEvents(logger eventLogger) {
	if s.options.DomainEventRetention <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.options.DomainEventRetention)
	var total int64
	for {
		pruned, err := s.store.PruneProcessedDomainEvents(cutoff, 500)
		if err != nil {
			if logger != nil {
				logger.Printf("domain event prune: %v", err)
			}
			return
		}
		total += pruned
		if pruned < 500 {
			break
		}
	}
	if total > 0 && logger != nil {
		logger.Printf("domain event prune: removed %d processed event(s)", total)
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
		projection, err := s.domainEventProjection(event)
		if err != nil {
			_ = s.store.MarkDomainEventFailed(event.ID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := s.store.ApplyDomainEventProjection(event.ID, projection); err != nil {
			_ = s.store.MarkDomainEventFailed(event.ID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := s.dispatchPendingWebhookNotifications(ctx, limit); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Server) domainEventProjection(event store.DomainEvent) (store.DomainEventProjection, error) {
	projection := store.DomainEventProjection{}
	if audit, ok := s.auditEventInput(event); ok {
		projection.Audit = &audit
	}
	if isTicketNotificationEvent(event.Type) {
		if err := s.projectTicketDomainEvent(event, &projection); err != nil {
			return store.DomainEventProjection{}, err
		}
		return projection, nil
	}
	s.projectAppDomainEvent(event, &projection)
	return projection, nil
}

func (s *Server) projectTicketDomainEvent(event store.DomainEvent, projection *store.DomainEventProjection) error {
	ticket, err := s.store.GetTicket(event.TicketID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	actor := event.Actor()
	payload := ticketEventPayload(event)
	sendAfter := event.CreatedAt.Add(s.options.NotificationDelay)
	projection.EmailNotifications = append(projection.EmailNotifications, s.ticketEventEmails(event.Type, ticket, actor, payload, sendAfter)...)
	projection.WebhookNotifications = append(projection.WebhookNotifications, s.ticketWebhookNotifications(event.Type, ticket, actor, event.CreatedAt, sendAfter)...)
	return nil
}

func (s *Server) dispatchPendingWebhookNotifications(ctx context.Context, limit int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	notifications, err := s.store.ClaimWebhookNotifications(limit, time.Minute)
	if err != nil {
		return err
	}
	var firstErr error
	for _, notification := range notifications {
		if err := ctx.Err(); err != nil {
			_ = s.store.MarkWebhookNotificationFailed(notification.ID, err, 5)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		hook, err := s.store.GetWebhook(notification.WebhookID)
		if errors.Is(err, store.ErrNotFound) {
			_ = s.store.MarkWebhookNotificationSent(notification.ID)
			continue
		}
		if err != nil {
			_ = s.store.MarkWebhookNotificationFailed(notification.ID, err, 5)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		delivery := s.deliverWebhook(hook, notification.Event, notification.TicketID, []byte(notification.PayloadJSON))
		if strings.TrimSpace(delivery.Error) != "" {
			err := errors.New(delivery.Error)
			_ = s.store.MarkWebhookNotificationFailed(notification.ID, err, 5)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := s.store.MarkWebhookNotificationSent(notification.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) projectAppDomainEvent(event store.DomainEvent, projection *store.DomainEventProjection) {
	payload := appEventPayloadFromEvent(event)
	if payload.AccountLink == nil {
		return
	}
	user := store.User{
		ID:          payload.AccountLink.UserID,
		Username:    payload.AccountLink.Username,
		DisplayName: payload.AccountLink.DisplayName,
		Email:       payload.AccountLink.Email,
	}
	projection.EmailNotifications = append(projection.EmailNotifications, s.accountLinkEmailNotifications(payload.AccountLink.Event, user, payload.AccountLink.Token, payload.AccountLink.ExpiresAt)...)
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

func (s *Server) ticketEventEmails(event string, ticket store.Ticket, actor store.User, payload store.TicketEventPayload, sendAfter time.Time) []store.CreateEmailNotification {
	inputs := make([]store.CreateEmailNotification, 0)
	switch event {
	case "ticket.created":
		inputs = append(inputs, s.ticketEmailNotifications(event, ticket, actor, sendAfter)...)
		if payload.RequesterCreatedCopy {
			inputs = append(inputs, s.requesterEmailNotifications(event, ticket, "Pappice Support", sendAfter)...)
		}
	case "ticket.updated":
		if payload.HasPatch {
			inputs = append(inputs, s.ticketEmailNotifications(event, ticket, actor, sendAfter)...)
		}
		if payload.StatusChanged && payload.TerminalStatus && !s.isSupportTicketRequester(actor, ticket) {
			inputs = append(inputs, s.requesterEmailNotifications(event, ticket, defaultString(actor.DisplayName, actor.Username), sendAfter)...)
		}
	case "ticket.assigned":
		if payload.AssignmentChanged && payload.OnlyAssigneePatch && !payload.PublicComment {
			inputs = append(inputs, s.ticketEmailNotifications(event, ticket, actor, sendAfter)...)
		}
	case "ticket.commented":
		if payload.PublicComment && !payload.HasPatch {
			inputs = append(inputs, s.ticketEmailNotifications(event, ticket, actor, sendAfter)...)
		}
		if payload.PublicComment && !s.isSupportTicketRequester(actor, ticket) {
			inputs = append(inputs, s.requesterEmailNotifications(event, ticket, defaultString(actor.DisplayName, actor.Username), sendAfter)...)
		}
	}
	return inputs
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
