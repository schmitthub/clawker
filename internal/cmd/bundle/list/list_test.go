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
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
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
