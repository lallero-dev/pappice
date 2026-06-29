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

	"pappice/internal/store"
)

const demoPassword = "pappice-demo"

type demoSeed struct {
	URL      string
	Admin    string
	Staff    string
	Customer string
	Password string
	Dir      string
}

func runDemo(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pappice demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "127.0.0.1:8388", "HTTPS listen address")
	keep := fs.Bool("keep", false, "keep the temporary demo directory after shutdown")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: pappice demo [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "pappice demo: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	publicURL, err := demoURL(*addr)
	if err != nil {
		fmt.Fprintf(stderr, "pappice demo: %v\n", err)
		return 2
	}

	dir, err := os.MkdirTemp("", "pappice-demo-*")
	if err != nil {
		fmt.Fprintf(stderr, "pappice demo: create temp dir: %v\n", err)
		return 1
	}
	if !*keep {
		defer os.RemoveAll(dir)
	}

	certPath := filepath.Join(dir, "localhost.pem")
	keyPath := filepath.Join(dir, "localhost-key.pem")
	if err := writeDemoCertificate(certPath, keyPath, *addr); err != nil {
		fmt.Fprintf(stderr, "pappice demo: create TLS certificate: %v\n", err)
		return 1
	}

	cfg := defaultAppConfig()
	cfg.Addr = *addr
	cfg.DBPath = filepath.Join(dir, "pappice.db")
	cfg.UploadDir = filepath.Join(dir, "uploads")
	cfg.BackupDir = filepath.Join(dir, "backups")
	cfg.TLSCert = certPath
	cfg.TLSKey = keyPath
	cfg.PublicURL = publicURL
	cfg.BrandSubtitle = "demo support desk"
	cfg.EmailNotifications = false
	cfg.NotificationDelay = 2 * time.Second

	seed, err := seedDemoStore(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(stderr, "pappice demo: seed data: %v\n", err)
		return 1
	}
	seed.URL = cfg.PublicURL
	seed.Dir = dir
	printDemoSummary(stdout, seed, *keep)

	if err := serve(cfg, stderr); err != nil {
		fmt.Fprintf(stderr, "pappice demo: %v\n", err)
		return 1
	}
	return 0
}

func printDemoSummary(w io.Writer, seed demoSeed, keep bool) {
	fmt.Fprintf(w, "Pappice demo: %s\n\n", seed.URL)
	fmt.Fprintln(w, "Accounts:")
	fmt.Fprintf(w, "  Admin:    %s / %s\n", seed.Admin, seed.Password)
	fmt.Fprintf(w, "  Staff:    %s / %s\n", seed.Staff, seed.Password)
	fmt.Fprintf(w, "  Customer: %s / %s\n", seed.Customer, seed.Password)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The browser will show a warning because the demo uses a local self-signed certificate.")
	if keep {
		fmt.Fprintf(w, "Demo data: %s\n", seed.Dir)
	}
	fmt.Fprintln(w, "Press Ctrl-C to stop.")
	fmt.Fprintln(w)
}

func seedDemoStore(dbPath string) (demoSeed, error) {
	tracker, err := store.Open(dbPath)
	if err != nil {
		return demoSeed{}, err
	}
	defer tracker.Close()

	admin, err := tracker.CreateFirstAdmin(store.CreateUser{
		DisplayName: "Alex Admin",
		Email:       "admin@example.test",
		Password:    demoPassword,
	})
	if err != nil {
		return demoSeed{}, err
	}
	staff, err := tracker.CreateUser(store.CreateUser{
		DisplayName: "Sam Staff",
		Email:       "staff@example.test",
		Password:    demoPassword,
		Role:        "staff",
	})
	if err != nil {
		return demoSeed{}, err
	}
	customer, err := tracker.CreateUser(store.CreateUser{
		DisplayName: "Casey Customer",
		Email:       "customer@example.test",
		Password:    demoPassword,
		Role:        "customer",
	})
	if err != nil {
		return demoSeed{}, err
	}

	products := tracker.ListProducts(admin)
	if len(products) == 0 {
		return demoSeed{}, fmt.Errorf("default product was not created")
	}
	defaultProductID := products[0].ID
	product, err := tracker.CreateProduct(store.CreateProduct{
		Key:         "WEB",
		Name:        "Website Support",
		Description: "Example customer support product.",
	})
	if err != nil {
		return demoSeed{}, err
	}
	if _, err := tracker.DeleteProduct(defaultProductID); err != nil {
		return demoSeed{}, err
	}
	for _, member := range []struct {
		user store.User
		role string
	}{
		{staff, "staff"},
		{customer, "customer"},
	} {
		if _, err := tracker.UpsertProductMember(product.ID, store.UpsertProductMember{
			UserID: member.user.ID,
			Role:   member.role,
		}); err != nil {
			return demoSeed{}, err
		}
	}

	ticket, err := tracker.CreateTicket(store.CreateTicket{
		ProductID:      product.ID,
		Title:          "Login page returns an error",
		Description:    "I cannot sign in after the last password reset. The page shows an error and sends me back to the login form.",
		Priority:       "high",
		Assignee:       staff.DisplayName,
		Source:         "portal",
		RequesterName:  customer.DisplayName,
		RequesterEmail: customer.Email,
		Actor:          store.EventActorFromUser(customer),
	})
	if err != nil {
		return demoSeed{}, err
	}
	assigned := "assigned"
	if _, err := tracker.SaveTicket(store.SaveTicketInput{
		TicketID: ticket.ID,
		Patch:    store.UpdateTicket{Status: &assigned},
		Comment: &store.AddComment{
			Author:       staff.DisplayName,
			AuthorUserID: staff.ID,
			Body:         "Thanks, I can reproduce it. I am checking the account setup now.",
			Visibility:   "public",
		},
		Actor: store.EventActorFromUser(staff),
	}); err != nil {
		return demoSeed{}, err
	}
	if _, err := tracker.SaveTicket(store.SaveTicketInput{
		TicketID: ticket.ID,
		Comment: &store.AddComment{
			Author:       customer.DisplayName,
			AuthorUserID: customer.ID,
			Body:         "I tried again from a private window and it still fails.",
			Visibility:   "public",
		},
		Actor: store.EventActorFromUser(customer),
	}); err != nil {
		return demoSeed{}, err
	}

	if _, err := tracker.CreateTicket(store.CreateTicket{
		ProductID:      product.ID,
		Title:          "Invoice download link is expired",
		Description:    "The invoice link in last week's email no longer opens.",
		Priority:       "normal",
		Source:         "portal",
		RequesterName:  customer.DisplayName,
		RequesterEmail: customer.Email,
		Actor:          store.EventActorFromUser(customer),
	}); err != nil {
		return demoSeed{}, err
	}

	return demoSeed{
		Admin:    admin.Email,
		Staff:    staff.Email,
		Customer: customer.Email,
		Password: demoPassword,
	}, nil
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
	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		certFile.Close()
		return err
	}
	if err := certFile.Close(); err != nil {
		return err
	}

	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		keyFile.Close()
		return err
	}
	return keyFile.Close()
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
