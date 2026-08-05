package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/agent"
	fwhandler "github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// TestAdminServer_NewAdminServer_NilAgentsErrors pins that the
// constructor rejects a nil registry — CP is the sole sqlite writer, any
// wiring path reaching the constructor without a registry is a
// programming bug. It surfaces as ErrNilRegistry (not a panic) so the
// daemon degrades rather than crashing and stranding pinned eBPF.
func TestAdminServer_NewAdminServer_NilAgentsErrors(t *testing.T) {
	srv, err := NewAdminServer(nil, nil, new(fakeRecoverySource), nil)
	require.ErrorIs(t, err, ErrNilRegistry)
	assert.Nil(t, srv)
}

// TestAdminServer_NewAdminServer_NilRecoveryErrors pins that the
// constructor rejects a nil recovery source — the WatchSOS RPC is
// the CLI's only window into a CP waiting for boot assistance, so wiring
// the server without it is a programming bug. It surfaces as
// ErrNilRecovery (not a panic) per the CP no-crash contract.
func TestAdminServer_NewAdminServer_NilRecoveryErrors(t *testing.T) {
	srv, err := NewAdminServer(nil, agent.NewRegistry(nil), nil, nil)
	require.ErrorIs(t, err, ErrNilRecovery)
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
	srvIface, err := NewAdminServer(nil, reg, new(fakeRecoverySource), nil)
	require.NoError(t, err)
	srv := srvIface.(*adminServer)

	resp, err := srv.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok, "must be a gRPC status error")
	assert.Equal(t, codes.Internal, st.Code())
}

// fakeRecoverySource is an in-test RecoverySource handing out a fixed
// channel and counting cancels.
type fakeRecoverySource struct {
	ch      chan *adminv1.SOS
	cancels int
}

func (f *fakeRecoverySource) SubscribeRecovery() (<-chan *adminv1.SOS, func()) {
	return f.ch, func() { f.cancels++ }
}

// fakeRecoveryStream satisfies grpc.ServerStreamingServer[SOS]
// for driving WatchSOS without a network. Only Context and Send are
// exercised; the embedded nil ServerStream panics on anything else, which
// is exactly the regression signal we want.
type fakeRecoveryStream struct {
	grpc.ServerStream

	//nolint:containedctx // mirrors grpc.ServerStream, whose Context() is the stream's own — there is no ctx parameter to thread
	ctx  context.Context
	sent []*adminv1.SOS
}

func (f *fakeRecoveryStream) Context() context.Context { return f.ctx }
func (f *fakeRecoveryStream) Send(m *adminv1.SOS) error {
	f.sent = append(f.sent, m)
	return nil
}

// TestAdminServer_WatchSOS pins the stream contract: a published
// failure is sent to the watcher; a closed subscription channel ends the
// stream cleanly (nil — resolved, or nothing ever to report); a watcher
// whose context dies ends cleanly too. Every path releases the
// subscription (cancel), or the orchestrator's idle clock would treat a
// gone watcher as still connected and a CP abandoned mid-recovery would
// never shut down.
func TestAdminServer_WatchSOS(t *testing.T) {
	newServer := func(src *fakeRecoverySource) *adminServer {
		return &adminServer{
			Handler:  nil,
			agents:   nil,
			recovery: src,
			log:      logger.Nop(),
		}
	}

	t.Run("delivers failure then clean EOF on close", func(t *testing.T) {
		src := &fakeRecoverySource{ch: make(chan *adminv1.SOS, 1), cancels: 0}
		src.ch <- &adminv1.SOS{
			Kind:    adminv1.SOSKind_SOS_KIND_BPFFS_DELEGATION,
			Message: "bpffs delegation needed",
		}
		close(src.ch)

		stream := &fakeRecoveryStream{ServerStream: nil, ctx: context.Background(), sent: nil}
		err := newServer(src).WatchSOS(&adminv1.WatchSOSRequest{}, stream)
		require.NoError(t, err)
		require.Len(t, stream.sent, 1)
		assert.Equal(t, "bpffs delegation needed", stream.sent[0].GetMessage())
		assert.Equal(t, adminv1.SOSKind_SOS_KIND_BPFFS_DELEGATION, stream.sent[0].GetKind(),
			"kind must ride the wire — it is what the CLI dispatches on")
		assert.Equal(t, 1, src.cancels, "subscription must be released")
	})

	t.Run("closed channel with no message is clean EOF", func(t *testing.T) {
		src := &fakeRecoverySource{ch: make(chan *adminv1.SOS), cancels: 0}
		close(src.ch)

		stream := &fakeRecoveryStream{ServerStream: nil, ctx: context.Background(), sent: nil}
		err := newServer(src).WatchSOS(&adminv1.WatchSOSRequest{}, stream)
		require.NoError(t, err)
		assert.Empty(t, stream.sent)
		assert.Equal(t, 1, src.cancels)
	})

	t.Run("watcher context done ends stream and releases subscription", func(t *testing.T) {
		src := &fakeRecoverySource{ch: make(chan *adminv1.SOS), cancels: 0}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		stream := &fakeRecoveryStream{ServerStream: nil, ctx: ctx, sent: nil}
		err := newServer(src).WatchSOS(&adminv1.WatchSOSRequest{}, stream)
		require.NoError(t, err)
		assert.Empty(t, stream.sent)
		assert.Equal(t, 1, src.cancels)
	})
}

