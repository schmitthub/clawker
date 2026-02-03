package bundler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/bundler/registry"
	"github.com/schmitthub/clawker/internal/bundler/semver"
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

// ResolveOptions configures version resolution behavior.
type ResolveOptions struct {
	// Debug enables verbose output during resolution.
	Debug bool
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

	if opts.Debug {
		fmt.Printf("[DEBUG] Found %d versions\n", len(allVersions))
		fmt.Printf("[DEBUG] Dist-tags: %v\n", distTags)
	}

	result := make(registry.VersionsFile)

	for _, pattern := range patterns {
		fullVersion, err := m.resolvePattern(ctx, pattern, allVersions, distTags, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			continue
		}

		if opts.Debug {
			fmt.Printf("[DEBUG] Resolved %q -> %q\n", pattern, fullVersion)
		}

		// Parse and create version info
		v, err := semver.Parse(fullVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: invalid version format %q: %v\n", fullVersion, err)
			continue
		}

		info := registry.NewVersionInfo(v, m.config.DebianDefault, m.config.AlpineDefault, m.config.Variants)
		result[fullVersion] = info

		fmt.Printf("Full version for %s: %s\n", pattern, fullVersion)
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

	// Validate pattern is a valid semver (full or partial)
	if !semver.IsValid(pattern) {
		return "", fmt.Errorf("invalid version format %q", pattern)
	}

	// Try to match against available versions
	match, ok := semver.Match(versions, pattern)
	if !ok {
		return "", fmt.Errorf("no version matching %q found", pattern)
	}

	return match, nil
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
