package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/controlplane/agent"
	"github.com/schmitthub/clawker/controlplane/auth"
	fwhandler "github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// Surface names for the two listeners, used as the component field on every
// structured log line the stack emits and as the prefix of the errors it
// deposits on the orchestrator's serve-failure channel.
const (
	surfaceAdmin = "grpc-admin"
	surfaceAgent = "grpc-agent"
)

// grpcPanicMessage is the client-visible detail of the codes.Internal status
// a recovered handler panic is converted into. The recovered value and the
// stack go to the structured log, never onto the wire.
const grpcPanicMessage = "internal error"

// cpBootingMessage is the client-visible detail of the
// codes.FailedPrecondition status the ready gate answers non-exempt RPCs
// with while the startup flow is still running.
const cpBootingMessage = "control plane is starting up"

// ErrNilFirewallHandler is returned by NewGRPCStack when no firewall
// handler is supplied. The handler backs the AdminService surface (its
// embedded UnimplementedAdminServiceServer satisfies method promotion),
// so a wiring path that reaches the constructor without one is a
// programming bug — surfaced as an error (not a panic) so the daemon
// degrades rather than crashing and stranding pinned eBPF.
var ErrNilFirewallHandler = errors.New("controlplane: NewGRPCStack requires a non-nil firewall handler")

// ErrNilReadyCheck is returned by NewGRPCStack when no ready predicate is
// supplied. The admin listener serves from construction and the ready
// gate is what keeps every non-exempt RPC out until the startup flow
// completes — wiring the stack without it would open the whole admin
// surface mid-boot.
var ErrNilReadyCheck = errors.New("controlplane: NewGRPCStack requires a non-nil ready predicate")

// GRPCDeps is the dependency set for the CP gRPC listener stack. Every
// field is orchestrator-owned and INJECTED — the stack constructs the
// gRPC servers and listeners from them but owns none of their
// lifecycles. In particular the firewall ActionQueue and Handler are
// orchestrator-owned (the queue's Close() is drain step 1); the stack
// only registers Handler onto the AdminService surface.
type GRPCDeps struct {
	// Handler backs the AdminService surface. Required — see
	// ErrNilFirewallHandler.
	Handler *fwhandler.Handler

	// Registry is the durable agent identity registry, shared with the
	// AdminService.ListAgents RPC and the AgentService.Register handler.
	Registry agent.Registry

	// Recovery is the recovery queue the WatchSOS RPC streams
	// from. Required — see server.ErrNilRecovery.
	Recovery RecoverySource

	// Ready reports whether the CP completed startup (SetReady). The
	// admin listener serves from construction so the bootstrap RPCs
	// (the public GetSystemTime, the admin-scoped WatchSOS) answer
	// while the CP boots; the ready gate rejects every other admin RPC
	// with codes.FailedPrecondition until this reports true. Required — see
	// ErrNilReadyCheck.
	Ready func() bool

	// PeerLookup resolves a live mTLS peer IP to the purpose=agent
	// container owning that endpoint, grounding the IdentityInterceptor's
	// trust check on a kernel-attested source instead of cert claims. A
	// nil-yielding IdentityInterceptor (wiring regression) degrades the
	// AgentService surface — see ServeAgent / the identity gate below.
	PeerLookup agent.ContainerByPeerIP

	// ServerCertPath / ServerKeyPath locate the CP server leaf used for
	// both the admin and agent listeners (same mTLS material).
	ServerCertPath string
	ServerKeyPath  string

	// CACertPool verifies client certificates on both listeners (the CLI
	// CA). Single pool, built once by the orchestrator and reused here.
	CACertPool *x509.CertPool

	// CATLS is the client TLS config the Hydra introspector dials with
	// (same CA pool). Single config, built once by the orchestrator.
	CATLS *tls.Config

	// HydraAdminPort is the port the Hydra introspect endpoint listens on
	// (container-internal). Used to build the introspect URL.
	HydraAdminPort int

	// AdminPort is the host-published gRPC AdminService port (CLI ↔ CP).
	AdminPort int

	// AgentPort is the clawker-network-only gRPC AgentService port
	// (clawkerd ↔ CP, NOT host-published).
	AgentPort int

	// Log is the CP structured logger.
	Log *logger.Logger
}

