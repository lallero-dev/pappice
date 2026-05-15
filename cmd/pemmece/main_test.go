package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvHelpers(t *testing.T) {
	t.Setenv("PEMMECE_TEST_VALUE", "configured")
	t.Setenv("PEMMECE_TEST_TRUE", "yes")
	t.Setenv("PEMMECE_TEST_FALSE", "off")
	t.Setenv("PEMMECE_TEST_INT", "42")
	t.Setenv("PEMMECE_TEST_BAD_INT", "nope")

	if got := envOr("PEMMECE_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("envOr configured = %q", got)
	}
	if got := envOr("PEMMECE_TEST_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("envOr fallback = %q", got)
	}
	if !envBool("PEMMECE_TEST_TRUE") || envBool("PEMMECE_TEST_FALSE") || envBool("PEMMECE_TEST_MISSING") {
		t.Fatal("envBool returned unexpected values")
	}
	if got := envInt("PEMMECE_TEST_INT", 7); got != 42 {
		t.Fatalf("envInt parsed = %d", got)
	}
	if got := envInt("PEMMECE_TEST_BAD_INT", 7); got != 7 {
		t.Fatalf("envInt bad fallback = %d", got)
	}
	if got := envInt("PEMMECE_TEST_MISSING", 9); got != 9 {
		t.Fatalf("envInt missing fallback = %d", got)
	}
}

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := `
# comments and blanks are ignored
PEMMECE_ENV_PLAIN=plain
PEMMECE_ENV_QUOTED="quoted value"
PEMMECE_ENV_SINGLE='single value'
export PEMMECE_ENV_EXPORTED=exported
PEMMECE_ENV_KEEP=file-value
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	t.Setenv("PEMMECE_ENV_KEEP", "external-value")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("load env: %v", err)
	}
	if got := os.Getenv("PEMMECE_ENV_PLAIN"); got != "plain" {
		t.Fatalf("plain = %q", got)
	}
	if got := os.Getenv("PEMMECE_ENV_QUOTED"); got != "quoted value" {
		t.Fatalf("quoted = %q", got)
	}
	if got := os.Getenv("PEMMECE_ENV_SINGLE"); got != "single value" {
		t.Fatalf("single = %q", got)
	}
	if got := os.Getenv("PEMMECE_ENV_EXPORTED"); got != "exported" {
		t.Fatalf("exported = %q", got)
	}
	if got := os.Getenv("PEMMECE_ENV_KEEP"); got != "external-value" {
		t.Fatalf("keep = %q", got)
	}
}
