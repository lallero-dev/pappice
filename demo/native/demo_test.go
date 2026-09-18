package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pappice/demo/internal/data"
	"pappice/internal/app"
	"pappice/internal/store"
)

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-h"}, &stdout, &stderr, func(app.Config, io.Writer) error {
		t.Fatal("help must not start the server")
		return nil
	})
	if code != 0 {
		t.Fatalf("help exit = %d: %s", code, stderr.String())
	}
	for _, flag := range []string{"-addr", "-debug-addr", "-keep"} {
		if !strings.Contains(stderr.String(), flag) {
			t.Fatalf("help missing %s: %s", flag, stderr.String())
		}
	}
}

func TestRunDirectoryLifecycle(t *testing.T) {
	for _, test := range []struct {
		name     string
		addr     string
		keep     bool
		serveErr error
	}{
		{name: "shutdown", addr: "127.0.0.2:9443"},
		{name: "startup failure", addr: "localhost:9443", serveErr: errors.New("listener unavailable")},
		{name: "keep", addr: "localhost:9443", keep: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			args := []string{"-addr", test.addr, "-debug-addr", "127.0.0.1:6060"}
			if test.keep {
				args = append(args, "-keep")
			}
			var stdout, stderr bytes.Buffer
			var dir string
			code := run(args, &stdout, &stderr, func(cfg app.Config, _ io.Writer) error {
				dir = filepath.Dir(cfg.DBPath)
				if cfg.Addr != test.addr || cfg.PublicURL != "https://"+test.addr || cfg.DebugAddr != "127.0.0.1:6060" {
					t.Fatalf("demo listener configuration = %#v", cfg)
				}
				pair, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
				if err != nil {
					t.Fatal(err)
				}
				cert, err := x509.ParseCertificate(pair.Certificate[0])
				if err != nil {
					t.Fatal(err)
				}
				host, _, _ := net.SplitHostPort(cfg.Addr)
				if err := cert.VerifyHostname(host); err != nil {
					t.Fatal(err)
				}
				tracker, err := store.Open(cfg.DBPath)
				if err != nil {
					t.Fatal(err)
				}
				defer tracker.Close()
				if _, err := tracker.Authenticate("admin@example.test", data.Password); err != nil {
					t.Fatal(err)
				}
				return test.serveErr
			})
			wantCode := 0
			if test.serveErr != nil {
				wantCode = 1
				if !strings.Contains(stderr.String(), test.serveErr.Error()) {
					t.Fatalf("missing startup error: %s", stderr.String())
				}
			}
			if code != wantCode || dir == "" {
				t.Fatalf("Run = %d, directory %q, stderr: %s", code, dir, stderr.String())
			}
			_, err := os.Stat(dir)
			if test.keep && err != nil || !test.keep && !os.IsNotExist(err) {
				t.Fatalf("demo directory after Run (keep=%v): %v", test.keep, err)
			}
			if !strings.Contains(stdout.String(), "admin@example.test / "+data.Password) ||
				strings.Contains(stdout.String(), "Demo data:") != test.keep {
				t.Fatalf("demo summary: %s", stdout.String())
			}
		})
	}
}

func TestDemoURL(t *testing.T) {
	if got, err := demoURL(":8388"); err != nil || got != "https://127.0.0.1:8388" {
		t.Fatalf("demoURL(:8388) = %q, %v", got, err)
	}
	if got, err := demoURL("localhost:9443"); err != nil || got != "https://localhost:9443" {
		t.Fatalf("demoURL(localhost:9443) = %q, %v", got, err)
	}
	if _, err := demoURL(":0"); err == nil {
		t.Fatal("demoURL(:0) should fail")
	}
}

func TestSeedDemoStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pappice.db")
	seed, err := seedDemoStore(dbPath)
	if err != nil {
		t.Fatalf("seed demo store: %v", err)
	}
	if seed.Admin != "admin@example.test" || seed.Staff != "staff@example.test" ||
		seed.Customer != "customer@example.test" || seed.Password != data.Password {
		t.Fatalf("seed summary = %#v", seed)
	}

	tracker, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open seeded store: %v", err)
	}
	defer tracker.Close()

	admin, err := tracker.Authenticate(seed.Admin, seed.Password)
	if err != nil {
		t.Fatalf("authenticate demo admin: %v", err)
	}
	customer, err := tracker.Authenticate(seed.Customer, seed.Password)
	if err != nil {
		t.Fatalf("authenticate demo customer: %v", err)
	}
	products, err := tracker.ListProducts(customer)
	if err != nil {
		t.Fatalf("list customer products: %v", err)
	}
	if len(products) != 1 || products[0].Key != "WEB" || products[0].Name != "Website Support" {
		t.Fatalf("customer products = %#v", products)
	}
	page, err := tracker.ListTicketSummariesPage(admin, store.TicketSummaryFilter{})
	if err != nil {
		t.Fatalf("list seeded tickets: %v", err)
	}
	if len(page.Tickets) < 2 {
		t.Fatalf("seeded tickets = %#v", page.Tickets)
	}
}
