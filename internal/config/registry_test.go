package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ohseeeye/oci"
	"github.com/ohseeeye/oci/ociclient"
	"github.com/ohseeeye/oci/ocimem"
	"github.com/ohseeeye/oci/ociserver"
	"github.com/ohseeeye/oci/ocisqlite"
	"github.com/ohseeeye/oci/pkg/ocidigest"
)

func readContent(t *testing.T, reader oci.BlobReader, err error) []byte {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAuthenticatedPullThrough(t *testing.T) {
	for _, storage := range []string{"mem", "sqlite"} {
		for _, auth := range []string{"basic", "bearer"} {
			t.Run(storage+"/"+auth, func(t *testing.T) {
				ctx := context.Background()
				upstream := ocimem.New()
				blob := []byte(`{}`)
				digest := ocidigest.SHA256.FromBytes(blob)
				if _, err := upstream.PushBlob(ctx, "team/nested/image", oci.Descriptor{Digest: digest, Size: int64(len(blob)), MediaType: "application/vnd.oci.image.config.v1+json"}, bytes.NewReader(blob)); err != nil {
					t.Fatal(err)
				}
				manifest, err := json.Marshal(map[string]any{
					"schemaVersion": 2, "mediaType": oci.MediaTypeImageManifest,
					"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": digest, "size": len(blob)},
					"layers": []any{},
				})
				if err != nil {
					t.Fatal(err)
				}
				desc, err := upstream.PushManifest(ctx, "team/nested/image", manifest, oci.MediaTypeImageManifest, &oci.PushManifestParameters{Tags: []string{"latest"}})
				if err != nil {
					t.Fatal(err)
				}
				handler, err := ociserver.New(upstream, nil)
				if err != nil {
					t.Fatal(err)
				}
				var requests, authorized atomic.Int64
				var origin *httptest.Server
				origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					user, password, _ := r.BasicAuth()
					basicOK := user == "reader" && password == " secret "
					if auth == "bearer" && r.URL.Path == "/token" {
						if !basicOK || r.URL.Query().Get("scope") != "repository:team/nested/image:pull" {
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"token":"test-token","expires_in":3600}`)
						return
					}
					if (auth == "basic" && !basicOK) || (auth == "bearer" && r.Header.Get("Authorization") != "Bearer test-token") {
						if auth == "basic" {
							w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
						} else {
							w.Header().Set("WWW-Authenticate", `Bearer realm="`+origin.URL+`/token",service="test",scope="repository:team/nested/image:pull"`)
						}
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					authorized.Add(1)
					handler.ServeHTTP(w, r)
				}))
				defer origin.Close()
				secret := filepath.Join(t.TempDir(), "password")
				writeFile(t, secret, " secret \r\n")
				cfg := Config{Version: 1, Upstreams: []Upstream{{
					Name: "origin", Match: []string{"team/**"}, Host: strings.TrimPrefix(origin.URL, "http://"),
					Username: "reader", PasswordFile: secret, Insecure: true,
				}}}
				var cache oci.Registry
				local := ocimem.New()
				registry, err := cfg.Wrap(local, func(string) (oci.Registry, error) {
					if storage == "sqlite" {
						db, err := ocisqlite.New(t.TempDir(), &ocisqlite.Options{AllowSparseManifests: true})
						if err != nil {
							return nil, err
						}
						t.Cleanup(func() {
							if err := db.Close(); err != nil {
								t.Error(err)
							}
						})
						cache = db
					} else {
						cache = ocimem.NewWithConfig(&ocimem.Config{AllowSparseManifests: true})
					}
					return cache, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if requests.Load() != 0 {
					t.Fatal("startup contacted upstream")
				}
				downstreamHandler, err := ociserver.New(registry, nil)
				if err != nil {
					t.Fatal(err)
				}
				downstream := httptest.NewServer(downstreamHandler)
				defer downstream.Close()
				client, err := ociclient.New(strings.TrimPrefix(downstream.URL, "http://"), &ociclient.Options{Insecure: true})
				if err != nil {
					t.Fatal(err)
				}
				reader, err := client.GetTag(ctx, "team/nested/image", "latest")
				if got := readContent(t, reader, err); !bytes.Equal(got, manifest) {
					t.Fatal("unexpected manifest")
				}
				// This must populate a sparse cache before the config blob arrives.
				if _, err := cache.ResolveManifest(ctx, "team/nested/image", desc.Digest); err != nil {
					t.Fatalf("manifest did not populate cache: %v", err)
				}
				before := authorized.Load()
				reader, err = client.GetTag(ctx, "team/nested/image", "latest")
				readContent(t, reader, err)
				if authorized.Load() <= before {
					t.Fatal("default tag resolution failed to revalidate upstream")
				}
				reader, err = client.GetBlob(ctx, "team/nested/image", digest)
				if got := readContent(t, reader, err); !bytes.Equal(got, blob) {
					t.Fatal("unexpected blob")
				}
				if _, err := client.PushManifest(ctx, "team/nested/image", manifest, oci.MediaTypeImageManifest, &oci.PushManifestParameters{Tags: []string{"blocked"}}); err == nil {
					t.Fatal("cache route allowed an upstream write")
				}
				if _, err := upstream.ResolveTag(ctx, "team/nested/image", "blocked"); err == nil {
					t.Fatal("write reached upstream")
				}
				if _, err := local.ResolveManifest(ctx, "team/nested/image", desc.Digest); err == nil {
					t.Fatal("cache content leaked into local registry")
				}
				origin.Close()
				reader, err = client.GetManifest(ctx, "team/nested/image", desc.Digest)
				readContent(t, reader, err)
				reader, err = client.GetBlob(ctx, "team/nested/image", digest)
				readContent(t, reader, err)
				if reader, err := client.GetTag(ctx, "team/nested/image", "latest"); err == nil {
					reader.Close()
					t.Fatal("default tag lookup succeeded with upstream offline")
				}
			})
		}
	}
}

func TestRoutingOrderAndCacheIsolation(t *testing.T) {
	ctx := context.Background()
	newOrigin := func(value string) (*httptest.Server, oci.Descriptor) {
		t.Helper()
		backend := ocimem.New()
		desc, err := backend.PushManifest(ctx, "team/nested/image", []byte(value), "application/test", &oci.PushManifestParameters{Tags: []string{"latest"}})
		if err != nil {
			t.Fatal(err)
		}
		handler, err := ociserver.New(backend, nil)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server, desc
	}
	first, firstDesc := newOrigin("first")
	second, secondDesc := newOrigin("second")
	cfg := Config{Version: 1, Upstreams: []Upstream{
		{Name: "first", Match: []string{"team/nested/*"}, Host: strings.TrimPrefix(first.URL, "http://"), Insecure: true, CacheTags: true},
		{Name: "second", Match: []string{"team/**"}, Host: strings.TrimPrefix(second.URL, "http://"), Insecure: true, FallbackTagsOnError: true},
	}}
	local := ocimem.New()
	caches := map[string]oci.Registry{}
	factory := func(namespace string) (oci.Registry, error) {
		if caches[namespace] == nil {
			caches[namespace] = ocimem.NewWithConfig(&ocimem.Config{AllowSparseManifests: true})
		}
		return caches[namespace], nil
	}
	registry, err := cfg.Wrap(local, factory)
	if err != nil {
		t.Fatal(err)
	}
	if desc, err := registry.ResolveTag(ctx, "team/nested/image", "latest"); err != nil || desc.Digest != firstDesc.Digest {
		t.Fatalf("first match did not win: %+v, %v", desc, err)
	}
	first.Close()
	if _, err := registry.ResolveTag(ctx, "team/nested/image", "latest"); err != nil {
		t.Fatalf("explicit tag caching did not work offline: %v", err)
	}
	// Changing the selected upstream's host gets a new cache namespace.
	cfg.Upstreams[0].Host = strings.TrimPrefix(second.URL, "http://")
	registry, err = cfg.Wrap(local, factory)
	if err != nil {
		t.Fatal(err)
	}
	if desc, err := registry.ResolveTag(ctx, "team/nested/image", "latest"); err != nil || desc.Digest != secondDesc.Digest {
		t.Fatalf("reused cache from another upstream: %+v, %v", desc, err)
	}
	// The broader second route uses a separate cache and saves tags for fallback.
	cfg.Upstreams = cfg.Upstreams[1:]
	registry, err = cfg.Wrap(local, factory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ResolveTag(ctx, "team/nested/image", "latest"); err != nil {
		t.Fatal(err)
	}
	second.Close()
	if desc, err := registry.ResolveTag(ctx, "team/nested/image", "latest"); err != nil || desc.Digest != secondDesc.Digest {
		t.Fatalf("explicit tag fallback did not work offline: %+v, %v", desc, err)
	}
	if _, err := registry.PushManifest(ctx, "local/image", []byte("local"), "application/test", &oci.PushManifestParameters{Tags: []string{"latest"}}); err != nil {
		t.Fatalf("unmatched repository is not writable locally: %v", err)
	}
	if _, err := registry.ResolveTag(ctx, "unmatched/missing", "latest"); !errors.Is(err, oci.ErrNameUnknown) {
		t.Fatalf("unmatched repository did not stay local: %v", err)
	}
}
