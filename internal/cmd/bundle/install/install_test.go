package install_test

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
	installcmd "github.com/schmitthub/clawker/internal/cmd/bundle/install"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
)

// seedBundle authors a minimal single-stack acme.tools bundle in the fixture
// and returns its http clone URL.
func seedBundle(t *testing.T, srv *bundletest.Server) string {
	t.Helper()
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "init", map[string]string{
		".clawker-bundle/bundle.yaml": "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"stacks/node/stack.yaml":      "description: node\n",
	})
	repo.Tag(t, "v1.0.0")
	return srv.HTTPURL("tools")
}

// newFactory builds a Factory whose Config and BundleManager load a fresh
// config each call — mirroring one CLI invocation per Execute, so a file
// written by one run is discovered by the next.
func newFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		Version:   "",
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		Client:    nil,
		Config: func() (config.Config, error) {
			return config.NewConfig()
		},
		Logger:          nil,
		CLIState:        nil,
		ProjectRegistry: nil,
		ProjectManager:  nil,
		GitManager:      nil,
		HostProxy:       nil,
		SocketBridge:    nil,
		Prompter:        nil,
		AdminClient:     nil,
		ControlPlane:    nil,
		HttpClient:      nil,
		BundleManager: func() (*bundle.Manager, error) {
			cfg, err := config.NewConfig()
			if err != nil {
				return nil, err
			}
			// A no-registered-projects roots provider: GC is ON, rooted by the
			// current (testenv-isolated) config's declarations alone — exactly
			// the production wiring on a host with an empty registry.
			return bundle.NewManager(cfg, bundle.WithRegisteredRoots(
				func(context.Context) ([]string, error) { return nil, nil })), nil
		},
	}
	return f, out, errOut
}

func run(t *testing.T, f *cmdutil.Factory, args ...string) error {
	t.Helper()
	cmd := installcmd.NewCmdInstall(f, nil)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestInstall_WritesUserLayerAndFetches(t *testing.T) {
	testenv.New(t)
	srv := bundletest.New(t)
	url := seedBundle(t, srv)
	f, out, _ := newFactory(t)

	require.NoError(t, run(t, f, url, "--ref", "v1.0.0"))

	assert.Contains(t, out.String(), "Declared bundle source")
	assert.Contains(t, out.String(), "Fetched acme.tools into the cache")

	path, err := consts.UserProjectConfigFilePath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, url)
	assert.Contains(t, body, "v1.0.0")

	// The prefetch populated the value-keyed cache entry for the declaration.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	key := bundle.Source{URL: url, Ref: "v1.0.0", SHA: "", Path: ""}.Key()
	assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", key, "stacks", "node", "stack.yaml"))
}

func TestInstall_Idempotent(t *testing.T) {
	testenv.New(t)
	srv := bundletest.New(t)
	url := seedBundle(t, srv)
	f, _, errOut := newFactory(t)

	require.NoError(t, run(t, f, url, "--ref", "v1.0.0"))
	require.NoError(t, run(t, f, url, "--ref", "v1.0.0"))

	assert.Contains(t, errOut.String(), "already declared")

	// The source appears exactly once.
	cfg, err := config.NewConfig()
	require.NoError(t, err)
	count := 0
	for _, d := range cfg.BundleDeclarations() {
		if d.Source.URL == url {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

// --project outside any project dir is a hard error naming the fix; the
// default --user target needs no project and must keep working anywhere.
func TestInstall_ProjectFlagOutsideProject_Errors(t *testing.T) {
	testenv.New(t)
	f, _, _ := newFactory(t)

	err := run(t, f, "https://example.com/acme/tools.git", "--ref", "v1.0.0", "--project")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not inside a project")
	assert.Contains(t, err.Error(), "--user")

	// Nothing was declared anywhere: the user-layer config was not created.
	path, pathErr := consts.UserProjectConfigFilePath()
	require.NoError(t, pathErr)
	assert.NoFileExists(t, path)
}

// An unpinned remote source (no --ref/--sha) is legal: the entry is written
// url-only and the prefetch clones the repository's default branch tip.
func TestInstall_RemoteUnpinned_TracksDefaultBranch(t *testing.T) {
	testenv.New(t)
	srv := bundletest.New(t)
	url := seedBundle(t, srv)
	f, out, _ := newFactory(t)

	require.NoError(t, run(t, f, url))
	assert.Contains(t, out.String(), "Declared bundle source")
	assert.Contains(t, out.String(), "Fetched acme.tools into the cache")

	// The written entry carries the url and no pin.
	cfg, err := config.NewConfig()
	require.NoError(t, err)
	found := false
	for _, d := range cfg.BundleDeclarations() {
		if d.Source.URL == url {
			found = true
			assert.Empty(t, d.Source.Ref)
			assert.Empty(t, d.Source.SHA)
		}
	}
	assert.True(t, found, "the unpinned source is declared")

	// The prefetch populated the value-keyed entry for the unpinned declaration.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	key := bundle.Source{URL: url, Ref: "", SHA: "", Path: ""}.Key()
	assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", key, "stacks", "node", "stack.yaml"))
}

// TestInstall_AutoGCReconcilesEditedDeclaration proves the install verb's
// cache-maintenance half end to end: a declaration whose pin was edited leaves
// its OLD value-keyed entry stranded, and installing the new value collects it
// — the headline "edited ref no longer grows the cache forever" flow.
func TestInstall_AutoGCReconcilesEditedDeclaration(t *testing.T) {
	testenv.New(t)
	srv := bundletest.New(t)
	url := seedBundle(t, srv)

	// The entry a pre-edit declaration ({url, ref v0}) left behind. Nothing
	// declares this value anymore.
	stranded := bundle.Source{URL: url, Ref: "v0", SHA: "", Path: ""}
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "0.9.0", stranded,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})

	f, _, errOut := newFactory(t)
	require.NoError(t, run(t, f, url, "--ref", "v1.0.0"))

	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(cacheRoot, "acme", "tools", stranded.Key()),
		"install must reconcile the installed identity's stranded siblings")
	live := bundle.Source{URL: url, Ref: "v1.0.0", SHA: "", Path: ""}
	assert.DirExists(t, filepath.Join(cacheRoot, "acme", "tools", live.Key()))
	assert.Contains(t, errOut.String(), "removed stale cache entry of acme.tools")
	assert.Contains(t, errOut.String(), stranded.Key())
}