// TestNewGRPCStack_NilReadyErrors pins that the gRPC stack constructor
// rejects a nil ready predicate — the admin listener serves from
// construction, and the ready gate is the only thing keeping non-public
// RPCs out mid-boot. Wiring the stack without it would open the whole
// admin surface (rule mutations included) while the CP is still booting.
func TestNewGRPCStack_NilReadyErrors(t *testing.T) {
	stack, err := NewGRPCStack(GRPCDeps{
		Handler:        new(fwhandler.Handler),
		Registry:       nil,
		Recovery:       nil,
		Ready:          nil,
		PeerLookup:     nil,
		ServerCertPath: "",
		ServerKeyPath:  "",
		CACertPool:     nil,
		CATLS:          nil,
		HydraAdminPort: 0,
		AdminPort:      0,
		AgentPort:      0,
		Log:            nil,
	})
	require.ErrorIs(t, err, ErrNilReadyCheck)
	assert.Nil(t, stack)
}

// adminMethod builds a full admin method string the way the generated
// bindings do.
func adminMethod(name string) string {
	return "/" + adminv1.ServiceName + "/" + name
}

// TestReadyGate_Unary pins the ready gate's contract on the unary chain:
// while the CP boots, every non-exempt RPC is rejected with
// codes.FailedPrecondition BEFORE its handler runs (this is what
// preserves the no-mutation-mid-boot contract now that the admin
// listener serves from construction), while the bootstrap RPCs (the
// public GetSystemTime, the admin-scoped ready-gate-exempt
// WatchSOS) pass — WatchSOS is the CLI's only window into a
// boot waiting for assistance. Once ready, everything passes.
func TestReadyGate_Unary(t *testing.T) {
	ready := false
	gate := readyGateUnaryInterceptor(func() bool { return ready }, readyGateExemptMethods())

	invoke := func(method string) (bool, error) {
		called := false
		handler := func(_ context.Context, _ any) (any, error) {
			called = true
			return nil, nil //nolint:nilnil // test handler; only the called flag matters
		}
		info := &grpc.UnaryServerInfo{Server: nil, FullMethod: method}
		_, err := gate(context.Background(), nil, info, handler)
		return called, err
	}

	// Pre-ready: a mutating RPC is blocked before its handler runs.
	called, err := invoke(adminMethod("FirewallInit"))
	assert.False(t, called, "gated handler must not run mid-boot")
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	// Pre-ready: the exempt bootstrap RPCs pass.
	for _, m := range []string{"GetSystemTime", "WatchSOS"} {
		called, err = invoke(adminMethod(m))
		assert.True(t, called, "%s must answer mid-boot", m)
		require.NoError(t, err)
	}

	// Post-ready: everything passes.
	ready = true
	called, err = invoke(adminMethod("FirewallInit"))
	assert.True(t, called)
	require.NoError(t, err)
}

// TestReadyGate_Stream is TestReadyGate_Unary's streaming twin.
func TestReadyGate_Stream(t *testing.T) {
	ready := false
	gate := readyGateStreamInterceptor(func() bool { return ready }, readyGateExemptMethods())

	invoke := func(method string) (bool, error) {
		called := false
		handler := func(_ any, _ grpc.ServerStream) error {
			called = true
			return nil
		}
		info := &grpc.StreamServerInfo{
			FullMethod:     method,
			IsClientStream: false,
			IsServerStream: true,
		}
		err := gate(nil, nil, info, handler)
		return called, err
	}

	called, err := invoke(adminMethod("FirewallInit"))
	assert.False(t, called, "gated handler must not run mid-boot")
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	called, err = invoke(adminMethod("WatchSOS"))
	assert.True(t, called)
	require.NoError(t, err)

	ready = true
	called, err = invoke(adminMethod("FirewallInit"))
	assert.True(t, called)
	require.NoError(t, err)
}

