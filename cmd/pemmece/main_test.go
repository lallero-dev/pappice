package main

import (
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