// GRPCStack owns the two CP gRPC listeners: the host-published admin
// listener (AdminService surface for the CLI) and the clawker-network
// agent listener (AgentService surface for clawkerd). Both share the
// same mTLS material; each enforces its own per-method scope vocabulary.
//
// The agent listener is conditional: if the IdentityInterceptor
// constructor fails (nil peer resolver — a wiring regression), the
// AgentService surface degrades — no agent listener is brought up and no
// Register handler is registered — while the admin listener, firewall,
// registry, and AdminService stay up so operators can still observe and
// contain. agentServer / agentLis are nil in that case.
type GRPCStack struct {
	adminServer *grpc.Server
	adminLis    net.Listener

	agentServer *grpc.Server // nil when the identity gate is unavailable
	agentLis    net.Listener // nil when the identity gate is unavailable

	adminPort int
	agentPort int

	// serveAdminOnce / serveAgentOnce make double-serve structurally
	// impossible: grpc.Server.Serve on an already-serving listener is a
	// race the orchestrator must never be able to trigger by wiring order.
	serveAdminOnce sync.Once
	serveAgentOnce sync.Once

	// adminServing reports whether the admin serve goroutine is live. Both
	// listener sockets are BOUND at construction, so a TCP dial to the admin
	// port completes from the accept backlog whether or not anything is in
	// Serve — this flag is the only signal that distinguishes the two, and
	// the orchestrator's /healthz probe consumes it via AdminServing.
	adminServing atomic.Bool

	log *logger.Logger
}

// handlerPanicStatus audits a recovered gRPC handler panic and returns the
// status it is converted into. grpc-go does NOT recover handler panics: an
// unrecovered panic unwinds out of the serve goroutine and kills PID 1,
// leaving the pinned eBPF programs filtering agent egress with no supervisor
// — a security incident, not an availability one. The recovered value and
// stack land on the structured surface only; the caller sees codes.Internal.
func handlerPanicStatus(log *logger.Logger, method string, recovered any) error {
	log.Error().
		Interface("panic", recovered).
		Bytes("stack", debug.Stack()).
		Str("component", "grpc-handler").
		Str("method", method).
		Str("event", "grpc_handler_panic").
		Msg("gRPC handler panicked; converted to codes.Internal so the serve goroutine survives and eBPF stays supervised")
	//nolint:wrapcheck // status.Error CREATES the terminal status here (same class as the errors.New/.Errorf constructors wrapcheck ignores); the interceptor contract returns status errors verbatim and wrapping adds no context.
	return status.Error(codes.Internal, grpcPanicMessage)
}

// recoveryUnaryInterceptor contains a panic raised anywhere downstream of it
// — handler, auth interceptor, or identity gate. It must be the OUTERMOST
// link of the unary chain so nothing it is meant to contain runs outside it.
func recoveryUnaryInterceptor(log *logger.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		var resp any
		var err error
		// The handler runs inside a closure so its panic can be converted to
		// the (nil, codes.Internal) result without named returns.
		func() {
			defer func() {
				if r := recover(); r != nil {
					resp = nil
					err = handlerPanicStatus(log, info.FullMethod, r)
				}
			}()
			resp, err = handler(ctx, req)
		}()
		return resp, err
	}
}

// recoveryStreamInterceptor is recoveryUnaryInterceptor's streaming twin and
// carries the same outermost-link requirement.
func recoveryStreamInterceptor(log *logger.Logger) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = handlerPanicStatus(log, info.FullMethod, r)
			}
		}()
		return handler(srv, ss)
	}
}

// readyGateExemptMethods is the set of admin RPCs that must answer while
// the CP is still booting: every public-scope method from the scope
// vocabulary (the bootstrap GetSystemTime — automatically exempt, so a
// new public RPC needs no second list) plus WatchSOS, which is
// admin-scoped (the auth link after the gate still enforces its bearer
// token) but IS the boot-assistance channel — gating it on ready would
// gate the CLI's only window into a boot waiting for assistance on the
// boot completing. Everything else waits for SetReady.
func readyGateExemptMethods() map[string]bool {
	exempt := map[string]bool{
		adminv1.AdminService_WatchSOS_FullMethodName: true,
	}
	for method, scope := range adminv1.AdminMethodScopes() {
		if scope == consts.ScopePublic {
			exempt[method] = true
		}
	}
	return exempt
}

