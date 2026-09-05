package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"pappice/internal/store"
)

const webhookDispatchBatchSize = 25

func (s *Server) RunWebhookDispatcher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.dispatchPendingWebhookNotifications(ctx, webhookDispatchBatchSize); err != nil && ctx.Err() == nil && s.options.Logger != nil {
			s.options.Logger.Printf("webhook dispatch: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.webhookWake:
		case <-ticker.C:
		}
	}
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
			return err
		}
		hook, err := s.store.GetWebhook(notification.WebhookID)
		if errors.Is(err, store.ErrNotFound) {
			setFirstError(&firstErr, s.store.MarkWebhookNotificationSent(notification.ID))
			continue
		}
		if err != nil {
			setFirstError(&firstErr, err)
			setFirstError(&firstErr, s.store.MarkWebhookNotificationFailed(notification.ID, err, maxDispatchAttempts))
			continue
		}
		delivery, recordErr := s.deliverWebhook(ctx, hook, notification)
		if strings.TrimSpace(delivery.Error) != "" {
			deliveryErr := errors.New(delivery.Error)
			setFirstError(&firstErr, deliveryErr)
			setFirstError(&firstErr, recordErr)
			setFirstError(&firstErr, s.store.MarkWebhookNotificationFailed(notification.ID, deliveryErr, maxDispatchAttempts))
			continue
		}
		setFirstError(&firstErr, s.store.MarkWebhookNotificationSent(notification.ID))
		setFirstError(&firstErr, recordErr)
	}
	if limit > 0 && len(notifications) == limit {
		s.dispatchWebhooksSoon()
	}
	return firstErr
}

func (s *Server) dispatchWebhooksSoon() {
	select {
	case s.webhookWake <- struct{}{}:
	default:
	}
}
