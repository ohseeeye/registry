package config

import "testing"

func TestRepositoryPatterns(t *testing.T) {
	for _, tt := range []struct {
		pattern, repo string
		want          bool
	}{
		{"team/image", "team/image", true},
		{"team/image", "team/image/child", false},
		{"*", "image", true},
		{"*", "team/image", false},
		{"team/*", "team/image", true},
		{"team/*", "team/nested/image", false},
		{"team/*", "team", false},
		{"team/**", "team", true},
		{"team/**", "team/image", true},
		{"team/**", "team/a/b/image", true},
		{"team/**", "teammate/image", false},
		{"team/**", "other/team/image", false},
		{"team-*/**", "team-one/nested/image", true},
		{"team/*/**", "team/one", true},
		{"team/*/**", "team/one/nested/image", true},
		{"team/*/**", "team", false},
		{"**", "image", true},
		{"**", "a/b/c/image", true},
		{"team/image?", "team/image1", true},
		{"team/image?", "team/image12", false},
		{"team/[ab]*", "team/alpine", true},
		{"team/[ab]*", "team/nginx", false},
	} {
		t.Run(tt.pattern+"/"+tt.repo, func(t *testing.T) {
			if err := validateRepositoryPattern(tt.pattern); err != nil {
				t.Fatal(err)
			}
			if got := matchRepository(tt.pattern, tt.repo); got != tt.want {
				t.Fatalf("match(%q, %q) = %t; want %t", tt.pattern, tt.repo, got, tt.want)
			}
		})
	}
}

func TestUnsupportedRepositoryPatterns(t *testing.T) {
	for _, pattern := range []string{"", "/team/*", "/**", "[", "team/[/image", "team/**/image", "**/image", "team/**/**", "team/***", "{team,library}/**"} {
		if err := validateRepositoryPattern(pattern); err == nil {
			t.Errorf("accepted unsupported pattern %q", pattern)
		}
	}
}
