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
	"github.com/schmitthub/clawker/internal/cmd/skill/shared"
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

	require.NoError(t, shared.CopySkills(src, dst, []string{"clawker-support"}))

	assert.FileExists(t, filepath.Join(dst, "clawker-support", "SKILL.md"))
	assert.FileExists(t, filepath.Join(dst, "clawker-support", "reference", "notes.md"))
	assert.NoFileExists(t, stale)
}

func TestRemoveSkills_DeletesAndIsIdempotent(t *testing.T) {
	dst := t.TempDir()
	writeSkill(t, dst, "clawker-support")

	require.NoError(t, shared.RemoveSkills(dst, []string{"clawker-support"}))
	assert.NoDirExists(t, filepath.Join(dst, "clawker-support"))

	// Second removal of a now-missing skill is not an error.
	require.NoError(t, shared.RemoveSkills(dst, []string{"clawker-support"}))
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

func TestFetchPluginSkills_ResolvesMarketplacePin(t *testing.T) {
	manifest := map[string]any{
		"plugins": []map[string]any{{
			"name": shared.MarketplacePluginName,
			"source": map[string]any{
				"url":  "https://example.com/clawker.git",
				"path": "claude-plugin/clawker-support",
				"sha":  "1234567890123456789012345678901234567890",
			},
		}},
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: marketplaceTree(t, manifestJSON),
		"https://example.com/clawker.git": func(dir string) error {
			skills := filepath.Join(dir, "claude-plugin", "clawker-support", "skills")
			writeSkill(t, skills, "clawker-support")
			// A stray file at the skills root must not be reported as a skill.
			return os.WriteFile(
				filepath.Join(skills, "README.md"),
				[]byte("x"),
				0o644,
			)
		},
	}}

	fetched, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.NoError(t, err)
	defer fetched.Cleanup()

	assert.Equal(t, []string{"clawker-support"}, fetched.Names)
	assert.FileExists(t, filepath.Join(fetched.Dir, "clawker-support", "SKILL.md"))
}

func TestFetchPluginSkills_UnknownPluginFails(t *testing.T) {
	fetcher := &fakeFetcher{trees: map[string]func(string) error{
		shared.MarketplaceGitURL: marketplaceTree(t, []byte(`{"plugins":[]}`)),
	}}

	_, err := shared.FetchPluginSkills(context.Background(), fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), shared.MarketplacePluginName)
}
