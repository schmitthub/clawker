package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
)

// agentsHarness drives the agents verb against a real sqlite registry
// rooted in a testenv-managed temp dir. Using the real registry keeps
// the test honest about the local-read contract — a regression that
// points the verb back at the AdminService gRPC path would compile but
// the assertions on stdout/stderr would fail because no AdminClient was
// wired.
//
// The DB path is resolved through `consts.ControlPlaneDBPath()`, which
// reads `CLAWKER_DATA_DIR` at call time. `testenv.New(t)` sets that env
// var to an isolated temp dir per test, so production code and tests
// agree on a single accessor — no opts.DBPath injection seam.
type agentsHarness struct {
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI
	DBPath    string
	opts      *AgentsOptions
}

func newAgentsHarness(t *testing.T) *agentsHarness {
	t.Helper()
	testenv.New(t)
	dbPath, err := consts.ControlPlaneDBPath()
	require.NoError(t, err)

	ios, _, _, _ := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h := &agentsHarness{
		IOStreams: ios,
		TUI:       tuiInst,
		DBPath:    dbPath,
	}
	h.opts = &AgentsOptions{
		IOStreams: ios,
		TUI:       tuiInst,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Format:    &cmdutil.FormatFlags{},
	}
	return h
}

// Stdout returns the captured stdout buffer. Re-resolving via the
// IOStreams.Out type assertion keeps the harness simple — the
// Test() helper returns a *bytes.Buffer for stdout that we can read
// back directly via the writer interface on IOStreams.
func (h *agentsHarness) writeAgent(t *testing.T, e agentregistry.Entry) {
	t.Helper()
	reg, err := agentregistry.NewSQLiteWriter(h.DBPath, logger.Nop())
	require.NoError(t, err)
	require.NoError(t, reg.Add(e))
	if closer, ok := reg.(interface{ Close() error }); ok {
		require.NoError(t, closer.Close())
	}
}

func mkThumbprint(seed byte) [sha256.Size]byte {
	var tp [sha256.Size]byte
	for i := range tp {
		tp[i] = seed
	}
	return tp
}

func TestAgentsRun_NoDBFile_RendersEmpty(t *testing.T) {
	// Brand-new host: no `clawker run` has ever fired, so the registry
	// DB doesn't exist. The verb must render the empty-state branch
	// rather than failing with a missing-file error — the data point
	// "zero agents" is the correct answer.
	h := newAgentsHarness(t)
	ios, _, stdout, stderr := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h.IOStreams = ios
	h.TUI = tuiInst
	h.opts.IOStreams = ios
	h.opts.TUI = tuiInst

	require.NoError(t, agentsRun(context.Background(), h.opts))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "No agents registered")
}

func TestAgentsRun_EmptyRegistry(t *testing.T) {
	h := newAgentsHarness(t)
	// Apply schema by opening writer, then close — leaves an empty DB.
	require.NoError(t, agentregistry.EnsureSchema(h.DBPath, logger.Nop()))

	ios, _, stdout, stderr := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h.opts.IOStreams = ios
	h.opts.TUI = tuiInst

	require.NoError(t, agentsRun(context.Background(), h.opts))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "No agents registered")
}

func TestAgentsRun_RendersTable(t *testing.T) {
	h := newAgentsHarness(t)
	now := time.Unix(1717000000, 0).UTC()
	h.writeAgent(t, agentregistry.Entry{
		AgentName:    "alpha",
		Project:      "myapp",
		ContainerID:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Thumbprint:   mkThumbprint(0xaa),
		RegisteredAt: now,
		LastSeen:     now,
	})

	ios, _, stdout, _ := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h.opts.IOStreams = ios
	h.opts.TUI = tuiInst

	require.NoError(t, agentsRun(context.Background(), h.opts))
	out := stdout.String()
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "myapp")
	assert.Contains(t, out, "0123456789ab") // 12-char short container id
	assert.Contains(t, out, "aaaaaaaaaaaa") // 12-char short thumbprint hex
}

func TestAgentsRun_JSONOutput(t *testing.T) {
	h := newAgentsHarness(t)
	now := time.Unix(1, 0).UTC()
	h.writeAgent(t, agentregistry.Entry{
		AgentName:    "x",
		Project:      "p",
		ContainerID:  "ctr",
		Thumbprint:   mkThumbprint(0x11),
		RegisteredAt: now,
		LastSeen:     time.Unix(2, 0).UTC(),
	})

	ios, _, stdout, _ := iostreams.Test()
	tuiInst := tui.NewTUI(ios)
	h.opts.IOStreams = ios
	h.opts.TUI = tuiInst

	jsonFmt, err := cmdutil.ParseFormat("json")
	require.NoError(t, err)
	h.opts.Format = &cmdutil.FormatFlags{Format: jsonFmt}

	require.NoError(t, agentsRun(context.Background(), h.opts))
	var rows []agentRow
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "x", rows[0].AgentName)
	assert.Equal(t, "p", rows[0].Project)
	assert.Equal(t, "ctr", rows[0].ContainerID)
}

func TestAgentsRun_PropagatesLoggerError(t *testing.T) {
	h := newAgentsHarness(t)
	h.opts.Logger = func() (*logger.Logger, error) { return nil, errors.New("logger boom") }
	err := agentsRun(context.Background(), h.opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logger boom")
}
