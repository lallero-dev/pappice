package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"pappice/internal/notify"
	"pappice/internal/server"
)

type appConfig struct {
	Addr                  string
	DBPath                string
	TLSCert               string
	TLSKey                string
	AllowInsecureWebhooks bool
	AllowPrivateWebhooks  bool
	PublicURL             string
	BrandName             string
	BrandSubtitle         string
	BrandMark             string
	BrandColor            string
	DomainEventRetention  time.Duration
	EmailNotifications    bool
	SMTPHost              string
	SMTPPort              int
	SMTPUser              string
	SMTPPassword          string
	SMTPFrom              string
	SMTPTLSMode           string
	NotificationDelay     time.Duration
	SessionTTL            time.Duration
	UploadDir             string
	BackupDir             string
	MaxUploadSize         int64
	MaxUploadFiles        int
	AllowedUploadTypes    string
	LoginRateLimit        int
	LoginRateWindow       time.Duration
	AccountLinkRateLimit  int
	AccountLinkRateWindow time.Duration
}

func parseRuntimeConfig(name string, args []string, output io.Writer) (appConfig, int, bool) {
	cfg := defaultAppConfig()
	fs := newConfigFlagSet(name, &cfg, output)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cfg, 0, false
		}
		return cfg, 2, false
	}
	if err := loadDotEnv(".env"); err != nil {
		fmt.Fprintf(output, "load .env: %v\n", err)
		return cfg, 1, false
	}
	applyEnv(&cfg, visitedFlags(fs))
	return cfg, 0, true
}

func defaultAppConfig() appConfig {
	return appConfig{
		Addr:                  "127.0.0.1:8388",
		DBPath:                "pappice.db",
		SMTPTLSMode:           "starttls",
		DomainEventRetention:  30 * 24 * time.Hour,
		NotificationDelay:     30 * time.Second,
		SessionTTL:            14 * 24 * time.Hour,
		UploadDir:             "pappice-uploads",
		BackupDir:             "pappice-backups",
		MaxUploadSize:         10 << 20,
		MaxUploadFiles:        5,
		LoginRateLimit:        10,
		LoginRateWindow:       time.Minute,
		AccountLinkRateLimit:  10,
		AccountLinkRateWindow: time.Minute,
	}
}

func newConfigFlagSet(name string, cfg *appConfig, output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [flags]\n\nFlags:\n", name)
		fs.PrintDefaults()
	}
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.StringVar(&cfg.DBPath, "db", cfg.DBPath, "path to SQLite database file")
	fs.StringVar(&cfg.TLSCert, "tls-cert", cfg.TLSCert, "TLS certificate path")
	fs.StringVar(&cfg.TLSKey, "tls-key", cfg.TLSKey, "TLS private key path")
	fs.BoolVar(&cfg.AllowInsecureWebhooks, "allow-insecure-webhooks", cfg.AllowInsecureWebhooks, "allow http webhook URLs")
	fs.BoolVar(&cfg.AllowPrivateWebhooks, "allow-private-webhooks", cfg.AllowPrivateWebhooks, "allow private/link-local webhook targets")
	fs.StringVar(&cfg.PublicURL, "public-url", cfg.PublicURL, "public base URL used in email notifications")
	fs.StringVar(&cfg.BrandName, "brand-name", cfg.BrandName, "display name for this Pappice instance")
	fs.StringVar(&cfg.BrandSubtitle, "brand-subtitle", cfg.BrandSubtitle, "short subtitle shown under the brand name")
	fs.StringVar(&cfg.BrandMark, "brand-mark", cfg.BrandMark, "short mark shown in the header")
	fs.StringVar(&cfg.BrandColor, "brand-color", cfg.BrandColor, "hex color for the brand mark")
	fs.DurationVar(&cfg.DomainEventRetention, "domain-event-retention", cfg.DomainEventRetention, "processed domain event retention; set 0 to disable pruning")
	fs.BoolVar(&cfg.EmailNotifications, "email-notifications", cfg.EmailNotifications, "enable email notification enqueueing and delivery")
	fs.StringVar(&cfg.SMTPHost, "smtp-host", cfg.SMTPHost, "SMTP host for email notifications")
	fs.IntVar(&cfg.SMTPPort, "smtp-port", cfg.SMTPPort, "SMTP port for email notifications")
	fs.StringVar(&cfg.SMTPUser, "smtp-user", cfg.SMTPUser, "SMTP username")
	fs.StringVar(&cfg.SMTPPassword, "smtp-password", cfg.SMTPPassword, "SMTP password")
	fs.StringVar(&cfg.SMTPFrom, "smtp-from", cfg.SMTPFrom, "sender address for email notifications")
	fs.StringVar(&cfg.SMTPTLSMode, "smtp-tls-mode", cfg.SMTPTLSMode, "SMTP TLS mode: starttls, tls, or none")
	fs.DurationVar(&cfg.NotificationDelay, "notification-delay", cfg.NotificationDelay, "delay before sending ticket notifications through email and webhooks")
	fs.DurationVar(&cfg.SessionTTL, "session-ttl", cfg.SessionTTL, "browser session lifetime")
	fs.StringVar(&cfg.UploadDir, "upload-dir", cfg.UploadDir, "directory for ticket attachment files")
	fs.StringVar(&cfg.BackupDir, "backup-dir", cfg.BackupDir, "directory where backup snapshots are stored")
	fs.Int64Var(&cfg.MaxUploadSize, "max-upload-size", cfg.MaxUploadSize, "maximum bytes per attachment")
	fs.IntVar(&cfg.MaxUploadFiles, "max-upload-files", cfg.MaxUploadFiles, "maximum files per upload request")
	fs.StringVar(&cfg.AllowedUploadTypes, "allowed-upload-types", cfg.AllowedUploadTypes, "comma-separated allowed attachment MIME types")
	fs.IntVar(&cfg.LoginRateLimit, "login-rate-limit", cfg.LoginRateLimit, "login attempts allowed per rate window and user/IP")
	fs.DurationVar(&cfg.LoginRateWindow, "login-rate-window", cfg.LoginRateWindow, "login rate limit window")
	fs.IntVar(&cfg.AccountLinkRateLimit, "account-link-rate-limit", cfg.AccountLinkRateLimit, "account link attempts allowed per rate window and token/IP")
	fs.DurationVar(&cfg.AccountLinkRateWindow, "account-link-rate-window", cfg.AccountLinkRateWindow, "account link rate limit window")
	return fs
}

