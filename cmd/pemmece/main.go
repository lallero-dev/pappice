package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"pemmece/internal/server"
	"pemmece/internal/store"
)

func main() {
	addr := flag.String("addr", envOr("PEMMECE_ADDR", "127.0.0.1:8388"), "HTTP listen address")
	dbPath := flag.String("db", envOr("PEMMECE_DB", "pemmece.db"), "path to SQLite database file")
	tlsCert := flag.String("tls-cert", envOr("PEMMECE_TLS_CERT", ""), "TLS certificate path")
	tlsKey := flag.String("tls-key", envOr("PEMMECE_TLS_KEY", ""), "TLS private key path")
	allowInsecureWebhooks := flag.Bool("allow-insecure-webhooks", envBool("PEMMECE_ALLOW_INSECURE_WEBHOOKS"), "allow http webhook URLs")
	allowPrivateWebhooks := flag.Bool("allow-private-webhooks", envBool("PEMMECE_ALLOW_PRIVATE_WEBHOOKS"), "allow private/link-local webhook targets")
	var repoRoots stringListFlag
	repoRoots.Set(envOr("PEMMECE_REPO_ROOTS", ""))
	flag.Var(&repoRoots, "repo-root", "allowed repository scan root; may be repeated")
	flag.Parse()

	tracker, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer tracker.Close()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(tracker, server.Options{AllowInsecureWebhooks: *allowInsecureWebhooks, AllowPrivateWebhooks: *allowPrivateWebhooks, RepoRoots: repoRoots.Values()}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		if *tlsCert != "" || *tlsKey != "" {
			if *tlsCert == "" || *tlsKey == "" {
				errs <- http.ErrServerClosed
				log.Fatalf("both -tls-cert and -tls-key are required for HTTPS")
			}
			log.Printf("pemmece listening on https://%s", *addr)
			errs <- srv.ListenAndServeTLS(*tlsCert, *tlsKey)
			return
		}
		log.Printf("pemmece listening on http://%s (browser login requires HTTPS)", *addr)
		errs <- srv.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-signals:
		log.Printf("received %s, shutting down", sig)
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

type stringListFlag []string

func (f *stringListFlag) Set(value string) error {
	for _, item := range strings.FieldsFunc(value, func(r rune) bool {
		return r == os.PathListSeparator || r == ','
	}) {
		item = strings.TrimSpace(item)
		if item != "" {
			*f = append(*f, item)
		}
	}
	return nil
}

func (f *stringListFlag) String() string {
	return strings.Join(*f, string(os.PathListSeparator))
}

func (f *stringListFlag) Values() []string {
	return append([]string(nil), *f...)
}
