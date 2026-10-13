package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ohseeeye/oci"
	"github.com/ohseeeye/oci/ocimem"
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
	configFile := flag.String("config", "", "optional YAML configuration file")
	timeout := flag.Duration("shutdown-timeout", 30*time.Second, "time to let active requests finish")
	flag.Parse()
	cfg, err := config.Load(*configFile)
	if err != nil {
		return err
	}
	registry, err := cfg.Wrap(ocimem.New(), func(string) (oci.Registry, error) {
		return ocimem.NewWithConfig(&ocimem.Config{AllowSparseManifests: true}), nil
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Run(ctx, *addr, registry, *timeout)
}
