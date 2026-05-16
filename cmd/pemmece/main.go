package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"pemmece/internal/notify"
	"pemmece/internal/server"
	"pemmece/internal/store"
)

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Fatalf("load .env: %v", err)
	}

	addr := flag.String("addr", envOr("PEMMECE_ADDR", "127.0.0.1:8388"), "HTTP listen address")
	dbPath := flag.String("db", envOr("PEMMECE_DB", "pemmece.db"), "path to SQLite database file")
	tlsCert := flag.String("tls-cert", envOr("PEMMECE_TLS_CERT", ""), "TLS certificate path")
	tlsKey := flag.String("tls-key", envOr("PEMMECE_TLS_KEY", ""), "TLS private key path")
	allowInsecureWebhooks := flag.Bool("allow-insecure-webhooks", envBool("PEMMECE_ALLOW_INSECURE_WEBHOOKS"), "allow http webhook URLs")
	allowPrivateWebhooks := flag.Bool("allow-private-webhooks", envBool("PEMMECE_ALLOW_PRIVATE_WEBHOOKS"), "allow private/link-local webhook targets")
	publicURL := flag.String("public-url", envOr("PEMMECE_PUBLIC_URL", ""), "public base URL used in email notifications")
	emailNotifications := flag.Bool("email-notifications", envBool("PEMMECE_EMAIL_NOTIFICATIONS"), "enable email notification enqueueing and delivery")
	smtpHost := flag.String("smtp-host", envOr("PEMMECE_SMTP_HOST", ""), "SMTP host for email notifications")
	smtpPort := flag.Int("smtp-port", envInt("PEMMECE_SMTP_PORT", 0), "SMTP port for email notifications")
	smtpUser := flag.String("smtp-user", envOr("PEMMECE_SMTP_USER", ""), "SMTP username")
	smtpPassword := flag.String("smtp-password", envOr("PEMMECE_SMTP_PASSWORD", ""), "SMTP password")
	smtpFrom := flag.String("smtp-from", envOr("PEMMECE_SMTP_FROM", ""), "sender address for email notifications")
	smtpTLSMode := flag.String("smtp-tls-mode", envOr("PEMMECE_SMTP_TLS_MODE", "starttls"), "SMTP TLS mode: starttls, tls, or none")
	emailBatchDelay := flag.Duration("email-batch-delay", envDuration("PEMMECE_EMAIL_BATCH_DELAY", 20*time.Second), "delay before sending coalesced ticket notification emails")
	sessionTTL := flag.Duration("session-ttl", envDuration("PEMMECE_SESSION_TTL", 14*24*time.Hour), "browser session lifetime")
	loginRateLimit := flag.Int("login-rate-limit", envInt("PEMMECE_LOGIN_RATE_LIMIT", 10), "login attempts allowed per rate window and user/IP")
	loginRateWindow := flag.Duration("login-rate-window", envDuration("PEMMECE_LOGIN_RATE_WINDOW", time.Minute), "login rate limit window")
	accountLinkRateLimit := flag.Int("account-link-rate-limit", envInt("PEMMECE_ACCOUNT_LINK_RATE_LIMIT", 10), "account link attempts allowed per rate window and token/IP")
	accountLinkRateWindow := flag.Duration("account-link-rate-window", envDuration("PEMMECE_ACCOUNT_LINK_RATE_WINDOW", time.Minute), "account link rate limit window")
	flag.Parse()

	tracker, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	smtpConfig := notify.SMTPConfig{
		Host:     *smtpHost,
		Port:     *smtpPort,
		Username: *smtpUser,
		Password: *smtpPassword,
		From:     *smtpFrom,
		TLSMode:  *smtpTLSMode,
	}
	emailEnabled := *emailNotifications || smtpConfig.Enabled()
	var mailer notify.Mailer
	if emailEnabled {
		var err error
		mailer, err = notify.NewSMTPMailer(smtpConfig)
		if err != nil {
			log.Fatalf("configure email notifications: %v", err)
		}
		worker := notify.Worker{
			Store:       tracker,
			Mailer:      mailer,
			From:        smtpConfig.From,
			Interval:    5 * time.Second,
			LeaseFor:    time.Minute,
			BatchSize:   10,
			MaxAttempts: 5,
			Logger:      log.Default(),
		}
		go worker.Run(ctx)
		log.Printf("email notifications enabled via SMTP host %s", smtpConfig.Host)
	}

	srv := &http.Server{
		Addr: *addr,
		Handler: server.New(tracker, server.Options{
			AllowInsecureWebhooks: *allowInsecureWebhooks,
			AllowPrivateWebhooks:  *allowPrivateWebhooks,
			EmailNotifications:    emailEnabled,
			EmailBatchDelay:       *emailBatchDelay,
			PublicURL:             *publicURL,
			SessionTTL:            *sessionTTL,
			LoginRateLimit:        server.RateLimit{Limit: *loginRateLimit, Window: *loginRateWindow},
			AccountLinkRateLimit:  server.RateLimit{Limit: *accountLinkRateLimit, Window: *accountLinkRateWindow},
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		if *tlsCert != "" || *tlsKey != "" {
			if *tlsCert == "" || *tlsKey == "" {
				errs <- http.ErrServerClosed
				log.Fatalf("both -tls-cert and -tls-key are required for HTTPS")
			}
			log.Printf("pemmece listening on https://%s", *addr)
			errs <- srv.ListenAndServeTLS(*tlsCert, *tlsKey)
			return
		}
		log.Printf("pemmece listening on http://%s (browser login requires HTTPS)", *addr)
		errs <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Printf("shutdown requested")
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		index := strings.Index(line, "=")
		if index < 1 {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNumber)
		}
		key := strings.TrimSpace(line[:index])
		value, err := parseDotEnvValue(strings.TrimSpace(line[index+1:]))
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, lineNumber)
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
	}
	return scanner.Err()
}

func parseDotEnvValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, `"`) {
		if !strings.HasSuffix(value, `"`) {
			return "", fmt.Errorf("unterminated quoted value")
		}
		return strconv.Unquote(value)
	}
	if strings.HasPrefix(value, `'`) {
		if !strings.HasSuffix(value, `'`) {
			return "", fmt.Errorf("unterminated quoted value")
		}
		return value[1 : len(value)-1], nil
	}
	return value, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