func visitedFlags(fs *flag.FlagSet) map[string]bool {
	visited := map[string]bool{}
	fs.Visit(func(item *flag.Flag) {
		visited[item.Name] = true
	})
	return visited
}

func applyEnv(cfg *appConfig, flags map[string]bool) {
	if !flags["addr"] {
		cfg.Addr = envOr("PAPPICE_ADDR", cfg.Addr)
	}
	if !flags["db"] {
		cfg.DBPath = envOr("PAPPICE_DB", cfg.DBPath)
	}
	if !flags["tls-cert"] {
		cfg.TLSCert = envOr("PAPPICE_TLS_CERT", cfg.TLSCert)
	}
	if !flags["tls-key"] {
		cfg.TLSKey = envOr("PAPPICE_TLS_KEY", cfg.TLSKey)
	}
	if !flags["allow-insecure-webhooks"] {
		cfg.AllowInsecureWebhooks = envBoolOr("PAPPICE_ALLOW_INSECURE_WEBHOOKS", cfg.AllowInsecureWebhooks)
	}
	if !flags["allow-private-webhooks"] {
		cfg.AllowPrivateWebhooks = envBoolOr("PAPPICE_ALLOW_PRIVATE_WEBHOOKS", cfg.AllowPrivateWebhooks)
	}
	if !flags["public-url"] {
		cfg.PublicURL = envOr("PAPPICE_PUBLIC_URL", cfg.PublicURL)
	}
	if !flags["brand-name"] {
		cfg.BrandName = envOr("PAPPICE_BRAND_NAME", cfg.BrandName)
	}
	if !flags["brand-subtitle"] {
		cfg.BrandSubtitle = envOr("PAPPICE_BRAND_SUBTITLE", cfg.BrandSubtitle)
	}
	if !flags["brand-mark"] {
		cfg.BrandMark = envOr("PAPPICE_BRAND_MARK", cfg.BrandMark)
	}
	if !flags["brand-color"] {
		cfg.BrandColor = envOr("PAPPICE_BRAND_COLOR", cfg.BrandColor)
	}
	if !flags["domain-event-retention"] {
		cfg.DomainEventRetention = envDuration("PAPPICE_DOMAIN_EVENT_RETENTION", cfg.DomainEventRetention)
	}
	if !flags["email-notifications"] {
		cfg.EmailNotifications = envBoolOr("PAPPICE_EMAIL_NOTIFICATIONS", cfg.EmailNotifications)
	}
	if !flags["smtp-host"] {
		cfg.SMTPHost = envOr("PAPPICE_SMTP_HOST", cfg.SMTPHost)
	}
	if !flags["smtp-port"] {
		cfg.SMTPPort = envInt("PAPPICE_SMTP_PORT", cfg.SMTPPort)
	}
	if !flags["smtp-user"] {
		cfg.SMTPUser = envOr("PAPPICE_SMTP_USER", cfg.SMTPUser)
	}
	if !flags["smtp-password"] {
		cfg.SMTPPassword = envOr("PAPPICE_SMTP_PASSWORD", cfg.SMTPPassword)
	}
	if !flags["smtp-from"] {
		cfg.SMTPFrom = envOr("PAPPICE_SMTP_FROM", cfg.SMTPFrom)
	}
	if !flags["smtp-tls-mode"] {
		cfg.SMTPTLSMode = envOr("PAPPICE_SMTP_TLS_MODE", cfg.SMTPTLSMode)
	}
	if !flags["notification-delay"] {
		cfg.NotificationDelay = envDuration("PAPPICE_NOTIFICATION_DELAY", cfg.NotificationDelay)
	}
	if !flags["session-ttl"] {
		cfg.SessionTTL = envDuration("PAPPICE_SESSION_TTL", cfg.SessionTTL)
	}
	if !flags["upload-dir"] {
		cfg.UploadDir = envOr("PAPPICE_UPLOAD_DIR", cfg.UploadDir)
	}
	if !flags["backup-dir"] {
		cfg.BackupDir = envOr("PAPPICE_BACKUP_DIR", cfg.BackupDir)
	}
	if !flags["max-upload-size"] {
		cfg.MaxUploadSize = envInt64("PAPPICE_MAX_UPLOAD_SIZE", cfg.MaxUploadSize)
	}
	if !flags["max-upload-files"] {
		cfg.MaxUploadFiles = envInt("PAPPICE_MAX_UPLOAD_FILES", cfg.MaxUploadFiles)
	}
	if !flags["allowed-upload-types"] {
		cfg.AllowedUploadTypes = envOr("PAPPICE_ALLOWED_UPLOAD_TYPES", cfg.AllowedUploadTypes)
	}
	if !flags["login-rate-limit"] {
		cfg.LoginRateLimit = envInt("PAPPICE_LOGIN_RATE_LIMIT", cfg.LoginRateLimit)
	}
	if !flags["login-rate-window"] {
		cfg.LoginRateWindow = envDuration("PAPPICE_LOGIN_RATE_WINDOW", cfg.LoginRateWindow)
	}
	if !flags["account-link-rate-limit"] {
		cfg.AccountLinkRateLimit = envInt("PAPPICE_ACCOUNT_LINK_RATE_LIMIT", cfg.AccountLinkRateLimit)
	}
	if !flags["account-link-rate-window"] {
		cfg.AccountLinkRateWindow = envDuration("PAPPICE_ACCOUNT_LINK_RATE_WINDOW", cfg.AccountLinkRateWindow)
	}
}

