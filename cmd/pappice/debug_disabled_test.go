//go:build !debug

package main

import "testing"

func TestDebugDisabledRejectsConfiguredAddr(t *testing.T) {
	if srv, err := startDebugServer("", nil, nil); err != nil || srv != nil {
		t.Fatalf("disabled debug server without address = %#v, %v; want nil, nil", srv, err)
	}
	if _, err := startDebugServer("127.0.0.1:8390", nil, nil); err == nil {
		t.Fatal("configured debug address should require a debug build")
	}
}
