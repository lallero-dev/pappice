package notify

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSMTPMailerStopsBlockedSend(t *testing.T) {
	for _, phase := range []string{"greeting", "hello", "tls", "starttls", "data", "quit"} {
		for _, stopBy := range []string{"timeout", "deadline", "cancel"} {
			t.Run(phase+"/"+stopBy, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				stalled := make(chan struct{})
				serverDone := make(chan error, 1)
				go func() {
					conn, err := listener.Accept()
					if err != nil {
						serverDone <- err
						return
					}
					defer conn.Close()
					// Keep a regression from leaving a blocked test goroutine.
					_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
					serverDone <- stallSMTP(conn, phase, stalled)
				}()

				mode := "none"
				if phase == "tls" || phase == "starttls" {
					mode = phase
				}
				timeout := time.Minute
				if stopBy == "timeout" {
					timeout = 200 * time.Millisecond
				}
				mailer, err := NewSMTPMailer(SMTPConfig{
					Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
					From: "noreply@example.test", TLSMode: mode, Timeout: timeout,
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if stopBy == "deadline" {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, 200*time.Millisecond)
					defer stop()
				}
				sendDone := make(chan error, 1)
				go func() {
					sendDone <- mailer.Send(ctx, Message{To: "client@example.test", BodyText: "Body"})
				}()
				select {
				case <-stalled:
				case err := <-serverDone:
					t.Fatalf("SMTP server stopped before %s: %v", phase, err)
				case <-time.After(3 * time.Second):
					t.Fatalf("send did not reach %s", phase)
				}
				if stopBy == "cancel" {
					cancel()
				}
				select {
				case err := <-sendDone:
					if stopBy == "cancel" {
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("send error = %v, want cancellation", err)
						}
					} else {
						var timeoutErr net.Error
						if !errors.As(err, &timeoutErr) || !timeoutErr.Timeout() {
							t.Fatalf("send error = %v, want timeout", err)
						}
					}
				case <-time.After(3 * time.Second):
					t.Fatal("send stayed blocked")
				}
				select {
				case err := <-serverDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("send left its connection open")
				}
			})
		}
	}
}

func stallSMTP(conn net.Conn, phase string, stalled chan<- struct{}) error {
	stall := func() error {
		close(stalled)
		_, _ = io.Copy(io.Discard, conn)
		return nil
	}
	if phase == "greeting" || phase == "tls" {
		return stall()
	}
	protocol := textproto.NewConn(conn)
	readCommand := func(want string) error {
		line, err := protocol.ReadLine()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, want) {
			return fmt.Errorf("SMTP command = %q, want %s", line, want)
		}
		return nil
	}
	if err := protocol.PrintfLine("220 pappice.test ESMTP"); err != nil {
		return err
	}
	if err := readCommand("EHLO "); err != nil {
		return err
	}
	if phase == "hello" {
		return stall()
	}
	if phase == "starttls" {
		if err := protocol.PrintfLine("250-pappice.test\r\n250 STARTTLS"); err != nil {
			return err
		}
		if err := readCommand("STARTTLS"); err != nil {
			return err
		}
		if err := protocol.PrintfLine("220 Ready for TLS"); err != nil {
			return err
		}
		return stall()
	}
	if err := protocol.PrintfLine("250 pappice.test"); err != nil {
		return err
	}
	for _, command := range []string{"MAIL FROM:", "RCPT TO:", "DATA"} {
		if err := readCommand(command); err != nil {
			return err
		}
		response := "250 OK"
		if command == "DATA" {
			response = "354 Send message"
		}
		if err := protocol.PrintfLine("%s", response); err != nil {
			return err
		}
	}
	if _, err := protocol.ReadDotBytes(); err != nil {
		return err
	}
	if phase == "quit" {
		if err := protocol.PrintfLine("250 OK"); err != nil {
			return err
		}
		if err := readCommand("QUIT"); err != nil {
			return err
		}
	}
	return stall()
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

func mustAddress(t *testing.T, raw string) *mail.Address {
	t.Helper()
	address, err := mail.ParseAddress(raw)
	if err != nil {
		t.Fatalf("parse address: %v", err)
	}
	return address
}
