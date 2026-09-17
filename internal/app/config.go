package app

import (
	"errors"
	"strings"
	"time"

	"pappice/internal/notify"
	"pappice/internal/server"
)

type Config struct {
	Version               string
	Addr                  string
	DebugAddr             string
	DBPath                string
	TLSCert               string
	TLSKey                string
	TrustProxyHeaders     bool
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

func DefaultConfig() Config {
	return Config{
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

func (cfg Config) SMTPConfig() notify.SMTPConfig {
	return notify.SMTPConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		TLSMode:  cfg.SMTPTLSMode,
	}
}

func (cfg Config) EmailEnabled() bool {
	return cfg.EmailNotifications || cfg.SMTPConfig().Enabled()
}

func (cfg Config) TLSEnabled() (bool, error) {
	switch {
	case cfg.TLSCert == "" && cfg.TLSKey == "":
		return false, nil
	case cfg.TLSCert == "" || cfg.TLSKey == "":
		return false, errors.New("both -tls-cert and -tls-key are required for HTTPS")
	default:
		return true, nil
	}
}

func (cfg Config) serverOptions(emailEnabled bool) server.Options {
	return server.Options{
		AllowInsecureWebhooks: cfg.AllowInsecureWebhooks,
		AllowPrivateWebhooks:  cfg.AllowPrivateWebhooks,
		TrustProxyHeaders:     cfg.TrustProxyHeaders,
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
		Version:              cfg.Version,
		UploadDir:            cfg.UploadDir,
		BackupDir:            cfg.BackupDir,
		MaxUploadSize:        cfg.MaxUploadSize,
		MaxUploadFiles:       cfg.MaxUploadFiles,
		AllowedUploadTypes:   splitCSV(cfg.AllowedUploadTypes),
		LoginRateLimit:       server.RateLimit{Limit: cfg.LoginRateLimit, Window: cfg.LoginRateWindow},
		AccountLinkRateLimit: server.RateLimit{Limit: cfg.AccountLinkRateLimit, Window: cfg.AccountLinkRateWindow},
	}
}

func splitCSV(value string) []string {
	result := make([]string, 0, strings.Count(value, ",")+1)
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
