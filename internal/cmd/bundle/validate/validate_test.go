package validate_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	validatecmd "github.com/schmitthub/clawker/internal/cmd/bundle/validate"
	"github.com/schmitthub/clawker/internal/cmdutil"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func newFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	mgr := bundle.NewManager(configmocks.NewBlankConfig())
	f := &cmdutil.Factory{
		Version:         "",
		IOStreams:       ios,
		TUI:             nil,
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
	return f, out, errOut
}

func writeBundleDir(t *testing.T, dir, manifest string) {
	t.Helper()
	markerDir := filepath.Join(dir, bundle.MarkerDir)
	require.NoError(t, os.MkdirAll(markerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(markerDir, bundle.ManifestFile), []byte(manifest), 0o644))
}

func run(t *testing.T, f *cmdutil.Factory, args ...string) error {
	t.Helper()
	cmd := validatecmd.NewCmdValidate(f, nil)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestValidate_Valid(t *testing.T) {
	dir := t.TempDir()
	writeBundleDir(t, dir, "namespace: acme\nname: tools\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "stacks", "node"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "stacks", "node", "stack.yaml"), []byte("description: n\n"), 0o644))

	f, out, _ := newFactory(t)
	require.NoError(t, run(t, f, dir))
	assert.Contains(t, out.String(), "is valid")
}

func TestValidate_MalformedIsFailure(t *testing.T) {
	dir := t.TempDir()
	writeBundleDir(t, dir, "namespace: acme\n") // missing name

	f, _, errOut := newFactory(t)
	err := run(t, f, dir)
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.NotEmpty(t, errOut.String())
}

func TestValidate_StrictWarningsFail(t *testing.T) {
	dir := t.TempDir()
	writeBundleDir(t, dir, "namespace: acme\nname: tools\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o755)) // unknown dir → warning

	f, _, _ := newFactory(t)

	// Non-strict: warnings, but success.
	require.NoError(t, run(t, f, dir))

	// Strict: the warning is a failure.
	f2, _, errOut2 := newFactory(t)
	err := run(t, f2, dir, "--strict")
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, errOut2.String(), "warning")
}
