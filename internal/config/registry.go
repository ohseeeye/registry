package config

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ohseeeye/oci"
	"github.com/ohseeeye/oci/ociclient"
	"github.com/ohseeeye/oci/ocimiddleware"
	"github.com/ohseeeye/oci/pkg/ociauth"
)

// CacheFactory creates a dedicated sparse-capable backend for each upstream.
// The caller owns the returned backends and closes them after requests drain.
type CacheFactory func(namespace string) (oci.Registry, error)

type hostCredentials struct {
	host  string
	entry ociauth.ConfigEntry
}

func (c hostCredentials) EntryForRegistry(host string) (ociauth.ConfigEntry, error) {
	if host == c.host {
		return c.entry, nil
	}
	return ociauth.ConfigEntry{}, nil
}

// Wrap routes repositories to the first matching upstream's read-only cache,
// falling back to the writable local registry. With no upstreams it returns
// local unchanged, preserving its optional capabilities.
func (cfg Config) Wrap(local oci.Registry, newCache CacheFactory) (oci.Registry, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if len(cfg.Upstreams) == 0 {
		return local, nil
	}
	type route struct {
		patterns []string
		registry oci.Registry
	}
	routes := make([]route, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		var credentials ociauth.Config
		if u.PasswordFile != "" {
			data, err := os.ReadFile(u.PasswordFile)
			if err != nil {
				return nil, fmt.Errorf("upstream %s: read password file: %w", u.Name, err)
			}
			// Allow a final newline from a mounted text secret, preserving spaces.
			password := string(data)
			if strings.HasSuffix(password, "\n") {
				password = strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r")
			}
			if password == "" {
				return nil, fmt.Errorf("upstream %s: password file is empty", u.Name)
			}
			credentials = hostCredentials{host: u.Host, entry: ociauth.ConfigEntry{Username: u.Username, Password: password}}
		}
		client, err := ociclient.New(u.Host, &ociclient.Options{
			Insecure: u.Insecure,
			Transport: ociauth.NewStdTransport(ociauth.StdTransportParams{
				Config: credentials,
			}),
		})
		if err != nil {
			return nil, fmt.Errorf("upstream %s: %w", u.Name, err)
		}
		// Host/account changes must not reuse another authorization domain's
		// cached content. Password rotations keep the existing cache.
		identity := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%t", u.Host, u.Username, u.Insecure)))
		cache, err := newCache(fmt.Sprintf("%s-%x", u.Name, identity))
		if err != nil {
			return nil, fmt.Errorf("upstream %s: open cache: %w", u.Name, err)
		}
		cached, err := ocimiddleware.Cache(client, cache, &ocimiddleware.CacheOptions{
			CacheTags:           u.CacheTags,
			FallbackTagsOnError: u.FallbackTagsOnError,
			OnError: func(err error) {
				slog.Warn("upstream cache", "upstream", u.Name, "error", err)
			},
		})
		if err != nil {
			return nil, fmt.Errorf("upstream %s: configure cache: %w", u.Name, err)
		}
		routes = append(routes, route{patterns: append([]string(nil), u.Match...), registry: ocimiddleware.ReadOnly(cached)})
	}
	return ocimiddleware.Selector(func(repo string) oci.Registry {
		for _, route := range routes {
			for _, pattern := range route.patterns {
				if matchRepository(pattern, repo) {
					return route.registry
				}
			}
		}
		return local
	}), nil
}
