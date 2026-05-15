package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"pemmece/internal/security"
	"pemmece/internal/store"
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

func (s *Server) emitIssueWebhook(event string, issue store.Issue, actor store.User) {
	payload := map[string]any{
		"event":      event,
		"created_at": time.Now().UTC(),
		"actor":      store.ToPublicUser(actor),
		"ticket":     issue,
	}
	body, _ := json.Marshal(payload)
	for _, hook := range s.store.ListWebhooksForEvent(event, issue.ProjectID) {
		go s.deliverWebhook(hook, event, issue.ID, body)
	}
}

func (s *Server) deliverWebhook(hook store.Webhook, event string, issueID int64, body []byte) store.WebhookDelivery {
	started := time.Now()
	delivery := store.WebhookDelivery{
		WebhookID: hook.ID,
		ProjectID: hook.ProjectID,
		Event:     event,
		IssueID:   issueID,
	}
	if err := s.validateWebhookTarget(hook.URL); err != nil {
		delivery.Error = err.Error()
		delivery.DurationMS = time.Since(started).Milliseconds()
		_ = s.store.RecordDelivery(delivery)
		return delivery
	}
	req, err := http.NewRequest(http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		delivery.Error = err.Error()
		delivery.DurationMS = time.Since(started).Milliseconds()
		_ = s.store.RecordDelivery(delivery)
		return delivery
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "pemmece-webhook")
	req.Header.Set("X-Pemmece-Event", event)
	if hook.Secret != "" {
		req.Header.Set("X-Pemmece-Signature", "sha256="+security.HMACSHA256(hook.Secret, body))
	}

	resp, err := s.client.Do(req)
	delivery.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		delivery.Error = err.Error()
		_ = s.store.RecordDelivery(delivery)
		return delivery
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	delivery.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		delivery.Error = fmt.Sprintf("webhook returned HTTP %d", resp.StatusCode)
	}
	_ = s.store.RecordDelivery(delivery)
	return delivery
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
