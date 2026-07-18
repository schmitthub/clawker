package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/schmitthub/clawker/internal/bundle/fetch"
)

// ErrSourceTraversal marks a marketplace plugin source whose relative path
// would escape the marketplace checkout.
var ErrSourceTraversal = errors.New("relative plugin source must not traverse outside the marketplace")

// Copy-lane installs: every harness other than Claude Code treats a skill as
// installed when its directory sits in the harness's skills dir, so install
// is fetch-the-plugin-source-then-copy. The marketplace repo is the single
// source of truth for WHAT ships — the same catalog the Claude lane resolves
// through the Claude CLI.
const (
	// MarketplaceGitURL is the canonical clone URL of the plugin marketplace repo.
	MarketplaceGitURL = "https://github.com/schmitthub/clawker-plugin.git"
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

// marketplacePluginSource accepts both marketplace source shapes: a relative
// path string (e.g. "./" or "./plugins/name") for a plugin that lives inside
// the marketplace repo itself, or a git object {url, path, ref, sha} for a
// plugin fetched from another repo.
type marketplacePluginSource struct {
	// RelPath is set when the source is a relative-path string; the plugin
	// root resolves against the marketplace checkout.
	RelPath string `json:"-"`

	URL  string `json:"url"`
	Path string `json:"path"`
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
}

func (s *marketplacePluginSource) UnmarshalJSON(data []byte) error {
	var rel string
	if err := json.Unmarshal(data, &rel); err == nil {
		if traversesOutside(rel) {
			return fmt.Errorf("relative plugin source %q: %w", rel, ErrSourceTraversal)
		}
		var src marketplacePluginSource
		src.RelPath = rel
		*s = src
		return nil
	}
	type object marketplacePluginSource // local alias sheds UnmarshalJSON to avoid recursion
	var obj object
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("parsing plugin source object: %w", err)
	}
	*s = marketplacePluginSource(obj)
	return nil
}

// traversesOutside reports whether the slash-separated relative path escapes
// its root: after cleaning, any remaining ".." path segment climbs out.
// Comparison is per segment, so names that merely contain dots (my..dir) pass.
func traversesOutside(rel string) bool {
	return slices.Contains(strings.Split(path.Clean(rel), "/"), "..")
}

// FetchedSkills is the result of resolving and fetching the plugin's source:
// the on-disk skills directory and the skill names it contains.
// Cleanup removes the temp checkout and is safe to call exactly once.
type FetchedSkills struct {
	Dir     string
	Names   []string
	Cleanup func()
}

// FetchPluginSkills clones the marketplace repo, resolves the plugin's source
// (a relative path inside the marketplace, or a git url + path + sha), fetches
// it, and returns the plugin's skills directory. The marketplace catalog — not
// any local checkout — decides what ships, keeping the copy lane on the same
// release the Claude lane installs.
func FetchPluginSkills(ctx context.Context, fetcher fetch.Fetcher) (*FetchedSkills, error) {
	tmp, err := os.MkdirTemp("", "clawker-skill-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) } // best-effort temp cleanup

	marketDir := filepath.Join(tmp, "marketplace")
	entry, err := resolvePluginSource(ctx, fetcher, marketDir)
	if err != nil {
		cleanup()
		return nil, err
	}

	var skillsDir string
	if entry.RelPath != "" {
		// Relative source: the plugin lives inside the marketplace repo we
		// already cloned.
		skillsDir = filepath.Join(marketDir, filepath.FromSlash(entry.RelPath), skillsSubdir)
	} else {
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
		skillsDir = filepath.Join(srcDir, filepath.FromSlash(entry.Path), skillsSubdir)
	}
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
			if p.Source.RelPath == "" && p.Source.URL == "" {
				return zero, fmt.Errorf("marketplace entry %q has no source", MarketplacePluginName)
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
		if _, statErr := os.Stat(filepath.Join(dir, e.Name(), skillManifestName)); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				continue // no skill manifest — not a skill dir
			}
			return nil, fmt.Errorf("checking skill %s: %w", e.Name(), statErr)
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no skills found under %s", dir)
	}
	return names, nil
}

// CopySkills installs each named skill from srcDir into dstDir, replacing any
// existing copy of the same skill wholesale so removals in the source
// propagate. Non-regular entries (symlinks, FIFOs) are skipped defensively
// for fetched repo content; the returned count reports how many were dropped
// so callers can surface it.
func CopySkills(srcDir, dstDir string, names []string) (int, error) {
	skipped := 0
	for _, name := range names {
		dst := filepath.Join(dstDir, name)
		if rmErr := os.RemoveAll(dst); rmErr != nil {
			return skipped, fmt.Errorf("replacing existing skill %s: %w", name, rmErr)
		}
		n, copyErr := copyDir(filepath.Join(srcDir, name), dst)
		skipped += n
		if copyErr != nil {
			return skipped, fmt.Errorf("copying skill %s: %w", name, copyErr)
		}
	}
	return skipped, nil
}

// RemoveSkills deletes each named skill directory from dstDir and returns the
// names that were actually present. Missing entries are not errors — remove
// is idempotent.
func RemoveSkills(dstDir string, names []string) ([]string, error) {
	var removed []string
	for _, name := range names {
		dir := filepath.Join(dstDir, name)
		if _, statErr := os.Stat(dir); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				continue // already absent — nothing to remove
			}
			return removed, fmt.Errorf("checking skill %s: %w", name, statErr)
		}
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			return removed, fmt.Errorf("removing skill %s: %w", name, rmErr)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// copyDir mirrors the regular files and directories under src into dst,
// preserving file permission bits (skill scripts keep their exec bits).
// It returns how many non-regular entries were skipped.
func copyDir(src, dst string) (int, error) {
	skipped := 0
	walkErr := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", p, err)
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return fmt.Errorf("relativizing %s: %w", p, relErr)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if mkErr := os.MkdirAll(target, 0o750); mkErr != nil {
				return fmt.Errorf("creating %s: %w", target, mkErr)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			skipped++
			return nil
		}
		return copyFile(p, target, d)
	})
	//nolint:wrapcheck // the WalkDirFunc above wraps every error it returns
	return skipped, walkErr
}

// copyFile copies src to dst, creating dst with src's permission bits. The
// destination tree is freshly created (CopySkills removes it first), so the
// bits apply verbatim.
func copyFile(src, dst string, d os.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return fmt.Errorf("stating %s: %w", src, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer func() { _ = in.Close() }() // read-side close after full copy is unactionable
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
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
