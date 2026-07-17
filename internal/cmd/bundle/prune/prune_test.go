package prune_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	"github.com/schmitthub/clawker/internal/bundle/componentcheck"
	prunecmd "github.com/schmitthub/clawker/internal/cmd/bundle/prune"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
)

// newFactory wires a prune-capable factory: a Manager over the given
// declarations plus a registered-roots provider listing rootDirs.
func newFactory(
	t *testing.T, decls []config.BundleDeclaration, rootDirs ...string,
) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration { return decls }
	mgr := bundle.NewManager(cfg, componentcheck.Validate, bundle.WithRegisteredRoots(
		func(context.Context) ([]string, error) { return rootDirs, nil }))
	//nolint:exhaustruct // test factory carries only the nouns prune uses
	f := &cmdutil.Factory{
		IOStreams:     ios,
		TUI:           tui.NewTUI(ios),
		BundleManager: func() (*bundle.Manager, error) { return mgr, nil },
	}
	return f, out, errOut
}

func declaration(url, ref string) config.BundleDeclaration {
	return config.BundleDeclaration{
		Source: config.BundleSource{URL: url, Ref: ref, SHA: "", Path: "", AutoUpdate: false},
		File:   "clawker.yaml",
	}
}

// plant plants a one-stack acme.tools cache entry for src.
func plant(t *testing.T, version string, src bundle.Source) {
	t.Helper()
	bundletest.PlantCachedBundleSource(t, "acme", "tools", version, src,
		map[string]string{"stacks/x/stack.yaml": "description: x\n"})
}

func TestBundlePrune_DropsStaleKeepsRootedAndUnmanaged(t *testing.T) {
	testenv.New(t)
	live := bundle.Source{URL: "https://example.com/acme/tools.git", Ref: "v2", SHA: "", Path: ""}
	stale := bundle.Source{URL: "https://example.com/acme/tools.git", Ref: "v1", SHA: "", Path: ""}
	plant(t, "2.0.0", live)
	plant(t, "1.0.0", stale)
	bundletest.PlantCachedBundle(t, "hand", "placed", "0.1.0", "",
		map[string]string{"stacks/x/stack.yaml": "description: x\n"})

	f, out, errOut := newFactory(t, []config.BundleDeclaration{declaration(live.URL, "v2")})
	cmd := prunecmd.NewCmdPrune(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.DirExists(t, filepath.Join(cacheRoot, "acme", "tools", live.Key()))
	assert.NoDirExists(t, filepath.Join(cacheRoot, "acme", "tools", stale.Key()))
	assert.DirExists(t, filepath.Join(cacheRoot, "hand", "placed", "handplaced00"),
		"a hand-placed entry is not refetchable and must never be collected")

	stdout := out.String()
	assert.Contains(t, stdout, "Removed acme.tools cache entry "+stale.Key())
	assert.Contains(t, stdout, stale.Canonical())
	assert.NotContains(t, stdout, live.Key())
	assert.Empty(t, errOut.String())
}

func TestBundlePrune_NothingToPrune(t *testing.T) {
	testenv.New(t)
	live := bundle.Source{URL: "https://example.com/acme/tools.git", Ref: "v2", SHA: "", Path: ""}
	plant(t, "2.0.0", live)

	f, out, errOut := newFactory(t, []config.BundleDeclaration{declaration(live.URL, "v2")})
	cmd := prunecmd.NewCmdPrune(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Empty(t, out.String())
	assert.Contains(t, errOut.String(), "nothing to prune")
}

func TestBundlePrune_WarnsOnMultiRepositoryIdentity(t *testing.T) {
	testenv.New(t)
	mine := bundle.Source{URL: "https://example.com/acme/tools.git", Ref: "v1", SHA: "", Path: ""}
	theirs := bundle.Source{URL: "git@github.com:fork/tools.git", Ref: "v1", SHA: "", Path: ""}
	plant(t, "1.0.0", mine)
	plant(t, "1.0.0", theirs)

	otherRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(otherRoot, "."+consts.ProjectConfigFile),
		[]byte("bundles:\n  - url: git@github.com:fork/tools.git\n    ref: v1\n"), 0o644))

	f, _, errOut := newFactory(t,
		[]config.BundleDeclaration{declaration(mine.URL, "v1")}, otherRoot)
	cmd := prunecmd.NewCmdPrune(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	stderr := errOut.String()
	assert.Contains(t, stderr, "acme.tools is cached from 2 different repositories")
	assert.Contains(t, stderr, "https://example.com/acme/tools.git")
	assert.Contains(t, stderr, "git@github.com:fork/tools.git")
	assert.Contains(t, stderr, "clawker.yaml")
	assert.Contains(t, stderr, filepath.Join(otherRoot, "."+consts.ProjectConfigFile))
}

func TestBundlePrune_RootsUnavailableFails(t *testing.T) {
	testenv.New(t)
	stale := bundle.Source{URL: "https://example.com/acme/tools.git", Ref: "v1", SHA: "", Path: ""}
	plant(t, "1.0.0", stale)

	ios, _, _, errOut := iostreams.Test()
	cfg := configmocks.NewBlankConfig()
	mgr := bundle.NewManager(cfg, componentcheck.Validate) // no roots provider — GC must refuse
	//nolint:exhaustruct // test factory carries only the nouns prune uses
	f := &cmdutil.Factory{
		IOStreams:     ios,
		TUI:           tui.NewTUI(ios),
		BundleManager: func() (*bundle.Manager, error) { return mgr, nil },
	}
	cmd := prunecmd.NewCmdPrune(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetErr(&bytes.Buffer{})
	require.Error(t, cmd.Execute())
	assert.Empty(t, errOut.String(), "a refused prune reports nothing before failing")

	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.DirExists(t, filepath.Join(cacheRoot, "acme", "tools", stale.Key()))
}
