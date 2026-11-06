package app

import (
	"testing"

	"pappice/internal/notify"
)

func TestConfigEmailReplies(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IMAP = notify.IMAPConfig{Address: "imap.example.test:993", Username: "user", Password: "secret", Mailbox: "INBOX", ReplyAddress: "reply@example.test", AuthservID: "mx.example.test"}
	if err := cfg.ValidateEmailReplies(); err == nil {
		t.Fatal("enabled replies without SMTP")
	}
	cfg.SMTPHost, cfg.SMTPFrom = "smtp.example.test", "support@example.test"
	if err := cfg.ValidateEmailReplies(); err != nil || !cfg.serverOptions(true).EmailReplies {
		t.Fatalf("valid configuration: %v", err)
	}
	cfg.IMAP.Address = ""
	if err := cfg.ValidateEmailReplies(); err != nil || cfg.serverOptions(true).EmailReplies {
		t.Fatalf("disabled configuration: %v", err)
	}
}

func TestConfigTLSEnabled(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantTLS bool
		wantErr bool
	}{
		{"plain http", Config{}, false, false},
		{"tls", Config{TLSCert: "cert.pem", TLSKey: "key.pem"}, true, false},
		{"missing key", Config{TLSCert: "cert.pem"}, false, true},
		{"missing cert", Config{TLSKey: "key.pem"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.TLSEnabled()
			if (err != nil) != tt.wantErr {
				t.Fatalf("TLSEnabled error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantTLS {
				t.Fatalf("TLSEnabled() = %v, want %v", got, tt.wantTLS)
			}
		})
	}
}
