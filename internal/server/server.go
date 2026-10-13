// Package server provides the HTTP lifecycle shared by the registry binaries.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ohseeeye/oci"
	"github.com/ohseeeye/oci/ociserver"
)

func ValidateTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return errors.New("shutdown-timeout must be positive")
	}
	return nil
}

// Run serves the OCI API until ctx is canceled or the listener fails. A nil
// result means all active requests have finished and storage can be closed.
// On a shutdown timeout it closes connections and returns an error; callers
// should exit the process without closing storage still in use by handlers.
func Run(ctx context.Context, addr string, backend oci.Registry, timeout time.Duration) error {
	if err := ValidateTimeout(timeout); err != nil {
		return err
	}
	handler, err := ociserver.New(backend, &ociserver.ServerConfig{Logger: slog.Default()})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	slog.Info("registry listening", "address", listener.Addr().String())
	return serve(ctx, listener, handler, timeout)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler, timeout time.Duration) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		// No whole-request timeout: large blob transfers may take a long time.
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	select {
	case err := <-done:
		// A listener failure may leave active requests; close their connections.
		return errors.Join(err, srv.Close())
	case <-ctx.Done():
	}
	slog.Info("registry shutting down", "timeout", timeout)
	shutdown, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return errors.Join(fmt.Errorf("shutdown: %w", err), srv.Close())
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