// readyGateUnaryInterceptor rejects every non-exempt admin RPC with
// codes.FailedPrecondition until the CP reports ready. The admin listener
// serves from construction so the bootstrap RPCs answer while the CP
// boots (WatchSOS is the CLI's window into a boot waiting for
// assistance); this gate is what preserves the no-mutation-mid-boot
// contract that serving late used to provide. It sits between the
// recovery and auth links: a booting CP answers with a clean
// FailedPrecondition instead of whatever auth error a half-started Ory
// stack would produce.
func readyGateUnaryInterceptor(ready func() bool, exempt map[string]bool) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if !exempt[info.FullMethod] && !ready() {
			return nil, status.Error(codes.FailedPrecondition, cpBootingMessage)
		}
		return handler(ctx, req)
	}
}

// readyGateStreamInterceptor is readyGateUnaryInterceptor's streaming twin.
func readyGateStreamInterceptor(ready func() bool, exempt map[string]bool) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if !exempt[info.FullMethod] && !ready() {
			return status.Error(codes.FailedPrecondition, cpBootingMessage)
		}
		return handler(srv, ss)
	}
}

// NewGRPCStack constructs both gRPC listeners from deps without starting
// to serve. It loads the server cert, builds both mTLS configs, the
// shared Hydra introspector and the two per-listener auth interceptors,
// registers the AdminService on the admin server, and — when the
// IdentityInterceptor is available — builds the agent server and
// registers the AgentService.Register handler.
//
// Both listener sockets are BOUND here; neither serves until asked, and the
// order is a contract the orchestrator owns:
//
//   - ServeAgent runs at boot, before the CP is ready — clawkerd dial-back
//     and agent registration are boot-time flows.
//   - ServeAdmin also runs at boot, right after construction: the
//     bootstrap RPCs (the public GetSystemTime, the admin-scoped
//     WatchSOS) must answer while the startup flow is still running
//     — WatchSOS is the CLI's window into a boot waiting for
//     assistance. The ready gate in the admin
//     interceptor chain rejects every OTHER admin RPC — in particular any
//     rule mutation — with codes.FailedPrecondition until SetReady, which
//     preserves the no-mutation-mid-boot contract that serving late used
//     to provide.
//
// All failures return an error; the CP serve path never panics (a panic
// would strand pinned eBPF programs with no supervisor — a security
// incident). The IdentityInterceptor DEGRADE path is the one
// non-fatal-by-design outcome: a nil interceptor disables the agent
// listener and emits event=agent_identity_unavailable, but the
// constructor still returns a usable stack.
func NewGRPCStack(deps GRPCDeps) (*GRPCStack, error) {
	if deps.Handler == nil {
		return nil, ErrNilFirewallHandler
	}
	if deps.Ready == nil {
		return nil, ErrNilReadyCheck
	}
	log := deps.Log
	if log == nil {
		log = logger.Nop()
	}

	serverCert, err := tls.LoadX509KeyPair(deps.ServerCertPath, deps.ServerKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load server cert: %w", err)
	}

	// mTLS: require client certificates signed by the CLI CA.
	// CACertPool already contains the CA cert (parsed during the Ory
	// health waits). Authorization is still via OAuth2 bearer tokens —
	// mTLS authenticates the transport channel.
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    deps.CACertPool,
		MinVersion:   tls.VersionTLS13,
	}

	// Auth interceptors: one per listener so each enforces its own
	// method-scope vocabulary. Both share a single Hydra introspector —
	// tokens are checked against the same Hydra instance regardless of
	// which listener received them.
	hydraIntrospectURL := fmt.Sprintf("https://"+consts.Localhost+":%d/admin/oauth2/introspect", deps.HydraAdminPort)
	introspector := auth.NewHydraIntrospector(hydraIntrospectURL, deps.CATLS)
	authInterceptor := auth.NewAuthInterceptor(introspector, adminv1.AdminMethodScopes(), log)
	// Pin the agent interceptor to consts.ClientIDAgent — defense in
	// depth on top of the agent:self:register scope. The admin
	// interceptor stays unpinned — the CLI is the only client that holds
	// the admin scope and we don't want to lock out a future second admin
	// client.
	agentInterceptor := auth.NewAuthInterceptor(introspector, agentv1.AgentMethodScopes(), log).
		RequireClientID(consts.ClientIDAgent)

	// The recovery interceptor is FIRST in both chains — grpc.Chain*
	// interceptors run outermost-first, and a panic in the auth interceptor
	// itself must be contained just as a handler panic is. The ready gate
	// runs second, ahead of auth: the admin listener serves from
	// construction, so the gate is what keeps every non-exempt RPC out
	// until the startup flow completes (and a booting CP answers
	// FailedPrecondition instead of a half-started Ory stack's auth error).
	exempt := readyGateExemptMethods()
	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(
			recoveryUnaryInterceptor(log),
			readyGateUnaryInterceptor(deps.Ready, exempt),
			authInterceptor.UnaryInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			recoveryStreamInterceptor(log),
			readyGateStreamInterceptor(deps.Ready, exempt),
			authInterceptor.StreamInterceptor(),
		),
	)

	adminServer, err := NewAdminServer(deps.Handler, deps.Registry, deps.Recovery, log)
	if err != nil {
		return nil, fmt.Errorf("admin server: %w", err)
	}
	adminv1.RegisterAdminServiceServer(grpcServer, adminServer)

	grpcLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(deps.AdminPort))
	if err != nil {
		return nil, fmt.Errorf("grpc listen: %w", err)
	}

	// A listener bound here holds its port until closed. Every error arm
	// below must release what is already bound, or a CP restart after a
	// late constructor failure hits address-in-use on a port nothing serves.
	closeBound := func(listeners ...net.Listener) {
		for _, l := range listeners {
			if l == nil {
				continue
			}
			if cerr := l.Close(); cerr != nil {
				log.Warn().Err(cerr).
					Str("component", "grpc-stack").
					Msg("closing bound listener after constructor failure")
			}
		}
	}

	stack := &GRPCStack{
		adminServer: grpcServer,
		adminLis:    grpcLis,
		adminPort:   deps.AdminPort,
		agentPort:   deps.AgentPort,
		log:         log,
	}

	// Agent listener — bound to the clawker network only (NOT
	// host-published). Same mTLS material as the admin listener; the
	// per-listener AuthInterceptor enforces the agent-side method-scope
	// vocabulary so admin and agent surfaces fail closed on
	// cross-listener method names.
	agentTLSCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    deps.CACertPool,
		MinVersion:   tls.VersionTLS13,
	}
	// IdentityInterceptor runs AFTER AuthInterceptor: token + scope pass
	// first, then the universal identity gate grounds trust in the
	// kernel-attested peer IP (peer-IP → Docker → labels) and verifies
	// the cert's urn:clawker:agent: URI SAN against the label-derived
	// AgentFullName. Applies to every RPC including Register — no opt-out.
	// A constructor failure (nil resolver — wiring regression) degrades
	// the AgentService surface: no agent listener brought up, no Register
	// handler registered; CP, firewall, registry, AdminService stay up so
	// operators can still observe and contain.
	identityUnary, identityStream, identityErr := agent.IdentityInterceptor(
		deps.PeerLookup,
		log.With("component", "agent-identity"),
	)
	if identityErr != nil {
		log.Error().Err(identityErr).
			Str("component", "agent-identity").
			Str("event", "agent_identity_unavailable").
			Msg("agent identity gate unavailable; AgentService listener disabled, CP serve path otherwise unaffected")
		return stack, nil
	}
	if identityUnary == nil {
		return stack, nil
	}

	agentServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(agentTLSCfg)),
		grpc.ChainUnaryInterceptor(
			recoveryUnaryInterceptor(log),
			agentInterceptor.UnaryInterceptor(),
			identityUnary,
		),
		grpc.ChainStreamInterceptor(
			recoveryStreamInterceptor(log),
			agentInterceptor.StreamInterceptor(),
			identityStream,
		),
	)
	agentLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(deps.AgentPort))
	if err != nil {
		closeBound(grpcLis)
		return nil, fmt.Errorf("agent grpc listen: %w", err)
	}

	// Register the AgentService.Register handler. IdentityInterceptor
	// has already grounded the peer in a daemon-resolved container
	// identity and attached it to ctx; the handler captures the cert
	// thumbprint, cross-checks the cert's container_id SAN + request
	// fields against the resolved truth, and writes the registry row.
	registerHandler, herr := agent.NewHandler(
		deps.Registry,
		log.With("component", "agent-register"),
	)
	if herr != nil {
		closeBound(grpcLis, agentLis)
		return nil, fmt.Errorf("agent register handler: %w", herr)
	}
	agentv1.RegisterAgentServiceServer(agentServer, registerHandler)

	stack.agentServer = agentServer
	stack.agentLis = agentLis
	return stack, nil
}

