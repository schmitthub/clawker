package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/agent"
	"github.com/schmitthub/clawker/internal/auth"
)

// TestAdminServer_NewAdminServer_NilAgentsErrors pins that the
// constructor rejects a nil registry — CP is the sole sqlite writer, any
// wiring path reaching the constructor without a registry is a
// programming bug. It surfaces as ErrNilRegistry (not a panic) so the
// daemon degrades rather than crashing and stranding pinned eBPF.
func TestAdminServer_NewAdminServer_NilAgentsErrors(t *testing.T) {
	srv, err := NewAdminServer(nil, nil, nil)
	require.ErrorIs(t, err, ErrNilRegistry)
	assert.Nil(t, srv)
}

// TestNewGRPCStack_NilHandlerErrors pins that the gRPC stack constructor
// rejects a nil firewall handler — the handler backs the AdminService
// surface, so any wiring path reaching the constructor without one is a
// programming bug. It surfaces as ErrNilFirewallHandler (not a panic) so
// the daemon degrades rather than crashing and stranding pinned eBPF.
// The guard fires before any cert load or port bind, so the test needs
// no filesystem or network setup.
func TestNewGRPCStack_NilHandlerErrors(t *testing.T) {
	stack, err := NewGRPCStack(GRPCDeps{Handler: nil})
	require.ErrorIs(t, err, ErrNilFirewallHandler)
	assert.Nil(t, stack)
}

func TestAdminServer_ListAgents_Snapshot(t *testing.T) {
	reg := agent.NewRegistry(nil)
	now := time.Unix(1000, 0)

	thumbA := sha256.Sum256([]byte("cert-a"))
	thumbB := sha256.Sum256([]byte("cert-b"))

	require.NoError(t, reg.Add(agent.Entry{
		AgentName:    auth.MustAgentName("b"),
		Project:      auth.MustProjectSlug("p"),
		ContainerID:  "ctr-b",
		Thumbprint:   thumbB,
		RegisteredAt: now,
		LastSeen:     now,
	}))
	require.NoError(t, reg.Add(agent.Entry{
		AgentName:    auth.MustAgentName("a"),
		Project:      auth.MustProjectSlug("p"),
		ContainerID:  "ctr-a",
		Thumbprint:   thumbA,
		RegisteredAt: now.Add(time.Second),
		LastSeen:     now.Add(time.Second),
	}))

	srv := &adminServer{agents: reg}
	resp, err := srv.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Agents, 2)

	// Sorted by (Project, AgentName).
	assert.Equal(t, "a", resp.Agents[0].AgentName)
	assert.Equal(t, "b", resp.Agents[1].AgentName)
	assert.Equal(t, "p", resp.Agents[0].Project)
	assert.Equal(t, "p", resp.Agents[1].Project)

	// Thumbprint hex matches the stored thumbprint exactly.
	assert.Equal(t, hex.EncodeToString(thumbA[:]), resp.Agents[0].CertThumbprint)
	assert.Equal(t, hex.EncodeToString(thumbB[:]), resp.Agents[1].CertThumbprint)

	// Container IDs round-trip.
	assert.Equal(t, "ctr-a", resp.Agents[0].ContainerId)
	assert.Equal(t, "ctr-b", resp.Agents[1].ContainerId)

	// Timestamps round-trip as Unix seconds.
	assert.Equal(t, now.Add(time.Second).Unix(), resp.Agents[0].RegisteredAtUnix)
	assert.Equal(t, now.Unix(), resp.Agents[1].RegisteredAtUnix)
}

// fakeSnapshotRegistry is an in-test agent.Registry that lets the
// ListAgents test pin behavior on Snapshot's error return. The other
// methods are unused for this test and return zero values.
type fakeSnapshotRegistry struct {
	snap    []agent.Entry
	snapErr error
}

func (f *fakeSnapshotRegistry) Add(agent.Entry) error { return nil }
func (f *fakeSnapshotRegistry) LookupByContainerID(string) (*agent.Entry, error) {
	return nil, agent.ErrUnknownAgent
}
func (f *fakeSnapshotRegistry) EvictByContainerID(string) error  { return nil }
func (f *fakeSnapshotRegistry) Snapshot() ([]agent.Entry, error) { return f.snap, f.snapErr }

// TestAdminServer_ListAgents_SnapshotError_ReturnsCodesInternal pins
// the CLI-visible contract for the punch-list eviction-cascade fix:
// when Snapshot returns a non-nil error (sqlite query failure), the
// AdminService surfaces codes.Internal rather than silently returning
// an empty list. A regression that mapped the error back to a nil-err
// empty result would silently mislead operators reading
// `clawker controlplane agents` — "no agents" while the registry is
// intact but unreadable.
func TestAdminServer_ListAgents_SnapshotError_ReturnsCodesInternal(t *testing.T) {
	reg := &fakeSnapshotRegistry{snapErr: errors.New("sqlite query failed")}
	srvIface, err := NewAdminServer(nil, reg, nil)
	require.NoError(t, err)
	srv := srvIface.(*adminServer)

	resp, err := srv.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok, "must be a gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
}
