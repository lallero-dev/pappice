package main

import (
	"os"
	"path/filepath"
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
