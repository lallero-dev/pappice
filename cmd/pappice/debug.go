//go:build debug

package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"
)

func startDebugServer(addr string, logger *log.Logger, errs chan<- error) (*http.Server, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil
	}
	if err := validateDebugAddr(addr); err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen debug address: %w", err)
	}

	srv := &http.Server{
		Handler:           debugMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Printf("pappice debug pprof listening on http://%s", listener.Addr())
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errs <- fmt.Errorf("debug serve: %w", err)
		}
	}()
	return srv, nil
}

func debugMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

func validateDebugAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("debug address must be host:port: %w", err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("debug address must use an explicit loopback host")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("debug address must use localhost, 127.0.0.1, or ::1")
	}
	return nil
}
