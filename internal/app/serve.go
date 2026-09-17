// Package app owns native server configuration, startup, and shutdown.
package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"pappice/internal/notify"
	"pappice/internal/server"
	"pappice/internal/store"
)

// Serve runs the HTTP server and background workers until shutdown or failure.
func Serve(cfg Config, stderr io.Writer) error {
	useTLS, err := cfg.TLSEnabled()
	if err != nil {
		return err
	}
	tracker, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer tracker.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	var workers sync.WaitGroup
	defer func() {
		stop()
		workers.Wait()
	}()

	logger := log.New(stderr, "", log.LstdFlags)
	smtpConfig := cfg.SMTPConfig()
	emailEnabled := cfg.EmailEnabled()
	if emailEnabled {
		mailer, err := notify.NewSMTPMailer(smtpConfig)
		if err != nil {
			return fmt.Errorf("configure email notifications: %w", err)
		}
		worker := notify.Worker{
			Store:       tracker,
			Mailer:      mailer,
			From:        smtpConfig.From,
			Interval:    5 * time.Second,
			LeaseFor:    time.Minute,
			BatchSize:   10,
			MaxAttempts: 5,
			Logger:      logger,
		}
		workers.Go(func() { worker.Run(ctx) })
		logger.Printf("email notifications enabled via SMTP host %s", smtpConfig.Host)
	}

	serverOptions := cfg.serverOptions(emailEnabled)
	serverOptions.Logger = logger
	app := server.NewServer(tracker, serverOptions)
	workers.Go(func() { app.RunEventDispatcher(ctx, 5*time.Second) })
	workers.Go(func() { app.RunWebhookDispatcher(ctx, 5*time.Second) })

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app,
		ReadHeaderTimeout: 5 * time.Second,
	}
	defer srv.Close()

	errs := make(chan error, 2)
	debugSrv, err := startDebugServer(cfg.DebugAddr, logger, errs)
	if err != nil {
		return err
	}
	if debugSrv != nil {
		defer debugSrv.Close()
	}

	go func() {
		if useTLS {
			logger.Printf("pappice listening on https://%s", cfg.Addr)
			errs <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
			return
		}
		logger.Printf("pappice listening on http://%s (browser login requires HTTPS)", cfg.Addr)
		errs <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Printf("shutdown requested")
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if debugSrv != nil {
		if err := debugSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown debug: %w", err)
		}
	}
	return nil
}
