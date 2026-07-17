package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// envDefaultRe matches shell-style ${VAR:-fallback} references. Plain $VAR
// and ${VAR} go through [os.ExpandEnv] afterwards.
var envDefaultRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):-([^}]*)\}`)

// envRefRe matches ${VAR} / ${VAR:-fallback} env references so [HasGlobMeta]
// can strip them before scanning for glob metacharacters.
var envRefRe = regexp.MustCompile(`\$\{[^}]*\}`)

// ExpandHostPath expands a staging directive's host-side path vocabulary:
// shell-style ${VAR:-fallback} defaults, a leading ~, and $VAR / ${VAR}
// environment references. The result is absolute.
func ExpandHostPath(p string) (string, error) {
	p = envDefaultRe.ReplaceAllStringFunc(p, func(m string) string {
		groups := envDefaultRe.FindStringSubmatch(m)
		if v := os.Getenv(groups[1]); v != "" {
			return v
		}
		return groups[2]
	})

	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: resolve home dir: %w", p, err)
		}
		p = home + p[1:]
	}

	p = os.ExpandEnv(p)

	// A relative result (e.g. a relative env-var value in a multi-account
	// workflow) resolves against the current working directory, matching
	// filepath.Abs semantics.
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("expand %q: resolve absolute path: %w", p, err)
	}
	return abs, nil
}

// NormalizeContainerPath canonicalizes a container-home-relative staging
// path: cleaned, slash-separated, no leading or trailing slash.
func NormalizeContainerPath(p string) string {
	return strings.Trim(filepath.ToSlash(filepath.Clean(p)), "/")
}

// HasGlobMeta reports whether p contains doublestar glob metacharacters.
// ${VAR} / ${VAR:-fallback} env references are vocabulary, not globs — they
// are ignored.
func HasGlobMeta(p string) bool {
	return strings.ContainsAny(envRefRe.ReplaceAllString(p, ""), "*?[{")
}
