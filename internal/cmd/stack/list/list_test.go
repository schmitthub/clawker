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
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	listcmd "github.com/schmitthub/clawker/internal/cmd/stack/list"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
)

func newFactory(t *testing.T, cfg config.Config) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios, _, out, _ := iostreams.Test()
	mgr := bundle.NewManager(cfg)
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

// plantCachedBundle plants a cache entry carrying one stack.
func plantCachedBundle(t *testing.T, ns, name, version, url string) {
	t.Helper()
	bundletest.PlantCachedBundle(t, ns, name, version, url,
		map[string]string{"stacks/x/stack.yaml": "description: x\n"})
}

func TestStackList_FloorOnly(t *testing.T) {
	testenv.New(t)
	f, out := newFactory(t, configmocks.NewBlankConfig())

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	s := out.String()
	assert.Contains(t, s, "NAME")
	assert.Contains(t, s, "node")
	assert.Contains(t, s, "built-in")
	// Stacks only — no harness rows.
	assert.NotContains(t, s, "claude")
}

func TestStackList_ShadowMarker(t *testing.T) {
	env := testenv.New(t)
	looseDir := filepath.Join(env.Dirs.Config, "stacks", "node")
	require.NoError(t, os.MkdirAll(looseDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(looseDir, "stack.yaml"), []byte("description: local node\n"), 0o644))

	f, out := newFactory(t, configmocks.NewBlankConfig())
	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), "! shadows built-in")
}

// A bundle-sourced stack lists its qualified name, the owning bundle's version,
// and — for cleanup/provenance — the owning bundle identity in the source.
func TestStackList_BundleStackNamesItsBundle(t *testing.T) {
	testenv.New(t)
	plantCachedBundle(t, "acme", "tools", "1.0.0", "https://example.com/acme/tools.git")

	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{{
			Source: config.BundleSource{
				URL: "https://example.com/acme/tools.git", Ref: "v1", SHA: "", Path: "", AutoUpdate: false,
			},
			File: "clawker.yaml",
		}}
	}
	f, out := newFactory(t, cfg)

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	s := out.String()
	assert.Contains(t, s, "acme.tools.x")
	assert.Contains(t, s, "1.0.0")
	assert.Contains(t, s, "bundle acme.tools")
}

func TestStackList_JSON(t *testing.T) {
	testenv.New(t)
	plantCachedBundle(t, "acme", "tools", "1.0.0", "https://example.com/acme/tools.git")

	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{{
			Source: config.BundleSource{
				URL: "https://example.com/acme/tools.git", Ref: "v1", SHA: "", Path: "", AutoUpdate: false,
			},
			File: "clawker.yaml",
		}}
	}
	f, out := newFactory(t, cfg)

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{"--json"})
	require.NoError(t, cmd.Execute())

	s := strings.TrimSpace(out.String())
	assert.True(t, strings.HasPrefix(s, "["))
	assert.Contains(t, s, "\"name\":\"acme.tools.x\"")
	assert.Contains(t, s, "\"version\":\"1.0.0\"")
	assert.Contains(t, s, "\"bundle\":\"acme.tools\"")
}
