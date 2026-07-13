package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"pappice/internal/notify"
	"pappice/internal/store"
)

func runDoctor(args []string, stdout, stderr io.Writer) int {
	cfg, code, ok := parseRuntimeConfig("pappice doctor", args, stderr)
	if !ok {
		return code
	}
	report := doctorReport{out: stdout}
	report.run(cfg)
	if report.errors > 0 {
		return 1
	}
	return 0
}

type doctorReport struct {
	out      io.Writer
	errors   int
	warnings int
}

func (report *doctorReport) run(cfg appConfig) {
	fmt.Fprintln(report.out, "Pappice doctor")
	report.ok("version", version)
	report.checkDatabase(cfg.DBPath)
	report.checkSchema(cfg.DBPath)
	report.checkWritableDirectory("uploads", cfg.UploadDir)
	report.checkWritableDirectory("backups", cfg.BackupDir)
	report.checkTLS(cfg)
	report.checkProxyTrust(cfg)
	report.checkPublicURL(cfg)
	report.checkEmail(cfg)
	report.checkUploads(cfg)
	report.checkRateLimits(cfg)
	report.checkWebhookPolicy(cfg)
	fmt.Fprintf(report.out, "\nDoctor finished with %d error(s), %d warning(s).\n", report.errors, report.warnings)
}

func (report *doctorReport) checkProxyTrust(cfg appConfig) {
	if cfg.TrustProxyHeaders {
		report.warn("proxy", "trusting X-Forwarded-* headers; expose Pappice only behind a private reverse proxy")
		return
	}
	report.ok("proxy", "not trusting forwarded headers")
}

func (report *doctorReport) ok(label, message string) {
	fmt.Fprintf(report.out, "OK    %s: %s\n", label, message)
}

func (report *doctorReport) warn(label, message string) {
	report.warnings++
	fmt.Fprintf(report.out, "WARN  %s: %s\n", label, message)
}

func (report *doctorReport) err(label, message string) {
	report.errors++
	fmt.Fprintf(report.out, "ERROR %s: %s\n", label, message)
}

func (report *doctorReport) checkDatabase(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		report.err("database", "path is required")
		return
	}
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			report.err("database", "path points to a directory")
			return
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			report.err("database", err.Error())
			return
		}
		err = file.Close()
		if err != nil {
			report.err("database", fmt.Sprintf("could not close file: %v", err))
		}
		report.ok("database", path+" exists and is writable")
		return
	}
	if !os.IsNotExist(err) {
		report.err("database", err.Error())
		return
	}
	if err := checkWritableDir(filepath.Dir(path)); err != nil {
		report.err("database", "parent directory is not writable: "+err.Error())
		return
	}
	report.warn("database", path+" does not exist and will be created on first start")
}

func (report *doctorReport) checkSchema(path string) {
	status, err := store.InspectMigration(path)
	if err != nil {
		report.err("schema", err.Error())
		return
	}
	switch {
	case status.Empty:
		report.warn("schema", "database is empty; current schema will be installed on first start")
	case status.CurrentVersion > status.TargetVersion:
		report.err("schema", fmt.Sprintf("database is at version %d, app supports %d", status.CurrentVersion, status.TargetVersion))
	case len(status.Pending) > 0:
		report.err("schema", fmt.Sprintf("database migration required: %d pending; run pappice db migrate --dry-run, then pappice db migrate", len(status.Pending)))
	default:
		report.ok("schema", fmt.Sprintf("current (%d)", status.CurrentVersion))
	}
}

func (report *doctorReport) checkWritableDirectory(label, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		report.err(label, "path is required")
		return
	}
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			report.err(label, "path exists but is not a directory")
			return
		}
		if err := checkWritableDir(path); err != nil {
			report.err(label, err.Error())
			return
		}
		report.ok(label, path+" exists and is writable")
		return
	}
	if !os.IsNotExist(err) {
		report.err(label, err.Error())
		return
	}
	if err := checkWritableDir(filepath.Dir(path)); err != nil {
		report.err(label, "parent directory is not writable: "+err.Error())
		return
	}
	report.warn(label, path+" does not exist and will be created on first start")
}

