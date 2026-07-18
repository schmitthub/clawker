package shared_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle/fetch"
	"github.com/schmitthub/clawker/internal/cmd/plugin/shared"
)

func TestValidateHarness(t *testing.T) {
	for _, h := range shared.ValidHarnesses() {
		require.NoError(t, shared.ValidateHarness(h))
	}
	require.Error(t, shared.ValidateHarness("cursor"))
	require.Error(t, shared.ValidateHarness(""))
}

func TestSkillsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("codex", func(t *testing.T) {
		dir, err := shared.SkillsDir(shared.HarnessCodex)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, ".agents/skills"), dir)
	})

	t.Run("pi", func(t *testing.T) {
		dir, err := shared.SkillsDir(shared.HarnessPi)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, ".pi/agent/skills"), dir)
	})

	t.Run("opencode default", func(t *testing.T) {
		t.Setenv("OPENCODE_CONFIG_DIR", "")
		dir, err := shared.SkillsDir(shared.HarnessOpencode)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, ".config/opencode/skills"), dir)
	})

	t.Run("opencode honors OPENCODE_CONFIG_DIR", func(t *testing.T) {
		t.Setenv("OPENCODE_CONFIG_DIR", "/custom/opencode")
		dir, err := shared.SkillsDir(shared.HarnessOpencode)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/custom/opencode", "skills"), dir)
	})

	t.Run("claude is not a copy-lane harness", func(t *testing.T) {
		_, err := shared.SkillsDir(shared.HarnessClaude)
		require.Error(t, err)
	})
}

// writeSkill creates a minimal skill dir (SKILL.md + a reference file) under root.
func writeSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "reference"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nbody\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "reference", "notes.md"), []byte("ref"), 0o644))
}

func TestCopySkills_InstallsAndReplacesWholesale(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeSkill(t, src, "clawker-support")

	// Pre-existing stale content that must not survive the reinstall.
	stale := filepath.Join(dst, "clawker-support", "reference", "stale.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o755))
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o644))

	skipped, err := shared.CopySkills(src, dst, []string{"clawker-support"})
	require.NoError(t, err)
	assert.Zero(t, skipped)

	assert.FileExists(t, filepath.Join(dst, "clawker-support", "SKILL.md"))
	assert.FileExists(t, filepath.Join(dst, "clawker-support", "reference", "notes.md"))
	assert.NoFileExists(t, stale)
}

func TestCopySkills_PreservesExecBits(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeSkill(t, src, "clawker-support")
	script := filepath.Join(src, "clawker-support", "scripts", "run.sh")
	require.NoError(t, os.MkdirAll(filepath.Dir(script), 0o755))
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755))

	skipped, err := shared.CopySkills(src, dst, []string{"clawker-support"})
	require.NoError(t, err)
	assert.Zero(t, skipped)

	info, err := os.Stat(filepath.Join(dst, "clawker-support", "scripts", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestCopySkills_CountsSkippedNonRegularEntries(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeSkill(t, src, "clawker-support")
	link := filepath.Join(src, "clawker-support", "escape")
	require.NoError(t, os.Symlink("/etc/passwd", link))

	skipped, err := shared.CopySkills(src, dst, []string{"clawker-support"})
	require.NoError(t, err)
	assert.Equal(t, 1, skipped)
	assert.NoFileExists(t, filepath.Join(dst, "clawker-support", "escape"))
}

func TestRemoveSkills_DeletesAndIsIdempotent(t *testing.T) {
	dst := t.TempDir()
	writeSkill(t, dst, "clawker-support")

	removed, err := shared.RemoveSkills(dst, []string{"clawker-support"})
	require.NoError(t, err)
	assert.Equal(t, []string{"clawker-support"}, removed)
	assert.NoDirExists(t, filepath.Join(dst, "clawker-support"))

	// Second removal of a now-missing skill is not an error and reports
	// nothing removed.
	removed, err = shared.RemoveSkills(dst, []string{"clawker-support"})
	require.NoError(t, err)
	assert.Empty(t, removed)
}

// fakeFetcher satisfies fetch.Fetcher by materializing pre-registered trees
// keyed by URL into opts.Dir — no network, no git.
type fakeFetcher struct {
	trees map[string]func(dir string) error
}

func (f *fakeFetcher) ResolveRef(_ context.Context, _, _ string) (string, error) {
	return "deadbeef", nil
}

func (f *fakeFetcher) Clone(_ context.Context, opts fetch.CloneOptions) (string, error) {
	fill, ok := f.trees[opts.URL]
	if !ok {
		return "", os.ErrNotExist
	}
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return "", err
	}
	return "deadbeef", fill(opts.Dir)
}

func marketplaceTree(t *testing.T, manifestJSON []byte) func(dir string) error {
	t.Helper()
	return func(dir string) error {
		path := filepath.Join(dir, filepath.FromSlash(shared.MarketplaceManifestPath))
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(path, manifestJSON, 0o644)
	}
}

func TestFetchPluginSkills_ResolvesRelativeSource(t *testing.T) {
	// The live marketplace shape: the plugin lives at the marketplace repo
	// root and the entry's source is the relative-path string "./".
	manifest := map[string]any{
		"plugins": []map[string]any{{
			"name":   shared.MarketplacePluginName,
			"source": "./",
		}},
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: func(dir string) error {
			if fillErr := marketplaceTree(t, manifestJSON)(dir); fillErr != nil {
				return fillErr
			}
			skills := filepath.Join(dir, "skills")
			writeSkill(t, skills, "clawker-support")
			// A stray file at the skills root must not be reported as a skill.
			return os.WriteFile(filepath.Join(skills, "README.md"), []byte("x"), 0o644)
		},
	}}

	fetched, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.NoError(t, err)
	defer fetched.Cleanup()

	assert.Equal(t, []string{"clawker-support"}, fetched.Names)
	assert.FileExists(t, filepath.Join(fetched.Dir, "clawker-support", "SKILL.md"))
}

