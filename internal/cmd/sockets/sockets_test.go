package sockets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/db"
	dbmocks "github.com/schmitthub/clawker/internal/db/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

type commandFixture struct {
	factory      *cmdutil.Factory
	socketGrants socketGrantStoreFunc
	ios          *iostreams.IOStreams
	in           *bytes.Buffer
	out          *bytes.Buffer
	errOut       *bytes.Buffer
}

func newCommandFixture(store db.SocketGrantStore, cfg config.Config) *commandFixture {
	ios, in, out, errOut := iostreams.Test()
	if cfg == nil {
		cfg = configmocks.NewBlankConfig()
	}
	factory := &cmdutil.Factory{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		Config: func() (config.Config, error) {
			return cfg, nil
		},
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
		DB: func() (*db.DB, error) {
			return nil, errors.New("test command did not inject the socket grant store")
		},
		Prompter: func() *prompter.Prompter {
			return prompter.NewPrompter(ios)
		},
	}
	return &commandFixture{
		factory: factory,
		socketGrants: func() (db.SocketGrantStore, error) {
			return store, nil
		},
		ios: ios, in: in, out: out, errOut: errOut,
	}
}

func (f *commandFixture) execute(t *testing.T, args ...string) error {
	t.Helper()
	require.NotEmpty(t, args)
	var command *cobra.Command
	switch args[0] {
	case "list":
		command = newCmdList(f.factory, func(ctx context.Context, opts *ListOptions) error {
			opts.SocketGrants = f.socketGrants
			return listRun(ctx, opts)
		})
	case "info":
		command = newCmdInfo(f.factory, func(ctx context.Context, opts *InfoOptions) error {
			opts.SocketGrants = f.socketGrants
			return infoRun(ctx, opts)
		})
	case "revoke":
		command = newCmdRevoke(f.factory, func(ctx context.Context, opts *RevokeOptions) error {
			opts.SocketGrants = f.socketGrants
			return revokeRun(ctx, opts)
		})
	case "prune":
		command = newCmdPrune(f.factory, func(ctx context.Context, opts *PruneOptions) error {
			opts.SocketGrants = f.socketGrants
			return pruneRun(ctx, opts)
		})
	default:
		t.Fatalf("unknown test command %q", args[0])
	}
	command.SetArgs(args[1:])
	command.SetIn(f.in)
	command.SetOut(f.out)
	command.SetErr(f.errOut)
	return command.Execute()
}

func grant(id int64, harness, principal, hostPath, status, purpose string) db.SocketGrant {
	return db.SocketGrant{
		ID:          id,
		HarnessPath: principal,
		HarnessName: harness,
		HostPath:    hostPath,
		Status:      status,
		Identity: socketbridge.ListenerIdentity{
			UID:   1001,
			GID:   1002,
			Owner: "agent-owner",
			Group: "agent-group",
		},
		Purpose:   purpose,
		GrantedAt: time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC),
	}
}

func TestListCommand(t *testing.T) {
	grants := []db.SocketGrant{
		grant(1, "acme", "/harness/acme", "/run/acme.sock", db.GrantAllow, "Connect to Acme."),
		grant(2, "other", "/harness/other", "/run/other.sock", db.GrantDeny, "Connect to Other."),
	}
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return grants, nil },
	}
	fixture := newCommandFixture(store, nil)

	require.NoError(t, fixture.execute(t, "list"))

	output := fixture.out.String()
	assert.Contains(t, output, "ID")
	assert.Contains(t, output, "HARNESS")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "PURPOSE")
	assert.Contains(t, output, "allow")
	assert.Contains(t, output, "deny")
	assert.NotContains(t, output, "/run/acme.sock")
	assert.NotContains(t, output, "PRINCIPAL")
}

func TestListCommandJSON(t *testing.T) {
	grants := []db.SocketGrant{
		grant(1, "acme", "/harness/acme", "/run/acme.sock", db.GrantAllow, "Connect to Acme."),
	}
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return grants, nil },
	}
	fixture := newCommandFixture(store, nil)

	require.NoError(t, fixture.execute(t, "list", "--json"))

	var rows []map[string]any
	require.NoError(t, json.Unmarshal(fixture.out.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "acme", rows[0]["harness"])
	assert.Equal(t, "allow", rows[0]["status"])
	assert.NotContains(t, rows[0], "host_path")
}

func TestInfoCommand(t *testing.T) {
	row := grant(7, "acme", "/harness/acme", "/run/acme.sock", db.GrantDeny, "Connect to Acme.")
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return []db.SocketGrant{row}, nil },
	}
	fixture := newCommandFixture(store, nil)

	require.NoError(t, fixture.execute(t, "info", "7"))

	output := fixture.out.String()
	for _, value := range []string{
		"ID         :", "7", "Harness    :", "acme", "Principal  :", "/harness/acme",
		"Host socket:", "/run/acme.sock", "Status     :", "deny",
		"Listener   :", "agent-owner:agent-group (uid 1001, gid 1002)",
		"Purpose    :", "Connect to Acme.", "Granted at :", "2026-08-20T12:00:00Z",
	} {
		assert.Contains(t, output, value)
	}
}

