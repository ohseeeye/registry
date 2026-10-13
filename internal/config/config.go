// Package config loads the optional registry routing configuration.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version   int        `yaml:"version"`
	Upstreams []Upstream `yaml:"upstreams"`
}

type Upstream struct {
	Name                string   `yaml:"name"`
	Match               []string `yaml:"match"`
	Host                string   `yaml:"host"`
	Username            string   `yaml:"username"`
	PasswordFile        string   `yaml:"password_file"`
	Insecure            bool     `yaml:"insecure"`
	CacheTags           bool     `yaml:"cache_tags"`
	FallbackTagsOnError bool     `yaml:"fallback_tags_on_error"`
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Load returns a local-only configuration when filename is empty. Relative
// password paths are resolved against the YAML file, not the working directory.
func Load(filename string) (Config, error) {
	cfg := Config{Version: 1}
	if filename == "" {
		return cfg, nil
	}
	f, err := os.Open(filename)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config must contain exactly one YAML document")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	for i := range cfg.Upstreams {
		u := &cfg.Upstreams[i]
		if u.PasswordFile != "" && !filepath.IsAbs(u.PasswordFile) {
			u.PasswordFile = filepath.Join(filepath.Dir(filename), u.PasswordFile)
		}
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	if cfg.Version != 1 {
		return fmt.Errorf("unsupported config version %d (want 1)", cfg.Version)
	}
	names := make(map[string]bool)
	for i, u := range cfg.Upstreams {
		label := fmt.Sprintf("upstreams[%d]", i)
		if !validName.MatchString(u.Name) || names[u.Name] {
			return fmt.Errorf("%s: name must be unique and match %s", label, validName)
		}
		names[u.Name] = true
		parsed, err := url.Parse("https://" + u.Host)
		if err != nil || u.Host == "" || strings.TrimSpace(u.Host) != u.Host || parsed.Host != u.Host || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%s: host must be a hostname or host:port, without a scheme or path", label)
		}
		if (u.Username == "") != (u.PasswordFile == "") {
			return fmt.Errorf("%s: username and password_file must be provided together", label)
		}
		if len(u.Match) == 0 {
			return fmt.Errorf("%s: match must contain at least one repository glob", label)
		}
		for _, pattern := range u.Match {
			if err := validateRepositoryPattern(pattern); err != nil {
				return fmt.Errorf("%s: invalid repository glob %q: %w", label, pattern, err)
			}
		}
	}
	return nil
}
