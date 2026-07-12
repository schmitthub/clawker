package list_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	listcmd "github.com/schmitthub/clawker/internal/cmd/bundle/list"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
)

func newFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios, _, out, _ := iostreams.Test()
	mgr := bundle.NewManager(configmocks.NewBlankConfig())
	f := &cmdutil.Factory{
		Version:         "",
		IOStreams:       ios,
		TUI:             tui.NewTUI(ios),
		Client:          nil,
		Config:          nil,
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
		BundleManager:   func() (*bundle.Manager, error) { return mgr, nil },
	}
	return f, out
}

func TestBundleList_FloorComponents(t *testing.T) {
	testenv.New(t)
	f, out := newFactory(t)

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	s := out.String()
	assert.Contains(t, s, "claude")
	assert.Contains(t, s, "node")
	assert.Contains(t, s, "built-in")
}

func TestBundleList_ShadowMarker(t *testing.T) {
	env := testenv.New(t)
	looseDir := filepath.Join(env.Dirs.Config, "stacks", "node")
	require.NoError(t, os.MkdirAll(looseDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(looseDir, "stack.yaml"), []byte("description: local node\n"), 0o644))

	f, out := newFactory(t)
	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), "shadows built-in")
}

func TestBundleList_JSON(t *testing.T) {
	testenv.New(t)
	f, out := newFactory(t)

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{"--json"})
	require.NoError(t, cmd.Execute())

	s := strings.TrimSpace(out.String())
	assert.True(t, strings.HasPrefix(s, "["))
	assert.Contains(t, s, "\"address\"")
	assert.Contains(t, s, "\"type\"")
}

// plantCachedBundle writes a cache entry: a version content root (manifest +
// one stack), and optionally the source.yaml linking it to url.
func plantCachedBundle(t *testing.T, ns, name, version, url string) {
	t.Helper()
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	verRoot := filepath.Join(cacheRoot, ns, name, version)
	require.NoError(t, os.MkdirAll(filepath.Join(verRoot, ".clawker-bundle"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(verRoot, ".clawker-bundle", "bundle.yaml"),
		[]byte("namespace: "+ns+"\nname: "+name+"\nversion: "+version+"\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(verRoot, "stacks", "x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(verRoot, "stacks", "x", "stack.yaml"),
		[]byte("description: x\n"), 0o644))
	if url == "" {
		return
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(cacheRoot, ns, name, "source.yaml"),
		[]byte(
			"url: "+url+"\nref: v1\nversions:\n  \""+version+"\":\n    sha: \"\"\n    fetched_at: 2026-01-01T00:00:00Z\n",
		),
		0o644,
	))
}

func declaration(url string) config.BundleDeclaration {
	return config.BundleDeclaration{
		Source: config.BundleSource{URL: url, Ref: "v1", SHA: "", Path: "", AutoUpdate: false},
		File:   "clawker.yaml",
	}
}

// The listing links declarations to the cache with one honest row per bundle:
// resolving, declared-but-uncached, cached-but-undeclared, and hand-placed —
// with actionable states repeated as stderr hints.
func TestBundleList_BundleStatusRows(t *testing.T) {
	testenv.New(t)
	plantCachedBundle(t, "acme", "tools", "1.0.0", "https://example.com/acme/tools.git")
	plantCachedBundle(t, "acme", "extra", "2.0.0", "https://example.com/acme/extra.git")
	plantCachedBundle(t, "hand", "placed", "0.1.0", "")

	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{
			declaration("https://example.com/acme/tools.git"),
			declaration("https://example.com/acme/missing.git"),
		}
	}
	mgr := bundle.NewManager(cfg)
	ios, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		Version:         "",
		IOStreams:       ios,
		TUI:             tui.NewTUI(ios),
		Client:          nil,
		Config:          nil,
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
		BundleManager:   func() (*bundle.Manager, error) { return mgr, nil },
	}

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	stdout := out.String()
	// The declared+cached bundle resolves: its component row and a resolving
	// bundle row.
	assert.Contains(t, stdout, "acme.tools.x")
	assert.Contains(t, stdout, "installed (clawker.yaml)")
	// The undeclared cached bundle contributes NO component row, only status.
	assert.NotContains(t, stdout, "acme.extra.x")
	assert.Contains(t, stdout, "cached, not declared")
	assert.Contains(t, stdout, "cached, unmanaged (no source metadata)")
	assert.Contains(t, stdout, "declared, not installed")

	stderr := errOut.String()
	assert.Contains(t, stderr, "is not installed — run `clawker bundle install`")
	assert.Contains(t, stderr, "bundle acme.extra is cached but no longer declared")
	assert.Contains(t, stderr, "bundle hand.placed is cached without source metadata")
}
