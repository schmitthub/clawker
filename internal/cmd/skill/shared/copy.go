package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/bundle/fetch"
)

// Copy-lane installs: every harness other than Claude Code treats a skill as
// installed when its directory sits in the harness's skills dir, so install
// is fetch-the-pinned-plugin-source-then-copy. The marketplace repo is the
// single source of truth for WHICH commit ships — the same pin the Claude
// lane resolves through the Claude CLI.
const (
	// MarketplaceGitURL is the canonical clone URL of the plugin marketplace repo.
	MarketplaceGitURL = "https://github.com/schmitthub/claude-plugins.git"
	// MarketplaceManifestPath locates the marketplace manifest within that repo.
	MarketplaceManifestPath = ".claude-plugin/marketplace.json"
	// MarketplacePluginName is the plugin's entry name in the manifest.
	MarketplacePluginName = "clawker-support"

	// skillsSubdir is the convention directory inside the plugin that holds
	// one folder per skill.
	skillsSubdir = "skills"
	// skillManifestName marks a directory as a skill.
	skillManifestName = "SKILL.md"

	// Native per-harness skills locations, relative to the user's home unless
	// anchored by an env var the harness itself honors.
	codexSkillsRel       = ".agents/skills"
	piSkillsRel          = ".pi/agent/skills"
	opencodeConfigDirEnv = "OPENCODE_CONFIG_DIR"
	opencodeConfigRel    = ".config/opencode"
)

// SkillsDir returns the harness's native skills directory for copy-lane
// installs. Claude is not a copy-lane harness and returns an error.
func SkillsDir(harness string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	switch harness {
	case HarnessCodex:
		return filepath.Join(home, codexSkillsRel), nil
	case HarnessOpencode:
		configDir := os.Getenv(opencodeConfigDirEnv)
		if configDir == "" {
			configDir = filepath.Join(home, opencodeConfigRel)
		}
		return filepath.Join(configDir, skillsSubdir), nil
	case HarnessPi:
		return filepath.Join(home, piSkillsRel), nil
	default:
		return "", fmt.Errorf("harness %q does not install by file copy", harness)
	}
}

// marketplaceManifest mirrors the marketplace.json subset the copy lane needs.
type marketplaceManifest struct {
	Plugins []marketplacePlugin `json:"plugins"`
}

type marketplacePlugin struct {
	Name   string                  `json:"name"`
	Source marketplacePluginSource `json:"source"`
}

type marketplacePluginSource struct {
	URL  string `json:"url"`
	Path string `json:"path"`
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
}

// FetchedSkills is the result of resolving and fetching the plugin's pinned
// source: the on-disk skills directory and the skill names it contains.
// Cleanup removes the temp checkout and is safe to call exactly once.
type FetchedSkills struct {
	Dir     string
	Names   []string
	Cleanup func()
}

// FetchPluginSkills clones the marketplace repo, resolves the plugin's pinned
// source (url + path + sha), fetches it, and returns the plugin's skills
// directory. The marketplace pin — not any local checkout — decides what
// ships, keeping the copy lane on the same release the Claude lane installs.
func FetchPluginSkills(ctx context.Context, fetcher fetch.Fetcher) (*FetchedSkills, error) {
	tmp, err := os.MkdirTemp("", "clawker-skill-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) } // best-effort temp cleanup

	entry, err := resolvePluginSource(ctx, fetcher, filepath.Join(tmp, "marketplace"))
	if err != nil {
		cleanup()
		return nil, err
	}

	srcDir := filepath.Join(tmp, "plugin")
	if _, cloneErr := fetcher.Clone(ctx, fetch.CloneOptions{
		URL: entry.URL,
		Ref: entry.Ref,
		SHA: entry.SHA,
		Dir: srcDir,
	}); cloneErr != nil {
		cleanup()
		return nil, fmt.Errorf("fetching plugin source %s: %w", entry.URL, cloneErr)
	}

	skillsDir := filepath.Join(srcDir, filepath.FromSlash(entry.Path), skillsSubdir)
	names, err := skillNames(skillsDir)
	if err != nil {
		cleanup()
		return nil, err
	}
	return &FetchedSkills{Dir: skillsDir, Names: names, Cleanup: cleanup}, nil
}

func resolvePluginSource(ctx context.Context, fetcher fetch.Fetcher, dir string) (marketplacePluginSource, error) {
	var zero marketplacePluginSource
	if _, err := fetcher.Clone(
		ctx,
		fetch.CloneOptions{URL: MarketplaceGitURL, Ref: "", SHA: "", Dir: dir},
	); err != nil {
		return zero, fmt.Errorf("fetching marketplace %s: %w", MarketplaceGitURL, err)
	}
	data, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(MarketplaceManifestPath)))
	if readErr != nil {
		return zero, fmt.Errorf("reading marketplace manifest: %w", readErr)
	}
	var manifest marketplaceManifest
	if unmarshalErr := json.Unmarshal(data, &manifest); unmarshalErr != nil {
		return zero, fmt.Errorf("parsing marketplace manifest: %w", unmarshalErr)
	}
	for _, p := range manifest.Plugins {
		if p.Name == MarketplacePluginName {
			if p.Source.URL == "" {
				return zero, fmt.Errorf("marketplace entry %q has no source url", MarketplacePluginName)
			}
			return p.Source, nil
		}
	}
	return zero, fmt.Errorf("plugin %q not found in marketplace manifest", MarketplacePluginName)
}

// skillNames lists the skill directories (those containing SKILL.md) exactly
// one level under dir — the flat layout every supported harness discovers.
func skillNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading skills dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(dir, e.Name(), skillManifestName)); statErr == nil {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no skills found under %s", dir)
	}
	return names, nil
}

// CopySkills installs each named skill from srcDir into dstDir, replacing any
// existing copy of the same skill wholesale so removals in the source
// propagate. Symlinks are skipped defensively.
func CopySkills(srcDir, dstDir string, names []string) error {
	for _, name := range names {
		dst := filepath.Join(dstDir, name)
		if rmErr := os.RemoveAll(dst); rmErr != nil {
			return fmt.Errorf("replacing existing skill %s: %w", name, rmErr)
		}
		if copyErr := copyDir(filepath.Join(srcDir, name), dst); copyErr != nil {
			return fmt.Errorf("copying skill %s: %w", name, copyErr)
		}
	}
	return nil
}

// RemoveSkills deletes each named skill directory from dstDir. Missing
// entries are not errors — remove is idempotent.
func RemoveSkills(dstDir string, names []string) error {
	for _, name := range names {
		if rmErr := os.RemoveAll(filepath.Join(dstDir, name)); rmErr != nil {
			return fmt.Errorf("removing skill %s: %w", name, rmErr)
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	//nolint:wrapcheck // the WalkDirFunc below wraps every error it returns
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", path, err)
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return fmt.Errorf("relativizing %s: %w", path, relErr)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if mkErr := os.MkdirAll(target, 0o750); mkErr != nil {
				return fmt.Errorf("creating %s: %w", target, mkErr)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer func() { _ = in.Close() }() // read-side close after full copy is unactionable
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}
	if _, copyErr := io.Copy(out, in); copyErr != nil {
		_ = out.Close() // the copy error is the one worth reporting
		return fmt.Errorf("copying to %s: %w", dst, copyErr)
	}
	if closeErr := out.Close(); closeErr != nil {
		return fmt.Errorf("closing %s: %w", dst, closeErr)
	}
	return nil
}