func TestFetchPluginSkills_ResolvesGitObjectSource(t *testing.T) {
	manifest := map[string]any{
		"plugins": []map[string]any{{
			"name": shared.MarketplacePluginName,
			"source": map[string]any{
				"url":  "https://example.com/clawker.git",
				"path": "plugins/clawker-support",
				"sha":  "1234567890123456789012345678901234567890",
			},
		}},
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: marketplaceTree(t, manifestJSON),
		"https://example.com/clawker.git": func(dir string) error {
			skills := filepath.Join(dir, "plugins", "clawker-support", "skills")
			writeSkill(t, skills, "clawker-support")
			return nil
		},
	}}

	fetched, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.NoError(t, err)
	defer fetched.Cleanup()

	assert.Equal(t, []string{"clawker-support"}, fetched.Names)
	assert.FileExists(t, filepath.Join(fetched.Dir, "clawker-support", "SKILL.md"))
}

func TestFetchPluginSkills_RejectsTraversingRelativeSource(t *testing.T) {
	manifest := []byte(`{"plugins":[{"name":"clawker-support","source":"../outside"}]}`)
	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: marketplaceTree(t, manifest),
	}}

	_, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.Error(t, err)
	assert.ErrorIs(t, err, shared.ErrSourceTraversal)
}

func TestFetchPluginSkills_AllowsDotDotInsideName(t *testing.T) {
	// ".." as a substring of a path segment is a legitimate name, not a
	// traversal — only a whole ".." segment climbs out.
	manifest := []byte(`{"plugins":[{"name":"clawker-support","source":"./my..dir"}]}`)
	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: func(dir string) error {
			if fillErr := marketplaceTree(t, manifest)(dir); fillErr != nil {
				return fillErr
			}
			writeSkill(t, filepath.Join(dir, "my..dir", "skills"), "clawker-support")
			return nil
		},
	}}

	fetched, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.NoError(t, err)
	defer fetched.Cleanup()
	assert.Equal(t, []string{"clawker-support"}, fetched.Names)
}

func TestFetchPluginSkills_EntryWithoutSourceFails(t *testing.T) {
	manifest := []byte(`{"plugins":[{"name":"clawker-support"}]}`)
	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: marketplaceTree(t, manifest),
	}}

	_, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no source")
}

func TestFetchPluginSkills_MalformedSourceFails(t *testing.T) {
	manifest := []byte(`{"plugins":[{"name":"clawker-support","source":42}]}`)
	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: marketplaceTree(t, manifest),
	}}

	_, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing plugin source object")
}

func TestFetchPluginSkills_UnknownPluginFails(t *testing.T) {
	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: marketplaceTree(t, []byte(`{"plugins":[]}`)),
	}}

	_, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), shared.MarketplacePluginName)
}
