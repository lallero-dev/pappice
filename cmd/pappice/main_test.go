package main

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pappice/internal/store"

	_ "modernc.org/sqlite"
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
		{[]string{"pappice", "demo", "-addr", ":18443"}, "demo", []string{"-addr", ":18443"}},
		{[]string{"pappice", "serve", "-addr", ":8080"}, "serve", []string{"-addr", ":8080"}},
		{[]string{"pappice", "-addr", ":8080"}, "-addr", []string{":8080"}},
		{[]string{"pappice", "doctor"}, "doctor", nil},
		{[]string{"pappice", "backup"}, "backup", nil},
		{[]string{"pappice", "restore", "latest"}, "restore", []string{"latest"}},
		{[]string{"pappice", "healthcheck"}, "healthcheck", nil},
		{[]string{"pappice", "db", "status"}, "db", []string{"status"}},
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

func TestDemoHelpers(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "localhost.pem")
	keyPath := filepath.Join(dir, "localhost-key.pem")
	if err := writeDemoCertificate(certPath, keyPath, "127.0.0.1:8388"); err != nil {
		t.Fatalf("write demo cert: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("load generated cert: %v", err)
	}
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
		seed.Customer != "customer@example.test" || seed.Password != demoPassword {
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
	products := tracker.ListProducts(customer)
	if len(products) != 1 || products[0].Key != "WEB" || products[0].Name != "Website Support" {
		t.Fatalf("customer products = %#v", products)
	}
	tickets, err := tracker.ListTicketSummariesForUser(admin, store.TicketSummaryFilter{})
	if err != nil {
		t.Fatalf("list seeded tickets: %v", err)
	}
	if len(tickets) < 2 {
		t.Fatalf("seeded tickets = %#v", tickets)
	}
}

func TestDBCommandStatusAndMigrate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pappice.db")
	var stdout, stderr bytes.Buffer
	code := run([]string{"pappice", "db", "status", "-db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("db status exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Status: empty") {
		t.Fatalf("db status output = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"pappice", "db", "migrate", "-db", dbPath, "-dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("db migrate dry-run exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Dry-run succeeded") {
		t.Fatalf("db migrate dry-run output = %s", stdout.String())
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create source db, stat err = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"pappice", "db", "migrate", "-db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("db migrate exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Migration succeeded") || !strings.Contains(stdout.String(), "Initialized current schema") {
		t.Fatalf("db migrate output = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"pappice", "db", "status", "-db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("db status current exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Status: current") {
		t.Fatalf("db status current output = %s", stdout.String())
	}
}

func TestBackupAndRestoreCommands(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pappice.db")
	uploads := filepath.Join(dir, "uploads")
	backups := filepath.Join(dir, "backups")
	createCommandDB(t, dbPath, "before")
	writeCommandFile(t, filepath.Join(uploads, "tickets", "one.txt"), "attachment before")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"pappice",
		"backup",
		"-db", dbPath,
		"-upload-dir", uploads,
		"-backup-dir", backups,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("backup exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Backup created:") ||
		!strings.Contains(stdout.String(), "Restore with: pappice restore") {
		t.Fatalf("backup output = %s", stdout.String())
	}

	createCommandDB(t, dbPath, "after")
	writeCommandFile(t, filepath.Join(uploads, "stale.txt"), "stale upload")
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"pappice",
		"restore",
		"-yes",
		"-db", dbPath,
		"-upload-dir", uploads,
		"-backup-dir", backups,
		"latest",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("restore exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Restore complete from:") ||
		!strings.Contains(stdout.String(), "Previous files saved in:") {
		t.Fatalf("restore output = %s", stdout.String())
	}
	if got := queryCommandDB(t, dbPath); got != "before" {
		t.Fatalf("restored database value = %q", got)
	}
	if got := readCommandFile(t, filepath.Join(uploads, "tickets", "one.txt")); got != "attachment before" {
		t.Fatalf("restored upload = %q", got)
	}
	if _, err := os.Stat(filepath.Join(uploads, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale upload remained after restore: %v", err)
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

func TestHealthcheckURL(t *testing.T) {
	tests := []struct {
		name  string
		addr  string
		https bool
		want  string
	}{
		{"default http", "127.0.0.1:8388", false, "http://127.0.0.1:8388/api/health"},
		{"default https", "127.0.0.1:8388", true, "https://127.0.0.1:8388/api/health"},
		{"all interfaces", "0.0.0.0:8388", true, "https://127.0.0.1:8388/api/health"},
		{"port only", ":8388", false, "http://127.0.0.1:8388/api/health"},
		{"ipv6 all interfaces", "[::]:8388", true, "https://127.0.0.1:8388/api/health"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := healthcheckURL(tt.addr, tt.https)
			if err != nil {
				t.Fatalf("healthcheckURL returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("healthcheckURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigTLSEnabled(t *testing.T) {
	tests := []struct {
		name    string
		cfg     appConfig
		wantTLS bool
		wantErr bool
	}{
		{"plain http", appConfig{}, false, false},
		{"tls", appConfig{TLSCert: "cert.pem", TLSKey: "key.pem"}, true, false},
		{"missing key", appConfig{TLSCert: "cert.pem"}, false, true},
		{"missing cert", appConfig{TLSKey: "key.pem"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.tlsEnabled()
			if (err != nil) != tt.wantErr {
				t.Fatalf("tlsEnabled error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantTLS {
				t.Fatalf("tlsEnabled() = %v, want %v", got, tt.wantTLS)
			}
		})
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

func TestDemoHelpIncludesDebugAddr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"pappice", "demo", "-h"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("demo help exit = %d", code)
	}
	output := stdout.String() + stderr.String()
	if !strings.Contains(output, "-debug-addr") {
		t.Fatalf("demo help output missing debug flag:\n%s", output)
	}
}

func TestParseRuntimeConfigAppliesEnvAfterFlags(t *testing.T) {
	t.Setenv("PAPPICE_ADDR", "127.0.0.1:9000")
	t.Setenv("PAPPICE_DB", "env.db")
	t.Setenv("PAPPICE_SMTP_PASSWORD", "configured-secret")
	t.Setenv("PAPPICE_DEBUG_ADDR", "127.0.0.1:8390")
	t.Setenv("PAPPICE_NOTIFICATION_DELAY", "45s")
	t.Setenv("PAPPICE_DOMAIN_EVENT_RETENTION", "720h")
	t.Setenv("PAPPICE_TRUST_PROXY_HEADERS", "true")

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
	if cfg.DebugAddr != "127.0.0.1:8390" {
		t.Fatalf("env debug addr was not applied: %q", cfg.DebugAddr)
	}
	if cfg.NotificationDelay != 45*time.Second {
		t.Fatalf("env notification delay = %s", cfg.NotificationDelay)
	}
	if cfg.DomainEventRetention != 720*time.Hour {
		t.Fatalf("env domain event retention = %s", cfg.DomainEventRetention)
	}
	if !cfg.TrustProxyHeaders {
		t.Fatalf("env trust proxy headers was not applied")
	}
}

func TestRuntimeCommandsRejectUnexpectedArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"serve", []string{"pappice", "serve", "extra"}},
		{"doctor", []string{"pappice", "doctor", "extra"}},
		{"healthcheck", []string{"pappice", "healthcheck", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "unexpected argument") {
				t.Fatalf("stderr did not report unexpected argument: %s", stderr.String())
			}
		})
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
	if !strings.Contains(output, "WARN  schema: database is empty") {
		t.Fatalf("doctor missing empty schema warning: %s", output)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"pappice",
		"doctor",
		"-db", dbPath,
		"-upload-dir", uploads,
		"-backup-dir", backups,
		"-trust-proxy-headers",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor proxy exit = %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK    tls: terminated by trusted reverse proxy") {
		t.Fatalf("doctor proxy output = %s", stdout.String())
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

	legacyDBPath := filepath.Join(dir, "legacy.db")
	legacyDB, err := sql.Open("sqlite", legacyDBPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacyDB.Exec(`CREATE TABLE legacy_marker (id INTEGER PRIMARY KEY)`); err != nil {
		_ = legacyDB.Close()
		t.Fatalf("create legacy db: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"pappice",
		"doctor",
		"-db", legacyDBPath,
		"-upload-dir", uploads,
		"-backup-dir", backups,
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("doctor should fail when migrations are pending: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "ERROR schema: database migration required") {
		t.Fatalf("doctor missing migration error: %s", stdout.String())
	}
}

func createCommandDB(t *testing.T, path, value string) {
	t.Helper()
	for _, item := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(item); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove command db file: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create command db dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open command db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records (value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create command records: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO records (value) VALUES (?)`, value); err != nil {
		t.Fatalf("insert command record: %v", err)
	}
}

func queryCommandDB(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open command query db: %v", err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM records`).Scan(&value); err != nil {
		t.Fatalf("query command record: %v", err)
	}
	return value
}

func writeCommandFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create command file dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write command file: %v", err)
	}
}

func readCommandFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read command file: %v", err)
	}
	return string(data)
}
