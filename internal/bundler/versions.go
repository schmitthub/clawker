package bundler

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/bundler/registry"
	"github.com/schmitthub/clawker/internal/config"
)

const (
	// ClaudeCodePackage is the npm package name for Claude Code.
	ClaudeCodePackage = "@anthropic-ai/claude-code"
)

// VersionsManager handles npm version discovery and resolution for harness packages.
type VersionsManager struct {
	fetcher registry.Fetcher
	config  *VariantConfig
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

// ResolveHarnessVersion resolves the concrete version rendered into the
// harness template's version ARG, per the bundle manifest's version spec:
//
//   - npm: the package's "latest" dist-tag, resolved through the supplied
//     [http.Client] (same contract as ResolveLatestHarnessVersion).
//   - github-release: the repo's latest release tag via the GitHub API,
//     with the manifest's tag prefix stripped (e.g. rust-v0.50.0 → 0.50.0).
//   - none: the literal DefaultHarnessVersion tag — the harness template
//     either ignores the value or treats it as a floating tag.
//
// On resolution failure returns the "latest" literal plus the underlying
// error so callers can warn; the build still works with a floating tag.
func ResolveHarnessVersion(ctx context.Context, httpClient *http.Client, b *Bundle) (string, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	spec := b.Manifest.Version
	switch spec.Resolver {
	case "", config.ResolverNone:
		return DefaultHarnessVersion, nil
	case config.ResolverNPM:
		return resolveNPMLatest(ctx, httpClient, spec.Package)
	case config.ResolverGitHubRelease:
		return resolveGitHubLatest(ctx, httpClient, b.Name, spec)
	default:
		return DefaultHarnessVersion, fmt.Errorf(
			"harness %q: unsupported version resolver %q",
			b.Name,
			spec.Resolver,
		)
	}
}

// resolveNPMLatest resolves pkg's "latest" dist-tag from the npm registry.
func resolveNPMLatest(ctx context.Context, httpClient *http.Client, pkg string) (string, error) {
	fetcher := registry.NewNPMClient(registry.WithHTTPClient(httpClient))
	mgr := NewVersionsManagerWithFetcher(fetcher, nil)
	vf, err := mgr.ResolveVersions(
		ctx,
		[]string{DefaultHarnessVersion},
		ResolveOptions{Package: pkg, Debug: false, Output: nil},
	)
	if err != nil {
		return DefaultHarnessVersion, err
	}
	if len(*vf) != 1 {
		return DefaultHarnessVersion, fmt.Errorf(
			"expected 1 resolved version for %q, got %d",
			pkg,
			len(*vf),
		)
	}
	for v := range *vf {
		return v, nil
	}
	return DefaultHarnessVersion, ErrNoVersions
}

// resolveGitHubLatest resolves the repo's latest release tag via the GitHub
// API, stripping the manifest's tag prefix.
func resolveGitHubLatest(
	ctx context.Context,
	httpClient *http.Client,
	name string,
	spec config.VersionSpec,
) (string, error) {
	if spec.Package == "" {
		return DefaultHarnessVersion, fmt.Errorf(
			"harness %q: github-release resolver requires package (owner/repo)",
			name,
		)
	}
	client := registry.NewGitHubReleaseClient(registry.WithGitHubHTTPClient(httpClient))
	v, err := client.LatestVersion(ctx, spec.Package, spec.TagPrefix)
	if err != nil {
		return DefaultHarnessVersion, fmt.Errorf(
			"harness %q: resolve github release for %q: %w",
			name, spec.Package, err,
		)
	}
	return v, nil
}

// ResolveOptions configures version resolution behavior.
type ResolveOptions struct {
	// Debug enables verbose output during resolution.
	Debug bool
	// Output is the writer for informational messages. Defaults to io.Discard if nil.
	Output io.Writer
	// Package is the npm package whose versions are resolved. Empty means
	// ClaudeCodePackage.
	Package string
}

// pkg returns the configured package, defaulting to ClaudeCodePackage.
func (o ResolveOptions) pkg() string {
	if o.Package != "" {
		return o.Package
	}
	return ClaudeCodePackage
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
func (m *VersionsManager) ResolveVersions(
	ctx context.Context,
	patterns []string,
	opts ResolveOptions,
) (*registry.VersionsFile, error) {
	// Fetch all available versions and dist-tags
	allVersions, err := m.fetcher.FetchVersions(ctx, opts.pkg())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions: %w", err)
	}

	distTags, err := m.fetcher.FetchDistTags(ctx, opts.pkg())
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
		fullVersion, resolveErr := m.resolvePattern(pattern, allVersions, distTags)
		if resolveErr != nil {
			fmt.Fprintf(out, "warning: %v\n", resolveErr)
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
//
//nolint:gocognit
func (m *VersionsManager) resolvePattern(
	pattern string,
	versions []string,
	distTags registry.DistTags,
) (string, error) {
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
