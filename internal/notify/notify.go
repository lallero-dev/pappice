package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"pappice/internal/store"
)

type Message struct {
	From     string
	To       string
	ToName   string
	Subject  string
	BodyText string
	BodyHTML string
}

type Mailer interface {
	Send(context.Context, Message) error
}

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLSMode  string
	Timeout  time.Duration
}

func (c SMTPConfig) Enabled() bool {
	return strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.From) != ""
}

type SMTPMailer struct {
	config SMTPConfig
}

func NewSMTPMailer(config SMTPConfig) (*SMTPMailer, error) {
	config.Host = strings.TrimSpace(config.Host)
	config.From = strings.TrimSpace(config.From)
	config.TLSMode = strings.ToLower(strings.TrimSpace(config.TLSMode))
	if config.TLSMode == "" {
		config.TLSMode = "starttls"
	}
	if config.Port == 0 {
		if config.TLSMode == "tls" {
			config.Port = 465
		} else {
			config.Port = 587
		}
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if !config.Enabled() {
		return nil, errors.New("smtp host and from address are required")
	}
	if _, err := mail.ParseAddress(config.From); err != nil {
		return nil, fmt.Errorf("invalid smtp from address: %w", err)
	}
	switch config.TLSMode {
	case "none", "starttls", "tls":
	default:
		return nil, fmt.Errorf("invalid smtp tls mode %q", config.TLSMode)
	}
	return &SMTPMailer{config: config}, nil
}

func (m *SMTPMailer) Send(ctx context.Context, message Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return fmt.Errorf("invalid recipient address: %w", err)
	}
	from, err := mail.ParseAddress(firstNonEmpty(message.From, m.config.From))
	if err != nil {
		return fmt.Errorf("invalid sender address: %w", err)
	}
	client, err := m.connect()
	if err != nil {
		return err
	}
	defer client.Close()

	if m.config.Username != "" {
		auth := smtp.PlainAuth("", m.config.Username, m.config.Password, m.config.Host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(renderMessage(from, to, message)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (m *SMTPMailer) connect() (*smtp.Client, error) {
	address := net.JoinHostPort(m.config.Host, strconv.Itoa(m.config.Port))
	dialer := net.Dialer{Timeout: m.config.Timeout}
	if m.config.TLSMode == "tls" {
		conn, err := tls.DialWithDialer(&dialer, "tcp", address, &tls.Config{ServerName: m.config.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return nil, err
		}
		return smtp.NewClient(conn, m.config.Host)
	}
	conn, err := dialer.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	client, err := smtp.NewClient(conn, m.config.Host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if m.config.TLSMode == "starttls" {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			_ = client.Close()
			return nil, errors.New("smtp server does not advertise STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: m.config.Host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	return client, nil
}

type Worker struct {
	Store       *store.Store
	Mailer      Mailer
	From        string
	Interval    time.Duration
	LeaseFor    time.Duration
	BatchSize   int
	MaxAttempts int
	Logger      *log.Logger
}

func (w Worker) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_ = w.ProcessOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w Worker) ProcessOnce(ctx context.Context) error {
	if w.Store == nil || w.Mailer == nil {
		return nil
	}
	leaseFor := w.LeaseFor
	if leaseFor <= 0 {
		leaseFor = time.Minute
	}
	notifications, err := w.Store.ClaimEmailNotifications(w.BatchSize, leaseFor)
	if err != nil {
		w.logf("claim email notifications: %v", err)
		return err
	}
	for _, notification := range notifications {
		if err := ctx.Err(); err != nil {
			return err
		}
		message := Message{
			From:     firstNonEmpty(w.From, ""),
			To:       notification.RecipientEmail,
			ToName:   notification.RecipientName,
			Subject:  notification.Subject,
			BodyText: notification.BodyText,
			BodyHTML: notification.BodyHTML,
		}
		if err := w.Mailer.Send(ctx, message); err != nil {
			w.logf("send email notification %d: %v", notification.ID, err)
			_ = w.Store.MarkEmailFailed(notification.ID, err, w.MaxAttempts)
			continue
		}
		if err := w.Store.MarkEmailSent(notification.ID); err != nil {
			w.logf("mark email notification %d sent: %v", notification.ID, err)
		}
	}
	return nil
}

func (w Worker) logf(format string, args ...any) {
	if w.Logger != nil {
		w.Logger.Printf(format, args...)
	}
}

func renderMessage(from, to *mail.Address, message Message) []byte {
	boundary := "pappice-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	bodyText := normalizeLineEndings(message.BodyText)
	bodyHTML := normalizeLineEndings(message.BodyHTML)

	var out bytes.Buffer
	writeHeader(&out, "From", from.String())
	writeHeader(&out, "To", (&mail.Address{Name: message.ToName, Address: to.Address}).String())
	writeHeader(&out, "Subject", strings.ReplaceAll(message.Subject, "\n", " "))
	writeHeader(&out, "MIME-Version", "1.0")
	if bodyHTML == "" {
		writeHeader(&out, "Content-Type", `text/plain; charset="utf-8"`)
		out.WriteString("\r\n")
		out.WriteString(bodyText)
		if !strings.HasSuffix(bodyText, "\r\n") {
			out.WriteString("\r\n")
		}
		return out.Bytes()
	}
	writeHeader(&out, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	out.WriteString("\r\n")
	out.WriteString("--" + boundary + "\r\n")
	out.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n\r\n")
	out.WriteString(bodyText + "\r\n")
	out.WriteString("--" + boundary + "\r\n")
	out.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n\r\n")
	out.WriteString(bodyHTML + "\r\n")
	out.WriteString("--" + boundary + "--\r\n")
	return out.Bytes()
}

func writeHeader(out *bytes.Buffer, key, value string) {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	out.WriteString(key)
	out.WriteString(": ")
	out.WriteString(value)
	out.WriteString("\r\n")
}

func normalizeLineEndings(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
