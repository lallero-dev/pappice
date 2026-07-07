//go:build !debug

package main

import (
	"errors"
	"log"
	"net/http"
	"strings"
)

func startDebugServer(addr string, _ *log.Logger, _ chan<- error) (*http.Server, error) {
	if strings.TrimSpace(addr) != "" {
		return nil, errors.New("debug pprof requires a binary built with -tags debug")
	}
	return nil, nil
}
