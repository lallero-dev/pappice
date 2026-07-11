package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"pappice/internal/store"
)

const domainEventPruneInterval = time.Hour

func (s *Server) RunEventDispatcher(ctx context.Context, interval time.Duration) {
	logger := s.options.Logger
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

func (s *Server) pruneProcessedDomainEvents(logger Logger) {
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
			setFirstError(&firstErr, err)
			setFirstError(&firstErr, s.store.MarkDomainEventFailed(event.ID, err))
			continue
		}
		projection, err := s.domainEventProjection(event)
		if err != nil {
			setFirstError(&firstErr, err)
			setFirstError(&firstErr, s.store.MarkDomainEventFailed(event.ID, err))
			continue
		}
		if err := s.store.ApplyDomainEventProjection(event.ID, projection); err != nil {
			setFirstError(&firstErr, err)
			setFirstError(&firstErr, s.store.MarkDomainEventFailed(event.ID, err))
		}
	}
	setFirstError(&firstErr, s.dispatchPendingWebhookNotifications(ctx, limit))
	return firstErr
}

func (s *Server) domainEventProjection(event store.DomainEvent) (store.DomainEventProjection, error) {
	projection := store.DomainEventProjection{}
	if isTicketNotificationEvent(event.Type) {
		payload, err := decodeEventPayload[store.TicketEventPayload](event)
		if err != nil {
			return store.DomainEventProjection{}, err
		}
		ticket, err := s.store.GetTicket(event.TicketID)
		if errors.Is(err, store.ErrNotFound) {
			return projection, nil
		}
		if err != nil {
			return store.DomainEventProjection{}, err
		}
		audit := ticketAuditEventInput(event, payload, ticket)
		projection.Audit = &audit
		if err := s.projectTicketDomainEvent(event, payload, ticket, &projection); err != nil {
			return store.DomainEventProjection{}, err
		}
		return projection, nil
	}
	payload, err := decodeEventPayload[store.AppEventPayload](event)
	if err != nil {
		return store.DomainEventProjection{}, err
	}
	if audit, ok := appAuditEventInput(event, payload); ok {
		projection.Audit = &audit
	}
	if err := s.projectAppDomainEvent(event.Type, payload, &projection); err != nil {
		return store.DomainEventProjection{}, err
	}
	return projection, nil
}

func (s *Server) projectTicketDomainEvent(event store.DomainEvent, payload store.TicketEventPayload, ticket store.Ticket, projection *store.DomainEventProjection) error {
	actor := event.Actor()
	sendAfter := event.CreatedAt.Add(s.options.NotificationDelay)
	emails, err := s.ticketEventEmails(event.Type, ticket, actor, payload, sendAfter)
	if err != nil {
		return err
	}
	webhooks, err := s.ticketWebhookNotifications(event.Type, ticket, actor, event.CreatedAt, sendAfter)
	if err != nil {
		return err
	}
	projection.EmailNotifications = append(projection.EmailNotifications, emails...)
	projection.WebhookNotifications = append(projection.WebhookNotifications, webhooks...)
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
			setFirstError(&firstErr, err)
			setFirstError(&firstErr, s.store.MarkWebhookNotificationFailed(notification.ID, err, 5))
			continue
		}
		hook, err := s.store.GetWebhook(notification.WebhookID)
		if errors.Is(err, store.ErrNotFound) {
			setFirstError(&firstErr, s.store.MarkWebhookNotificationSent(notification.ID))
			continue
		}
		if err != nil {
			setFirstError(&firstErr, err)
			setFirstError(&firstErr, s.store.MarkWebhookNotificationFailed(notification.ID, err, 5))
			continue
		}
		delivery, recordErr := s.deliverWebhook(hook, notification.Event, notification.TicketID, []byte(notification.PayloadJSON))
		if strings.TrimSpace(delivery.Error) != "" {
			deliveryErr := errors.New(delivery.Error)
			setFirstError(&firstErr, deliveryErr)
			setFirstError(&firstErr, recordErr)
			setFirstError(&firstErr, s.store.MarkWebhookNotificationFailed(notification.ID, deliveryErr, 5))
			continue
		}
		setFirstError(&firstErr, s.store.MarkWebhookNotificationSent(notification.ID))
		setFirstError(&firstErr, recordErr)
	}
	return firstErr
}

