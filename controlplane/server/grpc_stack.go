package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/controlplane/agent"
	"github.com/schmitthub/clawker/controlplane/auth"
	fwhandler "github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// ErrNilFirewallHandler is returned by NewGRPCStack when no firewall
// handler is supplied. The handler backs the AdminService surface (its
// embedded UnimplementedAdminServiceServer satisfies method promotion),
// so a wiring path that reaches the constructor without one is a
// programming bug — surfaced as an error (not a panic) so the daemon
// degrades rather than crashing and stranding pinned eBPF.
var ErrNilFirewallHandler = errors.New("controlplane: NewGRPCStack requires a non-nil firewall handler")

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

	// PeerLookup resolves a live mTLS peer IP to the purpose=agent
	// container owning that endpoint, grounding the IdentityInterceptor's
	// trust check on a kernel-attested source instead of cert claims. A
	// nil-yielding IdentityInterceptor (wiring regression) degrades the
	// AgentService surface — see Serve / the identity gate below.
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

	log *logger.Logger
}

// NewGRPCStack constructs both gRPC listeners from deps without starting
// to serve. It loads the server cert, builds both mTLS configs, the
// shared Hydra introspector and the two per-listener auth interceptors,
// registers the AdminService on the admin server, and — when the
// IdentityInterceptor is available — builds the agent server and
// registers the AgentService.Register handler. Call Serve to start
// accepting connections.
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
	agentInterceptor :=
		auth.NewAuthInterceptor(introspector, agentv1.AgentMethodScopes(), log).
			RequireClientID(consts.ClientIDAgent)

	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(authInterceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(authInterceptor.StreamInterceptor()),
	)

	adminServer, err := NewAdminServer(deps.Handler, deps.Registry, log)
	if err != nil {
		return nil, fmt.Errorf("admin server: %w", err)
	}
	adminv1.RegisterAdminServiceServer(grpcServer, adminServer)

	grpcLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(deps.AdminPort))
	if err != nil {
		return nil, fmt.Errorf("grpc listen: %w", err)
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
		grpc.ChainUnaryInterceptor(agentInterceptor.UnaryInterceptor(), identityUnary),
		grpc.ChainStreamInterceptor(agentInterceptor.StreamInterceptor(), identityStream),
	)
	agentLis, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(deps.AgentPort))
	if err != nil {
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
		return nil, fmt.Errorf("agent register handler: %w", herr)
	}
	agentv1.RegisterAgentServiceServer(agentServer, registerHandler)

	stack.agentServer = agentServer
	stack.agentLis = agentLis
	return stack, nil
}

// Serve starts the recovered serve goroutines for both listeners. The
// admin listener always serves; the agent listener serves only when it
// was brought up (identity gate available). A non-nil Serve error from
// either server is deposited on failed without blocking — the channel is
// orchestrator-owned and buffered to cover every serve goroutine.
func (s *GRPCStack) Serve(failed chan<- error) {
	go func() {
		s.log.Info().Int("port", s.adminPort).Msg("gRPC admin API serving")
		if err := s.adminServer.Serve(s.adminLis); err != nil {
			failed <- fmt.Errorf("gRPC admin serve: %w", err)
		}
	}()

	if s.agentServer != nil {
		go func() {
			s.log.Info().Int("port", s.agentPort).Msg("gRPC agent API serving")
			if err := s.agentServer.Serve(s.agentLis); err != nil {
				failed <- fmt.Errorf("gRPC agent serve: %w", err)
			}
		}()
	}
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
