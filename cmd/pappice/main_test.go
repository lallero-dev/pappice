package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvHelpers(t *testing.T) {
	t.Setenv("PAPPICE_TEST_VALUE", "configured")
	t.Setenv("PAPPICE_TEST_TRUE", "yes")
	t.Setenv("PAPPICE_TEST_FALSE", "off")
	t.Setenv("PAPPICE_TEST_INT", "42")
	t.Setenv("PAPPICE_TEST_BAD_INT", "nope")
	t.Setenv("PAPPICE_TEST_DURATION", "1500ms")
	t.Setenv("PAPPICE_TEST_BAD_DURATION", "later")

	if got := envOr("PAPPICE_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("envOr configured = %q", got)
	}
	if got := envOr("PAPPICE_TEST_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("envOr fallback = %q", got)
	}
	if !envBool("PAPPICE_TEST_TRUE") || envBool("PAPPICE_TEST_FALSE") || envBool("PAPPICE_TEST_MISSING") {
		t.Fatal("envBool returned unexpected values")
	}
	if got := envInt("PAPPICE_TEST_INT", 7); got != 42 {
		t.Fatalf("envInt parsed = %d", got)
	}
	if got := envInt("PAPPICE_TEST_BAD_INT", 7); got != 7 {
		t.Fatalf("envInt bad fallback = %d", got)
	}
	if got := envInt("PAPPICE_TEST_MISSING", 9); got != 9 {
		t.Fatalf("envInt missing fallback = %d", got)
	}
	if got := envInt64("PAPPICE_TEST_INT", 7); got != 42 {
		t.Fatalf("envInt64 parsed = %d", got)
	}
	if got := envDuration("PAPPICE_TEST_DURATION", time.Second); got != 1500*time.Millisecond {
		t.Fatalf("envDuration parsed = %s", got)
	}
	if got := envDuration("PAPPICE_TEST_BAD_DURATION", time.Second); got != time.Second {
		t.Fatalf("envDuration bad fallback = %s", got)
	}
	if got := envDuration("PAPPICE_TEST_MISSING_DURATION", 2*time.Second); got != 2*time.Second {
		t.Fatalf("envDuration missing fallback = %s", got)
	}
}

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := `
# comments and blanks are ignored
PAPPICE_ENV_PLAIN=plain
PAPPICE_ENV_QUOTED="quoted value"
PAPPICE_ENV_SINGLE='single value'
export PAPPICE_ENV_EXPORTED=exported
PAPPICE_ENV_KEEP=file-value
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	t.Setenv("PAPPICE_ENV_KEEP", "external-value")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("load env: %v", err)
	}
	if got := os.Getenv("PAPPICE_ENV_PLAIN"); got != "plain" {
		t.Fatalf("plain = %q", got)
	}
	if got := os.Getenv("PAPPICE_ENV_QUOTED"); got != "quoted value" {
		t.Fatalf("quoted = %q", got)
	}
	if got := os.Getenv("PAPPICE_ENV_SINGLE"); got != "single value" {
		t.Fatalf("single = %q", got)
	}
	if got := os.Getenv("PAPPICE_ENV_EXPORTED"); got != "exported" {
		t.Fatalf("exported = %q", got)
	}
	if got := os.Getenv("PAPPICE_ENV_KEEP"); got != "external-value" {
		t.Fatalf("keep = %q", got)
	}
}

func TestLoadDotEnvValidation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".env")
	if err := loadDotEnv(missing); err != nil {
		t.Fatalf("missing .env should be ignored: %v", err)
	}

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("BROKEN\n"), 0o600); err != nil {
		t.Fatalf("write invalid env: %v", err)
	}
	if err := loadDotEnv(path); err == nil {
		t.Fatal("invalid env line should fail")
	}

	if err := os.WriteFile(path, []byte("PAPPICE_ENV_BAD=\"unterminated\n"), 0o600); err != nil {
		t.Fatalf("write unterminated env: %v", err)
	}
	if err := loadDotEnv(path); err == nil {
		t.Fatal("unterminated quoted value should fail")
	}
}

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		args        []string
		wantCommand string
		wantArgs    []string
	}{
		{[]string{"pappice"}, "help", nil},
		{[]string{"pappice", "serve", "-addr", ":8080"}, "serve", []string{"-addr", ":8080"}},
		{[]string{"pappice", "-addr", ":8080"}, "-addr", []string{":8080"}},
		{[]string{"pappice", "doctor"}, "doctor", nil},
		{[]string{"pappice", "version"}, "version", nil},
		{[]string{"pappice", "-h"}, "help", nil},
	}
	for _, tt := range tests {
		command, args := splitCommand(tt.args)
		if command != tt.wantCommand || strings.Join(args, "\x00") != strings.Join(tt.wantArgs, "\x00") {
			t.Fatalf("splitCommand(%v) = %q %v, want %q %v", tt.args, command, args, tt.wantCommand, tt.wantArgs)
		}
	}
}

func TestRootFlagsAreNotServeAliases(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"pappice", "-addr", ":8080"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("flat flags exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "-addr"`) {
		t.Fatalf("flat flags did not report unknown command: %s", stderr.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"pappice", "version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("version exit = %d stderr=%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "pappice "+version {
		t.Fatalf("version output = %q", got)
	}
}

func TestHelpDoesNotExposeEnvironmentSecrets(t *testing.T) {
	t.Setenv("PAPPICE_SMTP_PASSWORD", "super-secret-password")
	t.Setenv("PAPPICE_SMTP_USER", "secret-user")
	t.Setenv("PAPPICE_SMTP_HOST", "secret-host")

	var stdout, stderr bytes.Buffer
	code := run([]string{"pappice", "serve", "-h"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	output := stdout.String() + stderr.String()
	for _, secret := range []string{"super-secret-password", "secret-user", "secret-host"} {
		if strings.Contains(output, secret) {
			t.Fatalf("help output leaked %q:\n%s", secret, output)
		}
	}
	if !strings.Contains(output, "-smtp-password") {
		t.Fatalf("help output missing smtp password flag:\n%s", output)
	}
}

func TestParseRuntimeConfigAppliesEnvAfterFlags(t *testing.T) {
	t.Setenv("PAPPICE_ADDR", "127.0.0.1:9000")
	t.Setenv("PAPPICE_DB", "env.db")
	t.Setenv("PAPPICE_SMTP_PASSWORD", "configured-secret")
	t.Setenv("PAPPICE_NOTIFICATION_DELAY", "45s")
	t.Setenv("PAPPICE_DOMAIN_EVENT_RETENTION", "720h")

	var output bytes.Buffer
	cfg, code, ok := parseRuntimeConfig("pappice serve", []string{"-addr", "127.0.0.1:9999"}, &output)
	if !ok || code != 0 {
		t.Fatalf("parse config ok=%v code=%d output=%s", ok, code, output.String())
	}
	if cfg.Addr != "127.0.0.1:9999" {
		t.Fatalf("flag addr was not preserved: %q", cfg.Addr)
	}
	if cfg.DBPath != "env.db" {
		t.Fatalf("env db was not applied: %q", cfg.DBPath)
	}
	if cfg.SMTPPassword != "configured-secret" {
		t.Fatalf("env smtp password was not applied")
	}
	if cfg.NotificationDelay != 45*time.Second {
		t.Fatalf("env notification delay = %s", cfg.NotificationDelay)
	}
	if cfg.DomainEventRetention != 720*time.Hour {
		t.Fatalf("env domain event retention = %s", cfg.DomainEventRetention)
	}
}

func TestDoctorCommand(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pappice.db")
	uploads := filepath.Join(dir, "uploads")
	backups := filepath.Join(dir, "backups")
	t.Setenv("PAPPICE_EMAIL_NOTIFICATIONS", "false")
	t.Setenv("PAPPICE_SMTP_HOST", "")
	t.Setenv("PAPPICE_SMTP_FROM", "")
	t.Setenv("PAPPICE_PUBLIC_URL", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"pappice",
		"doctor",
		"-db", dbPath,
		"-upload-dir", uploads,
		"-backup-dir", backups,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Pappice doctor") || !strings.Contains(output, "0 error(s)") {
		t.Fatalf("doctor output = %s", output)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"pappice",
		"doctor",
		"-db", dbPath,
		"-upload-dir", uploads,
		"-backup-dir", backups,
		"-email-notifications",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("doctor should fail with enabled email and no SMTP config: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "ERROR email") {
		t.Fatalf("doctor missing email error: %s", stdout.String())
	}
}
