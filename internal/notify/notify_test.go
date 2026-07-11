package notify

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"pappice/internal/store"
)

type fakeMailer struct {
	messages []Message
	err      error
}

func (m *fakeMailer) Send(_ context.Context, message Message) error {
	if m.err != nil {
		return m.err
	}
	m.messages = append(m.messages, message)
	return nil
}

func TestWorkerSendsClaimedEmail(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	user, err := tracker.CreateFirstAdmin(store.CreateUser{
		Password: "correct horse",
		Email:    "admin@example.test",
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	queued, err := tracker.EnqueueEmailNotifications([]store.CreateEmailNotification{{
		UserID:         user.ID,
		RecipientEmail: user.Email,
		RecipientName:  user.DisplayName,
		Event:          "ticket.created",
		Subject:        "Subject",
		BodyText:       "Body",
	}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	mailer := &fakeMailer{}
	worker := Worker{
		Store:       tracker,
		Mailer:      mailer,
		From:        "noreply@example.test",
		BatchSize:   10,
		MaxAttempts: 3,
	}
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process once: %v", err)
	}
	if len(mailer.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(mailer.messages))
	}
	if mailer.messages[0].To != user.Email || mailer.messages[0].Subject != "Subject" {
		t.Fatalf("message = %#v", mailer.messages[0])
	}
	notification, err := tracker.GetEmailNotification(queued[0].ID)
	if err != nil {
		t.Fatalf("get notification: %v", err)
	}
	if notification.Status != "sent" {
		t.Fatalf("status = %q, want sent", notification.Status)
	}
}

func TestWorkerMarksFailedEmail(t *testing.T) {
	tracker, err := store.Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	user, err := tracker.CreateFirstAdmin(store.CreateUser{
		Password: "correct horse",
		Email:    "admin@example.test",
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	queued, err := tracker.EnqueueEmailNotifications([]store.CreateEmailNotification{{
		UserID:         user.ID,
		RecipientEmail: user.Email,
		RecipientName:  user.DisplayName,
		Event:          "ticket.created",
		Subject:        "Subject",
		BodyText:       "Body",
	}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	worker := Worker{
		Store:       tracker,
		Mailer:      &fakeMailer{err: errors.New("smtp offline")},
		BatchSize:   10,
		MaxAttempts: 1,
	}
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process once: %v", err)
	}
	notification, err := tracker.GetEmailNotification(queued[0].ID)
	if err != nil {
		t.Fatalf("get notification: %v", err)
	}
	if notification.Status != "failed" || notification.LastError != "smtp offline" || notification.Attempts != 1 {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestWorkerRunReturnsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		Worker{Interval: time.Millisecond}.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not return after context cancellation")
	}
}

func TestSMTPMailerSend(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	messages := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveSMTPTestConnection(listener, messages)
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}
	mailer, err := NewSMTPMailer(SMTPConfig{
		Host:    host,
		Port:    port,
		From:    "Pappice <noreply@example.test>",
		TLSMode: "none",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new smtp mailer: %v", err)
	}

	if err := mailer.Send(context.Background(), Message{
		To:       "Client <client@example.test>",
		ToName:   "Client",
		Subject:  "Ticket update",
		BodyText: "Plain update",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "Subject: Ticket update") || !strings.Contains(message, "Plain update") {
			t.Fatalf("smtp message = %q", message)
		}
	case <-time.After(time.Second):
		t.Fatal("smtp server did not receive message")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("smtp server: %v", err)
	}

	if err := mailer.Send(context.Background(), Message{To: "bad address", BodyText: "Body"}); err == nil {
		t.Fatal("invalid recipient should fail before dialing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mailer.Send(ctx, Message{To: "client@example.test", BodyText: "Body"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled send error = %v, want context.Canceled", err)
	}
}

func TestSMTPConfigAndMessageRendering(t *testing.T) {
	if (SMTPConfig{}).Enabled() {
		t.Fatal("empty smtp config should not be enabled")
	}
	if _, err := NewSMTPMailer(SMTPConfig{Host: "smtp.example.test", From: "bad address"}); err == nil {
		t.Fatal("invalid from address should fail")
	}
	if _, err := NewSMTPMailer(SMTPConfig{Host: "smtp.example.test", From: "noreply@example.test", TLSMode: "invalid"}); err == nil {
		t.Fatal("invalid tls mode should fail")
	}
	mailer, err := NewSMTPMailer(SMTPConfig{Host: "smtp.example.test", From: "noreply@example.test", TLSMode: "none"})
	if err != nil {
		t.Fatalf("new smtp mailer: %v", err)
	}
	if mailer.config.Port != 587 || mailer.config.Timeout <= 0 || mailer.config.TLSMode != "none" {
		t.Fatalf("smtp defaults = %#v", mailer.config)
	}

	rendered := string(renderMessage(
		mustAddress(t, "Pappice <noreply@example.test>"),
		mustAddress(t, "client@example.test"),
		Message{
			ToName:   "Client",
			Subject:  "Line one\nline two",
			BodyText: "Hello\nplain",
			BodyHTML: "<p>Hello</p>",
		},
	))
	for _, want := range []string{
		"Subject: Line one line two",
		"multipart/alternative",
		"text/plain",
		"text/html",
		"Hello\r\nplain",
		"<p>Hello</p>",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered message missing %q:\n%s", want, rendered)
		}
	}
}

func mustAddress(t *testing.T, raw string) *mail.Address {
	t.Helper()
	address, err := mail.ParseAddress(raw)
	if err != nil {
		t.Fatalf("parse address: %v", err)
	}
	return address
}

func serveSMTPTestConnection(listener net.Listener, messages chan<- string) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) error {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return err
		}
		return writer.Flush()
	}
	if err := writeLine("220 pappice.test ESMTP"); err != nil {
		return err
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			if err := writeLine("250 pappice.test"); err != nil {
				return err
			}
		case strings.HasPrefix(command, "MAIL FROM:"), strings.HasPrefix(command, "RCPT TO:"):
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case command == "DATA":
			if err := writeLine("354 End data with <CR><LF>.<CR><LF>"); err != nil {
				return err
			}
			var message strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				if strings.TrimSpace(line) == "." {
					break
				}
				message.WriteString(line)
			}
			messages <- message.String()
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case command == "QUIT":
			return writeLine("221 Bye")
		default:
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		}
	}
}