func setFirstError(target *error, err error) {
	if err != nil && *target == nil {
		*target = err
	}
}

func (s *Server) projectAppDomainEvent(eventType string, payload store.AppEventPayload, projection *store.DomainEventProjection) error {
	if payload.AccountLink == nil {
		return nil
	}
	user, err := s.store.GetUser(payload.AccountLink.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if user.Disabled {
		return nil
	}
	var emailEvent string
	switch eventType {
	case "user.created":
		emailEvent = "account.setup"
	case "user.password_reset_requested":
		emailEvent = "account.reset"
	default:
		return fmt.Errorf("account link payload on %s event", eventType)
	}
	projection.EmailNotifications = append(projection.EmailNotifications, s.accountLinkEmailNotifications(emailEvent, user, payload.AccountLink.Token, payload.AccountLink.ExpiresAt)...)
	return nil
}

func appAuditEventInput(event store.DomainEvent, payload store.AppEventPayload) (store.CreateAuditEvent, bool) {
	if strings.TrimSpace(payload.TargetType) == "" {
		return store.CreateAuditEvent{}, false
	}
	return store.CreateAuditEvent{
		DomainEventID: event.ID,
		ActorUserID:   event.ActorUserID,
		ActorEmail:    event.ActorEmail,
		Action:        event.Type,
		TargetType:    payload.TargetType,
		TargetID:      payload.TargetID,
		TargetName:    payload.TargetName,
		IP:            payload.IP,
		DetailsJSON:   detailsJSON(payload.Details),
	}, true
}

func ticketAuditEventInput(event store.DomainEvent, payload store.TicketEventPayload, ticket store.Ticket) store.CreateAuditEvent {
	details := map[string]any{"product_id": ticket.ProductID}
	switch event.Type {
	case "ticket.created":
		details["source"] = payload.Source
	case "ticket.updated":
		details["previous_status"] = payload.PreviousStatus
		details["current_status"] = payload.CurrentStatus
		details["status_changed"] = payload.PreviousStatus != payload.CurrentStatus
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
		ActorEmail:    event.ActorEmail,
		Action:        event.Type,
		TargetType:    "ticket",
		TargetID:      ticket.ID,
		TargetName:    ticket.Key,
		DetailsJSON:   detailsJSON(details),
	}
}

func decodeEventPayload[T any](event store.DomainEvent) (T, error) {
	var payload T
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		return payload, fmt.Errorf("decode %s event payload: %w", event.Type, err)
	}
	return payload, nil
}

func (s *Server) ticketEventEmails(event string, ticket store.Ticket, actor store.EventActor, payload store.TicketEventPayload, sendAfter time.Time) ([]store.CreateEmailNotification, error) {
	notifyStaff := false
	notifyRequester := false
	requesterActorName := defaultString(actor.DisplayName, actor.Email)
	switch event {
	case "ticket.created":
		notifyStaff = true
		if payload.Source == "portal" {
			notifyRequester = true
			requesterActorName = "Pappice Support"
		}
	case "ticket.updated":
		notifyStaff = payload.HasPatch
		notifyRequester = payload.PreviousStatus != payload.CurrentStatus && requesterTerminalStatus(payload.CurrentStatus) && actor.UserID != ticket.RequesterUserID
	case "ticket.assigned":
		notifyStaff = payload.AssignmentChanged && payload.OnlyAssigneePatch && !payload.PublicComment
	case "ticket.commented":
		notifyStaff = payload.PublicComment && !payload.HasPatch
		notifyRequester = payload.PublicComment && actor.UserID != ticket.RequesterUserID
	}
	inputs := make([]store.CreateEmailNotification, 0)
	if notifyStaff {
		notifications, err := s.ticketEmailNotifications(event, ticket, actor, sendAfter)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, notifications...)
	}
	if notifyRequester {
		inputs = append(inputs, s.requesterEmailNotifications(event, ticket, requesterActorName, sendAfter)...)
	}
	return inputs, nil
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
