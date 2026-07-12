package install_test

import (
	"bytes"
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

// seedBundle authors a minimal single-stack bundle in the fixture and returns
// its http clone URL.
func seedBundle(t *testing.T, srv *bundletest.Server, name string) string {
	t.Helper()
	repo := srv.InitRepo(t, name)
	repo.Commit(t, "init", map[string]string{
		".clawker-bundle/bundle.yaml": "namespace: acme\nname: " + name + "\nversion: 1.0.0\n",
		"stacks/node/stack.yaml":      "description: node\n",
	})
	repo.Tag(t, "v1.0.0")
	return srv.HTTPURL(name)
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
			return bundle.NewManager(cfg), nil
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
	url := seedBundle(t, srv, "tools")
	f, out, _ := newFactory(t)

	require.NoError(t, run(t, f, url, "--ref", "v1.0.0"))

	assert.Contains(t, out.String(), "Declared bundle source")
	assert.Contains(t, out.String(), "Fetched bundle content")

	path, err := consts.UserProjectConfigFilePath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, url)
	assert.Contains(t, body, "v1.0.0")

	// The prefetch populated the cache.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", "1.0.0", "stacks", "node", "stack.yaml"))
}

func TestInstall_Idempotent(t *testing.T) {
	testenv.New(t)
	srv := bundletest.New(t)
	url := seedBundle(t, srv, "tools")
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

// An unpinned remote source (no --ref/--sha) is legal: the entry is written
// url-only and the prefetch clones the repository's default branch tip.
func TestInstall_RemoteUnpinned_TracksDefaultBranch(t *testing.T) {
	testenv.New(t)
	srv := bundletest.New(t)
	url := seedBundle(t, srv, "tools")
	f, out, _ := newFactory(t)

	require.NoError(t, run(t, f, url))
	assert.Contains(t, out.String(), "Declared bundle source")
	assert.Contains(t, out.String(), "Fetched bundle content")

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

	// The prefetch populated the cache from the default branch tip.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", "1.0.0", "stacks", "node", "stack.yaml"))
}
