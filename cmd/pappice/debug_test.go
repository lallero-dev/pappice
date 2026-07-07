//go:build debug

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDebugAddrValidation(t *testing.T) {
	for _, addr := range []string{"localhost:8390", "127.0.0.1:8390", "[::1]:8390"} {
		if err := validateDebugAddr(addr); err != nil {
			t.Fatalf("validateDebugAddr(%q): %v", addr, err)
		}
	}
	for _, addr := range []string{"", ":8390", "0.0.0.0:8390", "192.0.2.10:8390", "example.com:8390", "127.0.0.1"} {
		if err := validateDebugAddr(addr); err == nil {
			t.Fatalf("validateDebugAddr(%q) should fail", addr)
		}
	}
}

func TestDebugMuxServesPprof(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	response := httptest.NewRecorder()

	debugMux().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("pprof index status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "profile") {
		t.Fatalf("pprof index body did not look like pprof: %s", response.Body.String())
	}
}
