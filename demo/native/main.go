package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"pappice/demo/internal/data"
	"pappice/internal/app"
	"pappice/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, app.Serve))
}

// run owns the demo directory and invokes serve with a populated database and
// local TLS certificate. The caller supplies the regular application server.
func run(args []string, stdout, stderr io.Writer, serve func(app.Config, io.Writer) error) int {
	fs := flag.NewFlagSet("pappice-demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "127.0.0.1:8388", "HTTPS listen address")
	debugAddr := fs.String("debug-addr", "", "optional local-only pprof listen address; requires a debug build")
	keep := fs.Bool("keep", false, "keep the temporary demo directory after shutdown")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: go run ./demo/native [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "pappice-demo: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	publicURL, err := demoURL(*addr)
	if err != nil {
		fmt.Fprintf(stderr, "pappice-demo: %v\n", err)
		return 2
	}

	dir, err := os.MkdirTemp("", "pappice-demo-*")
	if err != nil {
		fmt.Fprintf(stderr, "pappice-demo: create temp dir: %v\n", err)
		return 1
	}
	if !*keep {
		defer os.RemoveAll(dir)
	}

	certPath := filepath.Join(dir, "localhost.pem")
	keyPath := filepath.Join(dir, "localhost-key.pem")
	if err := writeDemoCertificate(certPath, keyPath, *addr); err != nil {
		fmt.Fprintf(stderr, "pappice-demo: create TLS certificate: %v\n", err)
		return 1
	}

	cfg := app.DefaultConfig()
	cfg.Addr = *addr
	cfg.DebugAddr = *debugAddr
	cfg.DBPath = filepath.Join(dir, "pappice.db")
	cfg.UploadDir = filepath.Join(dir, "uploads")
	cfg.BackupDir = filepath.Join(dir, "backups")
	cfg.TLSCert = certPath
	cfg.TLSKey = keyPath
	cfg.PublicURL = publicURL
	cfg.BrandSubtitle = "demo support desk"
	cfg.NotificationDelay = 2 * time.Second

	seed, err := seedDemoStore(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(stderr, "pappice-demo: seed data: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Pappice demo: %s\n\n", cfg.PublicURL)
	fmt.Fprintln(stdout, "Accounts:")
	fmt.Fprintf(stdout, "  Admin:    %s / %s\n", seed.Admin, seed.Password)
	fmt.Fprintf(stdout, "  Staff:    %s / %s\n", seed.Staff, seed.Password)
	fmt.Fprintf(stdout, "  Customer: %s / %s\n", seed.Customer, seed.Password)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "The browser will show a warning because the demo uses a local self-signed certificate.")
	if *keep {
		fmt.Fprintf(stdout, "Demo data: %s\n", dir)
	}
	fmt.Fprintln(stdout, "Press Ctrl-C to stop.")
	fmt.Fprintln(stdout)

	if err := serve(cfg, stderr); err != nil {
		fmt.Fprintf(stderr, "pappice-demo: %v\n", err)
		return 1
	}
	return 0
}

func seedDemoStore(dbPath string) (data.Accounts, error) {
	tracker, err := store.Open(dbPath)
	if err != nil {
		return data.Accounts{}, err
	}
	accounts, err := data.Seed(tracker)
	return accounts, errors.Join(err, tracker.Close())
}

func writeDemoCertificate(certPath, keyPath, addr string) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	notBefore := time.Now().Add(-time.Hour)
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "Pappice local demo",
		},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	template.DNSNames = []string{"localhost"}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else if host != "" {
			template.DNSNames = append(template.DNSNames, host)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return os.WriteFile(keyPath, keyPEM, 0o600)
}

func demoURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("addr must be in host:port form")
	}
	if port == "0" {
		return "", fmt.Errorf("addr must use a concrete port")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "https://" + net.JoinHostPort(host, port), nil
}
