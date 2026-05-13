package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pemmece/internal/server"
	"pemmece/internal/store"
)

func main() {
	addr := flag.String("addr", envOr("PEMMECE_ADDR", "127.0.0.1:8388"), "HTTP listen address")
	dataPath := flag.String("data", envOr("PEMMECE_DATA", "pemmece-data.json"), "path to JSON data file")
	flag.Parse()

	tracker, err := store.Open(*dataPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(tracker),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		log.Printf("pemmece listening on http://%s", *addr)
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
