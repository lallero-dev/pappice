package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func runHealthcheck(args []string, stdout, stderr io.Writer) int {
	cfg, code, ok := parseHealthcheckConfig(args, stderr)
	if !ok {
		return code
	}
	useTLS, err := cfg.tlsEnabled()
	if err != nil {
		fmt.Fprintf(stderr, "pappice healthcheck: %v\n", err)
		return 1
	}
	rawURL, err := healthcheckURL(cfg.Addr, useTLS)
	if err != nil {
		fmt.Fprintf(stderr, "pappice healthcheck: %v\n", err)
		return 1
	}
	client := healthcheckClient(strings.HasPrefix(rawURL, "https://"))
	resp, err := client.Get(rawURL)
	if err != nil {
		fmt.Fprintf(stderr, "pappice healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "pappice healthcheck: unexpected status %s\n", resp.Status)
		return 1
	}
	fmt.Fprintln(stdout, "OK")
	return 0
}

func parseHealthcheckConfig(args []string, output io.Writer) (appConfig, int, bool) {
	cfg := defaultAppConfig()
	fs := newConfigFlagSet("pappice healthcheck", &cfg, output)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: pappice healthcheck [flags]")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Checks the local /api/health endpoint. HTTPS checks skip certificate")
		fmt.Fprintln(fs.Output(), "verification because this command is intended for local container probes.")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cfg, 0, false
		}
		return cfg, 2, false
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(output, "pappice healthcheck: unexpected argument %q\n", fs.Arg(0))
		return cfg, 2, false
	}
	if err := loadDotEnv(".env"); err != nil {
		fmt.Fprintf(output, "load .env: %v\n", err)
		return cfg, 1, false
	}
	applyEnv(&cfg, visitedFlags(fs))
	return cfg, 0, true
}

func healthcheckClient(skipTLSVerify bool) *http.Client {
	client := &http.Client{Timeout: 5 * time.Second}
	if skipTLSVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Local probe only.
		}
	}
	return client
}

func healthcheckURL(addr string, https bool) (string, error) {
	host, port, err := splitHealthcheckAddr(addr)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if https {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port) + "/api/health", nil
}

func splitHealthcheckAddr(addr string) (string, string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", "", errors.New("listen address is required")
	}
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		return strings.Trim(host, "[]"), port, nil
	}
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1", strings.TrimPrefix(addr, ":"), nil
	}
	return "", "", fmt.Errorf("invalid listen address %q", addr)
}
