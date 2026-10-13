package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ohseeeye/oci"
	"github.com/ohseeeye/oci/ocisqlite"
	"github.com/ohseeeye/registry/internal/config"
	"github.com/ohseeeye/registry/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("registry stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("listen", "127.0.0.1:5000", "HTTP listen address")
	dir := flag.String("data", "./data", "SQLite metadata and blob storage directory")
	configFile := flag.String("config", "", "optional YAML configuration file")
	timeout := flag.Duration("shutdown-timeout", 30*time.Second, "time to let active requests finish")
	flag.Parse()
	if err := server.ValidateTimeout(*timeout); err != nil {
		return err
	}
	cfg, err := config.Load(*configFile)
	if err != nil {
		return err
	}
	backend, err := ocisqlite.New(*dir, nil)
	if err != nil {
		return err
	}
	backends := []*ocisqlite.Registry{backend}
	closeBackends := func() error {
		var err error
		for _, backend := range backends {
			err = errors.Join(err, backend.Close())
		}
		return err
	}
	registry, err := cfg.Wrap(backend, func(namespace string) (oci.Registry, error) {
		cache, err := ocisqlite.New(filepath.Join(*dir, "caches", namespace), &ocisqlite.Options{AllowSparseManifests: true})
		if err != nil {
			return nil, err
		}
		backends = append(backends, cache)
		return cache, nil
	})
	if err != nil {
		return errors.Join(err, closeBackends())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, *addr, registry, *timeout); err != nil {
		// main exits on failure. In particular, do not close the SQLite pool
		// underneath handlers still unwinding after a forced shutdown.
		return err
	}
	return closeBackends()
}