func TestInfoCommandJSON(t *testing.T) {
	row := grant(7, "acme", "/harness/acme", "/run/acme.sock", db.GrantDeny, "Connect to Acme.")
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return []db.SocketGrant{row}, nil },
	}
	fixture := newCommandFixture(store, nil)

	require.NoError(t, fixture.execute(t, "info", "7", "--json"))

	var result map[string]any
	require.NoError(t, json.Unmarshal(fixture.out.Bytes(), &result))
	assert.Equal(t, "/harness/acme", result["harness_path"])
	assert.Equal(t, "/run/acme.sock", result["host_path"])
	assert.Equal(t, float64(1001), result["listener_uid"])
	assert.Equal(t, "agent-owner", result["listener_owner"])
}

func TestRevokeCommandByID(t *testing.T) {
	var revokedID int64
	store := &dbmocks.SocketGrantStoreMock{
		RevokeSocketFunc: func(id int64) error {
			revokedID = id
			return nil
		},
	}
	fixture := newCommandFixture(store, nil)

	require.NoError(t, fixture.execute(t, "revoke", "42"))

	assert.Equal(t, int64(42), revokedID)
	assert.Contains(t, fixture.out.String(), "Running containers keep their socket bridges until they stop or restart.")
}

func TestRevokeCommandByHarness(t *testing.T) {
	rows := []db.SocketGrant{
		grant(1, "acme", "/harness/acme-a", "/run/acme-a.sock", db.GrantAllow, "A."),
		grant(2, "acme", "/harness/acme-b", "/run/acme-b.sock", db.GrantDeny, "B."),
		grant(3, "other", "/harness/other", "/run/other.sock", db.GrantAllow, "Other."),
	}
	var revoked []string
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return rows, nil },
		RevokeHarnessSocketsFunc: func(principal string) error {
			revoked = append(revoked, principal)
			return nil
		},
	}
	fixture := newCommandFixture(store, nil)

	require.NoError(t, fixture.execute(t, "revoke", "--harness", "acme"))

	assert.ElementsMatch(t, []string{"/harness/acme-a", "/harness/acme-b"}, revoked)
}

func TestRevokeCommandAllConfirms(t *testing.T) {
	called := false
	store := &dbmocks.SocketGrantStoreMock{
		RevokeAllSocketsFunc: func() error {
			called = true
			return nil
		},
	}
	fixture := newCommandFixture(store, nil)
	fixture.ios.SetStdinTTY(true)
	fixture.ios.SetStdoutTTY(true)
	fixture.in.WriteString("yes\n")

	require.NoError(t, fixture.execute(t, "revoke", "--all"))

	assert.True(t, called)
	assert.Contains(t, fixture.errOut.String(), "Revoke all stored socket grants")
}

func TestPruneCommandResolvedAndUnresolvedHarnesses(t *testing.T) {
	root := t.TempDir()
	t.Setenv(consts.EnvConfigDir, t.TempDir())
	cfg := configmocks.NewBlankConfig()
	cfg.ProjectRootFunc = func() string { return root }
	harnessDir := writeSocketHarness(t, root, "active", "$ACTIVE_SOCKET")
	hostPath := filepath.Join(t.TempDir(), "active.sock")
	require.NoError(t, os.WriteFile(hostPath, nil, 0o600))
	t.Setenv("ACTIVE_SOCKET", hostPath)

	rows := []db.SocketGrant{
		grant(1, "active", harnessDir, hostPath, db.GrantAllow, "Active."),
		grant(2, "missing", filepath.Join(root, "missing"), "/run/missing.sock", db.GrantAllow, "Missing."),
	}
	var prunedPrincipal string
	var prunedPaths []string
	var revokedPrincipal string
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return rows, nil },
		PruneHarnessSocketsFunc: func(principal string, paths []string) error {
			prunedPrincipal = principal
			prunedPaths = append([]string(nil), paths...)
			return nil
		},
		RevokeHarnessSocketsFunc: func(principal string) error {
			revokedPrincipal = principal
			return nil
		},
	}
	fixture := newCommandFixture(store, cfg)

	require.NoError(t, fixture.execute(t, "prune", "--yes"))

	assert.Equal(t, harnessDir, prunedPrincipal)
	assert.Equal(t, []string{hostPath}, prunedPaths)
	assert.Equal(t, filepath.Join(root, "missing"), revokedPrincipal)
}