// newAdminOnlyStack hand-builds a GRPCStack with only the admin half wired,
// over an ephemeral loopback listener. It mirrors the shape NewGRPCStack
// produces for the serve-lifecycle assertions below without needing cert
// material, a firewall handler, or fixed ports.
func newAdminOnlyStack(t *testing.T) *GRPCStack {
	t.Helper()
	lis, err := net.Listen("tcp", consts.Localhost+":0")
	require.NoError(t, err)
	srv := grpc.NewServer() // nosemgrep: go.grpc.security.grpc-server-insecure-connection.grpc-server-insecure-connection -- loopback test server for serve-lifecycle assertions; deliberately credential-free
	t.Cleanup(srv.Stop)
	tcpAddr, ok := lis.Addr().(*net.TCPAddr)
	require.True(t, ok, "loopback listener address is TCP")
	return &GRPCStack{
		adminServer:    srv,
		adminLis:       lis,
		agentServer:    nil,
		agentLis:       nil,
		adminPort:      tcpAddr.Port,
		agentPort:      0,
		serveAdminOnce: sync.Once{},
		serveAgentOnce: sync.Once{},
		adminServing:   atomic.Bool{},
		log:            logger.Nop(),
	}
}

// TestGRPCStack_ServeAgent_NoAgentListener pins the degrade path: when the
// IdentityInterceptor was unavailable the agent server and listener are nil,
// and ServeAgent must be an inert no-op. A regression that dereferenced the
// nil server would panic on a goroutine, killing PID 1 and stranding pinned
// eBPF; one that deposited a spurious error on the serve channel would trip
// the orchestrator's serve select and tear a healthy CP down.
func TestGRPCStack_ServeAgent_NoAgentListener(t *testing.T) {
	stack := &GRPCStack{
		adminServer:    nil,
		adminLis:       nil,
		agentServer:    nil,
		agentLis:       nil,
		adminPort:      0,
		agentPort:      0,
		serveAdminOnce: sync.Once{},
		serveAgentOnce: sync.Once{},
		adminServing:   atomic.Bool{},
		log:            logger.Nop(),
	}
	failed := make(chan error, 1)

	stack.ServeAgent(failed)

	select {
	case err := <-failed:
		t.Fatalf("ServeAgent deposited a failure with no agent listener: %v", err)
	default:
	}
}

// TestGRPCStack_AdminListenerBoundBeforeServe pins the accept-backlog
// semantics the /healthz grpc-admin probe has to work around: the admin
// socket is bound at construction, so a TCP dial succeeds while nothing is
// serving. AdminServing is the signal that distinguishes the two — if a
// change ever made the bare dial sufficient, this test's dial would fail and
// say so.
func TestGRPCStack_AdminListenerBoundBeforeServe(t *testing.T) {
	stack := newAdminOnlyStack(t)

	assert.False(t, stack.AdminServing(), "nothing has called ServeAdmin yet")

	conn, err := net.DialTimeout("tcp", stack.adminLis.Addr().String(), 2*time.Second)
	require.NoError(t, err, "bound listener must accept a connection before Serve")
	require.NoError(t, conn.Close())
}

// rpcReachesServer reports whether a gRPC request reaches the server on addr
// — the discriminator a bare TCP dial cannot provide, since the bound
// listener accepts either way. An unregistered method answering
// codes.Unimplemented proves the accept loop and HTTP/2 stack are live.
func rpcReachesServer(t *testing.T, addr string) bool {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			t.Logf("close probe client: %v", cerr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = conn.Invoke(
		ctx,
		"/clawker.probe.v1.Probe/Ping",
		&adminv1.GetSystemTimeRequest{},
		&adminv1.GetSystemTimeResult{UnixNanos: 0},
	)
	return status.Code(err) == codes.Unimplemented
}

// TestGRPCStack_ServeAdmin_SingleShot pins the [sync.Once] guard: the first
// call starts serving, later calls start nothing. grpc.Server.Serve on an
// already-serving listener is a race the orchestrator must not be able to
// trigger through wiring order, and a second goroutine would also report a
// second terminal failure on the serve channel — tearing the CP down twice
// over one event.
func TestGRPCStack_ServeAdmin_SingleShot(t *testing.T) {
	stack := newAdminOnlyStack(t)
	failed := make(chan error, 2)

	stack.ServeAdmin(failed)
	require.True(t, stack.AdminServing(), "serving flag is set before the goroutine is scheduled")

	// Repeat while the first goroutine is live — the regression shape.
	stack.ServeAdmin(failed)
	require.True(t, stack.AdminServing())

	require.Eventually(t, func() bool { return rpcReachesServer(t, stack.adminLis.Addr().String()) },
		5*time.Second, 20*time.Millisecond, "admin surface must answer RPCs once serving")

	// Serve was entered, so Stop unwinds it with a nil error: a single serve
	// goroutine reports nothing and clears the flag. Any extra goroutine
	// would have been started before Serve was live and would deposit
	// ErrServerStopped here.
	stack.adminServer.Stop()
	require.Eventually(t, func() bool { return !stack.AdminServing() },
		5*time.Second, 10*time.Millisecond, "serving flag must clear when Serve returns")

	stack.ServeAdmin(failed)
	assert.False(t, stack.AdminServing(), "a repeat ServeAdmin must not start a second serve goroutine")

	select {
	case err := <-failed:
		t.Fatalf("unexpected serve failure: %v", err)
	default:
	}
}
