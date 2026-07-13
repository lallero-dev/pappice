package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"pappice/internal/security"
	"pappice/internal/store"
)

func (s *Server) newWebhookClient() *http.Client {
	return &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			DialContext:           s.dialWebhookContext,
			ForceAttemptHTTP2:     true,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (s *Server) ticketWebhookNotifications(event string, ticket store.Ticket, actor store.EventActor, createdAt time.Time, sendAfter time.Time) ([]store.CreateWebhookNotification, error) {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	payload := map[string]any{
		"event":      event,
		"created_at": createdAt.UTC(),
		"actor":      actor,
		"ticket":     ticket,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hooks, err := s.store.ListWebhooksForEvent(event, ticket.ProductID)
	if err != nil {
		return nil, err
	}
	inputs := make([]store.CreateWebhookNotification, 0)
	for _, hook := range hooks {
		inputs = append(inputs, store.CreateWebhookNotification{
			WebhookID:   hook.ID,
			ProductID:   hook.ProductID,
			TicketID:    ticket.ID,
			Event:       event,
			PayloadJSON: string(body),
			SendAfter:   normalizeNotificationSendAfter(sendAfter),
			Coalesce:    true,
		})
	}
	return inputs, nil
}

func (s *Server) deliverWebhook(hook store.Webhook, event string, ticketID int64, body []byte) (store.WebhookDelivery, error) {
	started := time.Now()
	delivery := store.WebhookDelivery{
		WebhookID: hook.ID,
		ProductID: hook.ProductID,
		Event:     event,
		TicketID:  ticketID,
	}
	if err := s.validateWebhookTarget(hook.URL); err != nil {
		delivery.Error = err.Error()
		delivery.DurationMS = time.Since(started).Milliseconds()
		return s.recordWebhookDelivery(delivery)
	}
	req, err := http.NewRequest(http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		delivery.Error = err.Error()
		delivery.DurationMS = time.Since(started).Milliseconds()
		return s.recordWebhookDelivery(delivery)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "pappice-webhook")
	req.Header.Set("X-Pappice-Event", event)
	if hook.Secret != "" {
		req.Header.Set("X-Pappice-Signature", "sha256="+security.HMACSHA256(hook.Secret, body))
	}

	resp, err := s.client.Do(req)
	delivery.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		delivery.Error = err.Error()
		return s.recordWebhookDelivery(delivery)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		log.Printf("failed to drain response body: %v", err)
	}
	delivery.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		delivery.Error = fmt.Sprintf("webhook returned HTTP %d", resp.StatusCode)
	}
	return s.recordWebhookDelivery(delivery)
}

func (s *Server) recordWebhookDelivery(delivery store.WebhookDelivery) (store.WebhookDelivery, error) {
	if err := s.store.RecordDelivery(delivery); err != nil {
		return delivery, fmt.Errorf("record webhook delivery: %w", err)
	}
	return delivery, nil
}

func (s *Server) validateWebhookTarget(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("invalid webhook URL")
	}
	if parsed.Scheme != "https" && !(s.options.AllowInsecureWebhooks && parsed.Scheme == "http") {
		return fmt.Errorf("webhook URL must use https")
	}
	if s.options.AllowPrivateWebhooks {
		return nil
	}
	if !publicWebhookHost(parsed.Hostname()) {
		return fmt.Errorf("webhook private targets are blocked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		if !publicWebhookAddr(addr) {
			return fmt.Errorf("webhook private targets are blocked")
		}
	}
	return nil
}

func (s *Server) dialWebhookContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if s.options.AllowPrivateWebhooks {
		return dialer.DialContext(ctx, network, address)
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if !publicWebhookHost(host) {
		return nil, fmt.Errorf("webhook private targets are blocked")
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}

	var dialErr error
	for _, addr := range addrs {
		if !publicWebhookAddr(addr) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	if dialErr != nil {
		return nil, dialErr
	}
	return nil, fmt.Errorf("webhook private targets are blocked")
}

func publicWebhookHost(host string) bool {
	return !strings.EqualFold(strings.TrimSpace(host), "localhost")
}

func publicWebhookAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast() &&
		!ip.IsUnspecified()
}
