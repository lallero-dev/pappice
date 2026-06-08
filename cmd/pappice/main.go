package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pappice/internal/notify"
	"pappice/internal/server"
	"pappice/internal/store"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	command, commandArgs := splitCommand(args)
	switch command {
	case "help":
		printRootUsage(stdout)
		return 0
	case "serve":
		return runServe(commandArgs, stderr)
	case "db":
		return runDB(commandArgs, stdout, stderr)
	case "doctor":
		return runDoctor(commandArgs, stdout, stderr)
	case "version":
		return runVersion(commandArgs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", command)
		printRootUsage(stderr)
		return 2
	}
}

func splitCommand(args []string) (string, []string) {
	if len(args) < 2 {
		return "help", nil
	}
	first := args[1]
	switch first {
	case "-h", "--help", "help":
		return "help", args[2:]
	case "serve", "db", "doctor", "version":
		return first, args[2:]
	default:
		return first, args[2:]
	}
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, "Pappice customer support ticketing")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pappice serve [flags]     Start the web server")
	fmt.Fprintln(w, "  pappice db <command>      Inspect or migrate the SQLite database")
	fmt.Fprintln(w, "  pappice doctor [flags]    Validate local runtime configuration")
	fmt.Fprintln(w, "  pappice version           Print the build version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run \"pappice serve -h\", \"pappice db -h\", or \"pappice doctor -h\" for configuration flags.")
}

func runServe(args []string, stderr io.Writer) int {
	cfg, code, ok := parseRuntimeConfig("pappice serve", args, stderr)
	if !ok {
		return code
	}
	if err := serve(cfg, stderr); err != nil {
		fmt.Fprintf(stderr, "pappice: %v\n", err)
		return 1
	}
	return 0
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pappice version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: pappice version")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "pappice version: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	fmt.Fprintf(stdout, "pappice %s\n", version)
	return 0
}

func serve(cfg appConfig, stderr io.Writer) error {
	tracker, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer tracker.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := log.New(stderr, "", log.LstdFlags)
	smtpConfig := cfg.smtpConfig()
	emailEnabled := cfg.emailEnabled()
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
		go worker.Run(ctx)
		logger.Printf("email notifications enabled via SMTP host %s", smtpConfig.Host)
	}

	app := server.NewServer(tracker, cfg.serverOptions(emailEnabled))
	go app.RunEventDispatcher(ctx, 5*time.Second, logger)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		if cfg.TLSCert != "" || cfg.TLSKey != "" {
			if cfg.TLSCert == "" || cfg.TLSKey == "" {
				errs <- errors.New("both -tls-cert and -tls-key are required for HTTPS")
				return
			}
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
