package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
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
