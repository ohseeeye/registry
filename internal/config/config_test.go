package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ohseeeye/oci/ocimem"
	"github.com/ohseeeye/oci/pkg/ociauth"
)

func writeFile(t *testing.T, filename, contents string) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsStayScopedToUpstream(t *testing.T) {
	credentials := hostCredentials{host: "registry.example", entry: ociauth.ConfigEntry{Username: "reader", Password: "secret"}}
	entry, err := credentials.EntryForRegistry("registry.example")
	if err != nil || entry.Username != "reader" || entry.Password != "secret" {
		t.Fatal("upstream credentials missing")
	}
	entry, err = credentials.EntryForRegistry("other.example")
	if err != nil || entry != (ociauth.ConfigEntry{}) {
		t.Fatal("credentials leaked to another registry host")
	}
}

func TestLoadDefaults(t *testing.T) {
	for _, input := range []string{"", "# empty\n", "{}", "version: 1\nupstreams: []"} {
		filename := filepath.Join(t.TempDir(), "registry.yaml")
		writeFile(t, filename, input)
		cfg, err := Load(filename)
		if err != nil {
			t.Fatal(err)
		}
		local := ocimem.New()
		registry, err := cfg.Wrap(local, nil)
		if err != nil || registry != local {
			t.Fatalf("empty config changed the local registry: %v", err)
		}
	}
	cfg, err := Load("")
	if err != nil || cfg.Version != 1 || len(cfg.Upstreams) != 0 {
		t.Fatalf("no-config defaults: %+v, %v", cfg, err)
	}
}

func TestLoadValidation(t *testing.T) {
	tests := []struct{ name, yaml string }{
		{"version", "version: 2"},
		{"unknown field", "upstreems: []"},
		{"multiple documents", "version: 1\n---\nversion: 1"},
		{"duplicate key", "version: 1\nversion: 1"},
		{"missing name", "upstreams: [{host: example.com, match: ['**']}]"},
		{"bad name", "upstreams: [{name: ../escape, host: example.com, match: ['**']}]"},
		{"duplicate names", "upstreams: [{name: same, host: a.example, match: ['a/**']}, {name: same, host: b.example, match: ['b/**']}]"},
		{"missing host", "upstreams: [{name: test, match: ['**']}]"},
		{"URL host", "upstreams: [{name: test, host: 'https://example.com', match: ['**']}]"},
		{"host path", "upstreams: [{name: test, host: example.com/team, match: ['**']}]"},
		{"host credentials", "upstreams: [{name: test, host: 'user:pass@example.com', match: ['**']}]"},
		{"host query", "upstreams: [{name: test, host: 'example.com?x=y', match: ['**']}]"},
		{"missing matches", "upstreams: [{name: test, host: example.com}]"},
		{"bad glob", "upstreams: [{name: test, host: example.com, match: ['[']}]"},
		{"embedded globstar", "upstreams: [{name: test, host: example.com, match: ['team/**/image']}]"},
		{"brace alternatives", "upstreams: [{name: test, host: example.com, match: ['{team,library}/**']}]"},
		{"username only", "upstreams: [{name: test, host: example.com, match: ['**'], username: reader}]"},
		{"password file only", "upstreams: [{name: test, host: example.com, match: ['**'], password_file: secret}]"},
		{"inline password", "upstreams: [{name: test, host: example.com, match: ['**'], password: secret}]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "registry.yaml")
			writeFile(t, filename, tt.yaml)
			if _, err := Load(filename); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestRelativePasswordFileAndFailures(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "registry.yaml")
	writeFile(t, filename, "upstreams: [{name: test, host: example.com, match: ['**'], username: reader, password_file: secret}]")
	cfg, err := Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upstreams[0].PasswordFile != filepath.Join(dir, "secret") {
		t.Fatal("password path was not resolved relative to config")
	}
	for _, contents := range []string{"", "\n"} {
		writeFile(t, filepath.Join(dir, "secret"), contents)
		_, err := cfg.Wrap(ocimem.New(), nil)
		if err == nil || !strings.Contains(err.Error(), "password file is empty") {
			t.Fatalf("empty secret: %v", err)
		}
	}
	cfg.Upstreams[0].PasswordFile = filepath.Join(dir, "missing")
	if _, err := cfg.Wrap(ocimem.New(), nil); err == nil {
		t.Fatal("accepted missing secret file")
	}
}
