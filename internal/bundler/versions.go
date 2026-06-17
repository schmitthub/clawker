package bundler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/bundler/registry"
)

const (
	// ClaudeCodePackage is the npm package name for Claude Code.
	ClaudeCodePackage = "@anthropic-ai/claude-code"
)

// VersionsManager handles version discovery and resolution for Claude Code.
type VersionsManager struct {
	fetcher registry.Fetcher
	config  *VariantConfig
}

// NewVersionsManager creates a new versions manager with default settings.
func NewVersionsManager() *VersionsManager {
	return &VersionsManager{
		fetcher: registry.NewNPMClient(),
		config:  DefaultVariantConfig(),
	}
}

// NewVersionsManagerWithFetcher creates a versions manager with a custom fetcher.
// This is useful for testing with mock implementations.
func NewVersionsManagerWithFetcher(fetcher registry.Fetcher, config *VariantConfig) *VersionsManager {
	if config == nil {
		config = DefaultVariantConfig()
	}
	return &VersionsManager{
		fetcher: fetcher,
		config:  config,
	}
}

// ResolveLatestClaudeCodeVersion calls the npm registry through the supplied
// http.Client and returns the concrete version that the "latest" dist-tag
// currently points at (e.g. "2.1.5"). Resolution happens once per build at
// the command layer; the result is then baked into the rendered Dockerfile
// via ProjectGenerator.ClaudeCodeVersion / BuilderOptions.ClaudeCodeVersion
// so the install layer's ARG cache busts only when npm publishes a new
// release.
//
// On resolution failure (offline, registry 5xx, empty response) returns
// DefaultClaudeCodeVersion ("latest" literal) + the underlying error so
// callers can warn the user. Build still works in that path — the install
// RUN at the end of the Dockerfile downloads whatever npm latest is at
// build time — but the cache won't bust on a new release until network
// returns.
//
// Pass a stdlib *http.Client; in production the Factory's HttpClient
// closure supplies it, in tests a client with a stubbed RoundTripper
// substitutes the registry. The npm-specific knowledge (URL, parsing) is
// encapsulated in registry.NPMClient via WithHTTPClient.
func ResolveLatestClaudeCodeVersion(ctx context.Context, httpClient *http.Client) (string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	fetcher := registry.NewNPMClient(registry.WithHTTPClient(httpClient))
	mgr := NewVersionsManagerWithFetcher(fetcher, nil)
	vf, err := mgr.ResolveVersions(ctx, []string{DefaultClaudeCodeVersion}, ResolveOptions{})
	if err != nil {
		return DefaultClaudeCodeVersion, err
	}
	// Single-pattern call: ResolveVersions returns at most one entry, keyed
	// by the resolved version string. Take that single key explicitly
	// rather than picking via non-deterministic map iteration so the
	// contract is obvious if a future caller threads more patterns through.
	if len(*vf) != 1 {
		return DefaultClaudeCodeVersion, fmt.Errorf("expected 1 resolved version for %q, got %d", DefaultClaudeCodeVersion, len(*vf))
	}
	for v := range *vf {
		return v, nil
	}
	return DefaultClaudeCodeVersion, ErrNoVersions
}

// ResolveOptions configures version resolution behavior.
type ResolveOptions struct {
	// Debug enables verbose output during resolution.
	Debug bool
	// Output is the writer for informational messages. Defaults to io.Discard if nil.
	Output io.Writer
}

// output returns the configured output writer, defaulting to io.Discard.
func (o ResolveOptions) output() io.Writer {
	if o.Output != nil {
		return o.Output
	}
	return io.Discard
}

// ResolveVersions resolves version patterns to full versions.
// Patterns can be:
//   - "latest", "stable", "next" - resolved via npm dist-tags
//   - "2.1" - partial match to highest 2.1.x release
//   - "2.1.2" - exact version match
func (m *VersionsManager) ResolveVersions(ctx context.Context, patterns []string, opts ResolveOptions) (*registry.VersionsFile, error) {
	// Fetch all available versions and dist-tags
	allVersions, err := m.fetcher.FetchVersions(ctx, ClaudeCodePackage)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions: %w", err)
	}

	distTags, err := m.fetcher.FetchDistTags(ctx, ClaudeCodePackage)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dist-tags: %w", err)
	}

	out := opts.output()
	if opts.Debug {
		fmt.Fprintf(out, "[DEBUG] Found %d versions\n", len(allVersions))
		fmt.Fprintf(out, "[DEBUG] Dist-tags: %v\n", distTags)
	}

	result := make(registry.VersionsFile)

	for _, pattern := range patterns {
		fullVersion, err := m.resolvePattern(ctx, pattern, allVersions, distTags, opts)
		if err != nil {
			fmt.Fprintf(out, "warning: %v\n", err)
			continue
		}

		if opts.Debug {
			fmt.Fprintf(out, "[DEBUG] Resolved %q -> %q\n", pattern, fullVersion)
		}

		// Parse and create version info
		v, err := semver.NewVersion(fullVersion)
		if err != nil {
			fmt.Fprintf(out, "warning: invalid version format %q: %v\n", fullVersion, err)
			continue
		}

		info := registry.NewVersionInfo(v, m.config.DebianDefault, m.config.AlpineDefault, m.config.Variants)
		result[fullVersion] = info

		fmt.Fprintf(out, "Full version for %s: %s\n", pattern, fullVersion)
	}

	if len(result) == 0 {
		return nil, ErrNoVersions
	}

	return &result, nil
}

// resolvePattern resolves a single version pattern to a full version string.
func (m *VersionsManager) resolvePattern(ctx context.Context, pattern string, versions []string, distTags registry.DistTags, opts ResolveOptions) (string, error) {
	// Check if pattern is a dist-tag
	switch pattern {
	case "latest", "stable", "next":
		version, ok := distTags[pattern]
		if !ok || version == "" {
			return "", fmt.Errorf("cannot find version for dist-tag %q", pattern)
		}
		return version, nil
	}

	// Treat the pattern as a (possibly partial) semver constraint: "2.1" expands
	// to ">=2.1.0 <2.2.0", "2.1.3" is exact equality. Prereleases are excluded by
	// default unless the constraint string itself names one (Masterminds
	// semantics) — preserving the old Match behavior (highest non-prerelease in
	// range; an exact prerelease pattern still resolves).
	constraint, err := semver.NewConstraint(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid version format %q: %w", pattern, err)
	}

	var best *semver.Version
	for _, s := range versions {
		v, err := semver.NewVersion(s)
		if err != nil || !constraint.Check(v) {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
		}
	}
	if best == nil {
		return "", fmt.Errorf("no version matching %q found", pattern)
	}

	return best.Original(), nil
}

// LoadVersionsFile loads a versions.json file from disk.
func LoadVersionsFile(path string) (*registry.VersionsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read versions file: %w", err)
	}

	var versions registry.VersionsFile
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, fmt.Errorf("failed to parse versions file: %w", err)
	}

	return &versions, nil
}

// SaveVersionsFile saves a versions.json file to disk.
func SaveVersionsFile(path string, versions *registry.VersionsFile) error {
	data, err := json.MarshalIndent(versions, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal versions: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write versions file: %w", err)
	}

	return nil
}
