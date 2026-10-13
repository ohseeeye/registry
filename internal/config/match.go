package config

import (
	"errors"
	"path"
	"strings"
)

// Repository patterns use path.Match, plus ** and a trailing /** for subtrees.
// Reject other globstar positions and brace alternatives rather than silently
// treating syntax from a more general glob implementation as ordinary text.
func validateRepositoryPattern(pattern string) error {
	if pattern == "**" {
		return nil
	}
	prefix := strings.TrimSuffix(pattern, "/**")
	if prefix == "" || strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "**") || strings.ContainsAny(prefix, "{}") {
		return errors.New("use path.Match syntax, **, or a trailing /**; brace alternatives are unsupported")
	}
	_, err := path.Match(prefix, "")
	return err
}

// matchRepository is called only after configuration validation.
func matchRepository(pattern, repo string) bool {
	if pattern == "**" {
		return true
	}
	prefix, recursive := strings.CutSuffix(pattern, "/**")
	for {
		if matched, _ := path.Match(prefix, repo); matched {
			return true
		}
		slash := strings.LastIndexByte(repo, '/')
		if !recursive || slash < 0 {
			return false
		}
		repo = repo[:slash]
	}
}
