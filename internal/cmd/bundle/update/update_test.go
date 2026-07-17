package update_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	"github.com/schmitthub/clawker/internal/bundle/componentcheck"
	updatecmd "github.com/schmitthub/clawker/internal/cmd/bundle/update"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
)

func newFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	testenv.New(t)
	ios, _, _, errOut := iostreams.Test()
	mgr := bundle.NewManager(configmocks.NewBlankConfig(), componentcheck.Validate)
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
	return f, errOut
}

func run(t *testing.T, f *cmdutil.Factory, args ...string) error {
	t.Helper()
	cmd := updatecmd.NewCmdUpdate(f, nil)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func TestUpdate_NoBundlesIsANoOp(t *testing.T) {
	// No declared or cached bundles: the update pass finds nothing and succeeds
	// without error (the real refetch pipeline is covered in the bundle package
	// integration tests).
	f, errOut := newFactory(t)
	require.NoError(t, run(t, f))
	assert.Empty(t, errOut.String())
}

func TestUpdate_NamedUndeclaredErrors(t *testing.T) {
	f, _ := newFactory(t)
	err := run(t, f, "acme.tools")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no declared source")
}

func TestUpdate_InvalidIdentity(t *testing.T) {
	f, _ := newFactory(t)
	err := run(t, f, "acme.tools.node")
	require.Error(t, err)
	assert.NotErrorIs(t, err, cmdutil.SilentError)
}

// TestUpdate_AutoGCReconcilesRefetchedIdentity proves the update verb's
// cache-maintenance half end to end: when the tracked tip moves and the entry
// refetches, the identity's stranded siblings (values nothing declares) are
// collected and reported.
func TestUpdate_AutoGCReconcilesRefetchedIdentity(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", map[string]string{
		".clawker-bundle/bundle.yaml": "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"stacks/node/stack.yaml":      "description: node\n", "stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
	})

	testenv.New(t)
	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: false}
	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{{Source: src, File: "clawker.yaml"}}
	}
	mgr := bundle.NewManager(cfg, componentcheck.Validate, bundle.WithRegisteredRoots(
		func(context.Context) ([]string, error) { return nil, nil }))
	_, _, err := mgr.Install(context.Background(), src)
	require.NoError(t, err)

	// A stranded sibling of the same identity, no longer declared anywhere.
	stranded := bundle.Source{URL: srv.HTTPURL("tools"), Ref: "v0", SHA: "", Path: ""}
	bundletest.PlantCachedBundleSource(
		t,
		"acme",
		"tools",
		"0.9.0",
		stranded,
		map[string]string{
			"stacks/node/stack.yaml":                 "description: node\n",
			"stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
		},
	)

	// The tracked tip moves, so the update refetches — the AutoGC trigger.
	repo.Commit(t, "v2", map[string]string{
		".clawker-bundle/bundle.yaml": "namespace: acme\nname: tools\nversion: 2.0.0\n",
	})

	ios, _, out, errOut := iostreams.Test()
	//nolint:exhaustruct // test factory carries only the nouns update uses
	f := &cmdutil.Factory{
		IOStreams:     ios,
		BundleManager: func() (*bundle.Manager, error) { return mgr, nil },
	}
	require.NoError(t, run(t, f))

	assert.Contains(t, out.String(), "updated to version 2.0.0")
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(cacheRoot, "acme", "tools", stranded.Key()),
		"a refetch must reconcile the identity's stranded siblings")
	assert.Contains(t, errOut.String(), "removed stale cache entry of acme.tools")
}