func TestPruneCommandDropsUnresolvedSocketDeclarations(t *testing.T) {
	root := t.TempDir()
	t.Setenv(consts.EnvConfigDir, t.TempDir())
	t.Setenv("MISSING_SOCKET", "")
	cfg := configmocks.NewBlankConfig()
	cfg.ProjectRootFunc = func() string { return root }
	harnessDir := writeSocketHarness(t, root, "active", "$MISSING_SOCKET")
	rows := []db.SocketGrant{
		grant(1, "active", harnessDir, "/run/old.sock", db.GrantAllow, "Old."),
	}
	var prunedPaths []string
	var revokedPrincipal string
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return rows, nil },
		PruneHarnessSocketsFunc: func(_ string, paths []string) error {
			prunedPaths = append([]string(nil), paths...)
			return nil
		},
		RevokeHarnessSocketsFunc: func(principal string) error {
			revokedPrincipal = principal
			return nil
		},
	}
	fixture := newCommandFixture(store, cfg)

	require.NoError(t, fixture.execute(t, "prune", "--yes"))

	assert.Empty(t, prunedPaths)
	assert.Equal(t, harnessDir, revokedPrincipal)
}

func TestPruneCommandAll(t *testing.T) {
	called := false
	store := &dbmocks.SocketGrantStoreMock{
		RevokeAllSocketsFunc: func() error {
			called = true
			return nil
		},
	}
	fixture := newCommandFixture(store, nil)

	require.NoError(t, fixture.execute(t, "prune", "-a", "--yes"))

	assert.True(t, called)
}

func TestGrantIDCompletions(t *testing.T) {
	rows := []db.SocketGrant{
		grant(1, "acme", "/harness/acme", "/run/acme.sock", db.GrantAllow, "Acme purpose."),
		grant(2, "other", "/harness/other", "/run/other.sock", db.GrantDeny, "Other purpose."),
	}
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return rows, nil },
	}
	fixture := newCommandFixture(store, nil)

	completions, directive := grantIDCompletions(fixture.socketGrants)(nil, []string{"2"}, "")

	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	assert.Equal(t, []cobra.Completion{
		"1\tacme → /run/acme.sock (Acme purpose.)",
	}, completions)
}

func TestGrantIDCompletionsDegradeOnStoreError(t *testing.T) {
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return nil, errors.New("database unavailable") },
	}
	fixture := newCommandFixture(store, nil)

	completions, directive := grantIDCompletions(fixture.socketGrants)(nil, nil, "")

	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	assert.Empty(t, completions)
}

func TestHarnessFlagCompletions(t *testing.T) {
	rows := []db.SocketGrant{
		grant(1, "acme", "/harness/acme-a", "/run/acme-a.sock", db.GrantAllow, "A."),
		grant(2, "acme", "/harness/acme-b", "/run/acme-b.sock", db.GrantAllow, "B."),
		grant(3, "other", "/harness/other", "/run/other.sock", db.GrantAllow, "Other."),
	}
	store := &dbmocks.SocketGrantStoreMock{
		ListSocketGrantsFunc: func() ([]db.SocketGrant, error) { return rows, nil },
	}
	fixture := newCommandFixture(store, nil)

	completions, directive := harnessCompletions(fixture.socketGrants)(nil, nil, "")

	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	assert.Equal(t, []cobra.Completion{"acme", "other"}, completions)
}

func TestSocketCommandsWireCompletions(t *testing.T) {
	fixture := newCommandFixture(&dbmocks.SocketGrantStoreMock{}, nil)
	root := NewCmdSockets(fixture.factory)
	var names []string
	for _, command := range root.Commands() {
		names = append(names, command.Name())
	}
	assert.ElementsMatch(t, []string{"info", "list", "prune", "revoke"}, names)
	info, _, err := root.Find([]string{"info"})
	require.NoError(t, err)
	assert.NotNil(t, info.ValidArgsFunction)
	revoke, _, err := root.Find([]string{"revoke"})
	require.NoError(t, err)
	assert.NotNil(t, revoke.ValidArgsFunction)
	_, ok := revoke.GetFlagCompletionFunc("harness")
	assert.True(t, ok)
}

func writeSocketHarness(t *testing.T, root, name, source string) string {
	t.Helper()
	dir := filepath.Join(root, consts.DotClawkerDir, "harnesses", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	manifest := fmt.Sprintf(`version: { resolver: none }
sockets:
  - source: %s
    target: /home/clawker/service.sock
    purpose: Connect to the service.
`, source)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "harness.yaml"), []byte(manifest), 0o600))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "Dockerfile.harness.tmpl"), []byte(`{{define "cmd"}}CMD ["x"]{{end}}`), 0o600),
	)
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return realDir
}

func TestRevokeRejectsAmbiguousSelectors(t *testing.T) {
	store := &dbmocks.SocketGrantStoreMock{}
	fixture := newCommandFixture(store, nil)

	err := fixture.execute(t, "revoke", "1", "--all")

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "one") || strings.Contains(err.Error(), "exclusive"))
}