func (report *doctorReport) checkTLS(cfg appConfig) {
	useTLS, err := cfg.tlsEnabled()
	if err != nil {
		report.err("tls", err.Error())
		return
	}
	if !useTLS {
		if cfg.TrustProxyHeaders {
			report.ok("tls", "terminated by trusted reverse proxy")
			return
		}
		report.warn("tls", "not configured; browser login requires HTTPS here or at a reverse proxy")
		return
	}
	certOK := report.checkReadableFile("tls-cert", cfg.TLSCert)
	keyOK := report.checkReadableFile("tls-key", cfg.TLSKey)
	if certOK && keyOK {
		report.ok("tls", "certificate and key are readable")
	}
}

func (report *doctorReport) checkReadableFile(label, path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		report.err(label, err.Error())
		return false
	}
	if info.IsDir() {
		report.err(label, "path points to a directory")
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		report.err(label, err.Error())
		return false
	}
	err = file.Close()
	if err != nil {
		report.err("database", fmt.Sprintf("could not close file: %v", err))
	}
	return true
}

func (report *doctorReport) checkPublicURL(cfg appConfig) {
	publicURL := strings.TrimSpace(cfg.PublicURL)
	if publicURL == "" {
		if cfg.emailEnabled() {
			report.err("public-url", "required when email notifications are enabled")
			return
		}
		report.warn("public-url", "not configured; email links will need this before notifications are enabled")
		return
	}
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		report.err("public-url", "must be an absolute HTTPS URL")
		return
	}
	if parsed.Scheme != "https" {
		report.err("public-url", "must use https")
		return
	}
	report.ok("public-url", publicURL)
}

func (report *doctorReport) checkEmail(cfg appConfig) {
	if !cfg.emailEnabled() {
		report.ok("email", "disabled")
		return
	}
	if _, err := notify.NewSMTPMailer(cfg.smtpConfig()); err != nil {
		report.err("email", err.Error())
		return
	}
	if strings.TrimSpace(cfg.SMTPUser) != "" && strings.TrimSpace(cfg.SMTPPassword) == "" {
		report.warn("email", "SMTP username is set but password is empty")
		return
	}
	report.ok("email", "SMTP configuration is valid")
}

func (report *doctorReport) checkUploads(cfg appConfig) {
	if cfg.MaxUploadSize <= 0 {
		report.err("uploads", "max upload size must be greater than zero")
	}
	if cfg.MaxUploadFiles <= 0 {
		report.err("uploads", "max upload files must be greater than zero")
	}
	if cfg.MaxUploadSize > 0 && cfg.MaxUploadFiles > 0 {
		report.ok("uploads", fmt.Sprintf("limit is %d bytes across %d file(s)", cfg.MaxUploadSize, cfg.MaxUploadFiles))
	}
}

func (report *doctorReport) checkRateLimits(cfg appConfig) {
	if cfg.LoginRateLimit <= 0 || cfg.LoginRateWindow <= 0 {
		report.warn("rate-limits", "login limiter will use built-in defaults")
	}
	if cfg.AccountLinkRateLimit <= 0 || cfg.AccountLinkRateWindow <= 0 {
		report.warn("rate-limits", "account-link limiter will use built-in defaults")
	}
	if cfg.LoginRateLimit > 0 && cfg.LoginRateWindow > 0 && cfg.AccountLinkRateLimit > 0 && cfg.AccountLinkRateWindow > 0 {
		report.ok("rate-limits", "configured")
	}
}

func (report *doctorReport) checkWebhookPolicy(cfg appConfig) {
	if cfg.AllowInsecureWebhooks {
		report.warn("webhooks", "insecure HTTP webhook URLs are enabled")
	}
	if cfg.AllowPrivateWebhooks {
		report.warn("webhooks", "private network webhook targets are enabled")
	}
	if !cfg.AllowInsecureWebhooks && !cfg.AllowPrivateWebhooks {
		report.ok("webhooks", "public HTTPS targets only")
	}
}

func checkWritableDir(path string) error {
	if path == "" {
		path = "."
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	file, err := os.CreateTemp(path, ".pappice-doctor-*")
	if err != nil {
		return err
	}
	name := file.Name()

	closeErr := file.Close()
	removeErr := os.Remove(name)

	if closeErr != nil {
		return fmt.Errorf("failed to close temp file: %w", closeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("failed to remove temp file: %w", removeErr)
	}
	return nil
}
