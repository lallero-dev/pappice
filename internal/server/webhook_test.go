package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestWebhookDNSRespectsCancellation(t *testing.T) {
	for _, operation := range []string{"validate", "dial"} {
		t.Run(operation, func(t *testing.T) {
			started := make(chan struct{}, 1)
			resolver := net.DefaultResolver
			net.DefaultResolver = &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					select {
					case started <- struct{}{}:
					default:
					}
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}
			defer func() { net.DefaultResolver = resolver }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			app := &Server{}
			done := make(chan error, 1)
			go func() {
				if operation == "validate" {
					done <- app.validateWebhookTarget(ctx, "https://hooks.example.test")
					return
				}
				conn, err := app.dialWebhookContext(ctx, "tcp", "hooks.example.test:443")
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
			}()
			select {
			case <-started:
			case err := <-done:
				t.Fatalf("request ended before DNS lookup: %v", err)
			case <-time.After(time.Second):
				t.Fatal("DNS lookup did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lookup error = %v, want cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("DNS lookup ignored cancellation")
			}
		})
	}
}