func (cfg appConfig) smtpConfig() notify.SMTPConfig {
	return notify.SMTPConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		TLSMode:  cfg.SMTPTLSMode,
	}
}

func (cfg appConfig) emailEnabled() bool {
	return cfg.EmailNotifications || cfg.smtpConfig().Enabled()
}

func (cfg appConfig) serverOptions(emailEnabled bool) server.Options {
	return server.Options{
		AllowInsecureWebhooks: cfg.AllowInsecureWebhooks,
		AllowPrivateWebhooks:  cfg.AllowPrivateWebhooks,
		Branding: server.Branding{
			Name:     cfg.BrandName,
			Subtitle: cfg.BrandSubtitle,
			Mark:     cfg.BrandMark,
			Color:    cfg.BrandColor,
		},
		DomainEventRetention: cfg.DomainEventRetention,
		EmailNotifications:   emailEnabled,
		NotificationDelay:    cfg.NotificationDelay,
		PublicURL:            cfg.PublicURL,
		SessionTTL:           cfg.SessionTTL,
		Version:              version,
		UploadDir:            cfg.UploadDir,
		BackupDir:            cfg.BackupDir,
		MaxUploadSize:        cfg.MaxUploadSize,
		MaxUploadFiles:       cfg.MaxUploadFiles,
		AllowedUploadTypes:   splitCSV(cfg.AllowedUploadTypes),
		LoginRateLimit:       server.RateLimit{Limit: cfg.LoginRateLimit, Window: cfg.LoginRateWindow},
		AccountLinkRateLimit: server.RateLimit{Limit: cfg.AccountLinkRateLimit, Window: cfg.AccountLinkRateWindow},
	}
}