// serveLoop is the body of both serve goroutines. A non-nil Serve error is
// deposited on failed so the orchestrator's serve select reaches its drain;
// the channel is orchestrator-owned and buffered to cover every serve
// goroutine, and each goroutine deposits at most once.
//
// The recover is the reason this loop exists as a shared helper: an
// unrecovered panic here unwinds through PID 1 and kills the CP with the
// eBPF programs still pinned and unsupervised, so a panic is converted into
// the same terminal serve failure a Serve error produces — the orchestrator
// then drains and flushes eBPF instead of dying.
func (s *GRPCStack) serveLoop(surface string, srv *grpc.Server, lis net.Listener, failed chan<- error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error().
				Interface("panic", r).
				Bytes("stack", debug.Stack()).
				Str("component", surface).
				Str("event", "grpc_serve_panic").
				Msg("gRPC serve goroutine panicked; converting to a terminal serve failure so drain-to-zero/eBPF flush still runs")
			failed <- fmt.Errorf("%s serve panic: %v", surface, r)
		}
	}()
	if err := srv.Serve(lis); err != nil {
		failed <- fmt.Errorf("%s serve: %w", surface, err)
	}
}

// ServeAgent starts the recovered serve goroutine for the agent listener
// (no-op when the identity gate was unavailable and the listener was never
// brought up). It runs during startup, before the CP is ready: clawkerd
// dial-back and agent registration are boot-time flows and must not wait
// on the admin surface. Serving is single-shot — a second call serves
// nothing and warns.
func (s *GRPCStack) ServeAgent(failed chan<- error) {
	if s.agentServer == nil {
		return
	}
	started := false
	s.serveAgentOnce.Do(func() {
		started = true
		go func() {
			s.log.Info().Int("port", s.agentPort).Msg("gRPC agent API serving")
			s.serveLoop(surfaceAgent, s.agentServer, s.agentLis, failed)
		}()
	})
	if !started {
		s.log.Warn().
			Str("component", surfaceAgent).
			Str("event", "grpc_serve_already_started").
			Msg("agent listener is already serving; ignoring repeat ServeAgent")
	}
}

// ServeAdmin starts the recovered serve goroutine for the admin listener.
// The orchestrator calls it MID-BOOT, right after construction: the
// bootstrap RPCs (the public GetSystemTime, the admin-scoped
// WatchSOS) must answer while the
// startup flow runs, and the ready-gate interceptor rejects every other
// admin RPC — in particular any rule mutation — until SetReady. The
// listener socket is bound at construction, so a client that connects
// before this call simply waits in the accept backlog rather than getting
// connection-refused. Serving is single-shot — a second call serves
// nothing and warns.
//
// The serving flag flips BEFORE the goroutine is scheduled: /healthz starts
// after this call, and a probe landing between the go statement and the
// goroutine's first instruction must not read a false not-serving.
func (s *GRPCStack) ServeAdmin(failed chan<- error) {
	started := false
	s.serveAdminOnce.Do(func() {
		started = true
		s.adminServing.Store(true)
		go func() {
			defer s.adminServing.Store(false)
			s.log.Info().Int("port", s.adminPort).Msg("gRPC admin API serving")
			s.serveLoop(surfaceAdmin, s.adminServer, s.adminLis, failed)
		}()
	})
	if !started {
		s.log.Warn().
			Str("component", surfaceAdmin).
			Str("event", "grpc_serve_already_started").
			Msg("admin listener is already serving; ignoring repeat ServeAdmin")
	}
}

// AdminServing reports whether the admin serve goroutine is live: true from
// the moment ServeAdmin dispatches it until Serve returns. The listener
// socket is bound at construction, so a TCP dial to the admin port succeeds
// even when nothing is serving — a health probe that trusts the dial alone
// reports healthy while every admin RPC hangs in the accept backlog. This is
// the signal that closes that gap.
func (s *GRPCStack) AdminServing() bool {
	return s.adminServing.Load()
}

// GracefulStop drains in-flight RPCs on both listeners, then returns. If
// ctx expires first it forces a hard Stop on both servers and returns —
// the orchestrator passes a bounded context so a wedged RPC can't hang
// drain forever. The agent server is stopped only when it was brought
// up.
func (s *GRPCStack) GracefulStop(ctx context.Context) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.adminServer.GracefulStop()
	}()
	if s.agentServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.agentServer.GracefulStop()
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		s.log.Warn().Msg("gRPC graceful stop timed out, forcing")
		s.Stop()
	}
}

// Stop force-closes both servers, cancelling in-flight RPCs. The agent
// server is stopped only when it was brought up. Idempotent — safe to
// call after GracefulStop.
func (s *GRPCStack) Stop() {
	s.adminServer.Stop()
	if s.agentServer != nil {
		s.agentServer.Stop()
	}
}
