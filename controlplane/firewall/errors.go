package firewall

import (
	"errors"
	"fmt"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/runtime/protoiface"
)

// Sentinel errors for firewall stack health check failures.
var (
	ErrEnvoyUnhealthy   = errors.New("envoy not healthy")
	ErrCoreDNSUnhealthy = errors.New("coredns not healthy")
	ErrCPUnhealthy      = errors.New("clawker-controlplane not healthy")
)

// Constructor-validation sentinels. NewHandler returns these instead of
// panicking so a wiring fault degrades the subsystem with a structured
// event=<subsystem>_unavailable line rather than killing PID 1 (which
// would strand pinned eBPF programs with no supervisor).
var (
	ErrNilEBPFManager = errors.New("firewall: NewHandler requires a non-nil EBPFManager")
	ErrNilResolver    = errors.New("firewall: NewHandler requires a non-nil ContainerResolver")
	ErrNilQueue       = errors.New("firewall: NewHandler requires a non-nil ActionQueue")
)

// Sentinels surfaced through the Handler and queue. Each has a companion
// Reason constant for gRPC errdetails.ErrorInfo so the CLI can dispatch
// remediation without string-matching status messages.
//
// Layering: CLI-dial sentinels fire before the queue is
// touched; pre-Submit sentinels fire inside the RPC handler before the
// store write lands; queued-closure sentinels fire on the worker after
// the store write has already committed — partial success by design.
var (
	// CLI-dial layer. ErrCPNotRunning mostly surfaces client-side from
	// the AdminClient dial helper; the server rarely emits it.
	ErrCPNotRunning = errors.New("control plane not running")
	ErrQueueClosed  = errors.New("action queue closed or CP shutting down")

	// Pre-Submit layer (store unchanged on failure).
	ErrFirewallNotInitialized = errors.New("firewall not initialized")
	ErrContainerGone          = errors.New("container no longer exists")
	ErrRuleInvalid            = errors.New("invalid rule")
	ErrRuleStoreWrite         = errors.New("rule store write failed")
	ErrCertRegen              = errors.New("ca / per-domain cert regeneration failed")

	// Queued-closure layer (store already committed — partial success).
	ErrStackProbe     = errors.New("cannot probe firewall stack state")
	ErrConfigRegen    = errors.New("stack config regeneration failed")
	ErrEnvoyRestart   = errors.New("envoy restart failed")
	ErrCoreDNSRestart = errors.New("coredns restart failed")
	ErrStackUnhealthy = errors.New("stack containers are not healthy")
	ErrRouteSync      = errors.New("bpf route map sync failed")
)

// Reason* constants are the stable wire strings carried in
// errdetails.ErrorInfo.Reason across the gRPC boundary. CLI code matches
// on these instead of Go-side sentinel identity — status.Error wrapping
// drops errors.Is fidelity across the wire.
const (
	ReasonCPNotRunning           = "CP_NOT_RUNNING"
	ReasonQueueClosed            = "QUEUE_CLOSED"
	ReasonFirewallNotInitialized = "FIREWALL_NOT_INITIALIZED"
	ReasonContainerGone          = "CONTAINER_GONE"
	ReasonRuleInvalid            = "RULE_INVALID"
	ReasonRuleStoreWrite         = "RULE_STORE_WRITE"
	ReasonCertRegen              = "CERT_REGEN"
	ReasonStackProbe             = "STACK_PROBE"
	ReasonConfigRegen            = "CONFIG_REGEN"
	ReasonEnvoyRestart           = "ENVOY_RESTART"
	ReasonCoreDNSRestart         = "COREDNS_RESTART"
	ReasonStackUnhealthy         = "STACK_UNHEALTHY"
	ReasonRouteSync              = "ROUTE_SYNC"
)

// ErrorInfoDomain is the errdetails.ErrorInfo.Domain for every firewall
// error detail. Keeps Reason lookups scoped — future domain error
// catalogs pick their own domain string.
const ErrorInfoDomain = "firewall.clawker.dev"

// sentinelDispatch maps each sentinel to a gRPC code plus a Reason
// string. Ordered by layer (dial → pre-Submit → queued-closure) so that
// a joined error's FIRST match picks the primary status code — callers
// see the earliest-layer failure as the primary classification.
var sentinelDispatch = []struct {
	err    error
	code   codes.Code
	reason string
}{
	{ErrCPNotRunning, codes.Unavailable, ReasonCPNotRunning},
	{ErrQueueClosed, codes.Unavailable, ReasonQueueClosed},

	{ErrFirewallNotInitialized, codes.FailedPrecondition, ReasonFirewallNotInitialized},
	{ErrContainerGone, codes.FailedPrecondition, ReasonContainerGone},
	{ErrRuleInvalid, codes.InvalidArgument, ReasonRuleInvalid},
	{ErrRuleStoreWrite, codes.Internal, ReasonRuleStoreWrite},
	{ErrCertRegen, codes.Internal, ReasonCertRegen},

	{ErrStackProbe, codes.Unavailable, ReasonStackProbe},
	{ErrConfigRegen, codes.Internal, ReasonConfigRegen},
	{ErrEnvoyRestart, codes.Internal, ReasonEnvoyRestart},
	{ErrCoreDNSRestart, codes.Internal, ReasonCoreDNSRestart},
	{ErrStackUnhealthy, codes.Unavailable, ReasonStackUnhealthy},
	{ErrRouteSync, codes.Internal, ReasonRouteSync},
}

// toStatus converts a firewall sentinel (possibly a chain built via
// errors.Join) into a gRPC status with one errdetails.ErrorInfo per
// matched sentinel. The CLI iterates details and renders a remediation
// line per Reason — matching the wire contract without reading the
// status message string. Unknown errors become codes.Internal with no
// details. nil in → nil out.
func toStatus(err error) error {
	if err == nil {
		return nil
	}

	var (
		primaryCode = codes.Internal
		details     []protoiface.MessageV1
	)
	for _, d := range sentinelDispatch {
		if errors.Is(err, d.err) {
			if len(details) == 0 {
				primaryCode = d.code
			}
			details = append(details, &errdetails.ErrorInfo{
				Reason: d.reason,
				Domain: ErrorInfoDomain,
			})
		}
	}

	st := status.New(primaryCode, err.Error())
	if len(details) == 0 {
		return st.Err()
	}
	withDetails, attachErr := st.WithDetails(details...)
	if attachErr != nil {
		return st.Err()
	}
	return withDetails.Err()
}

// HealthTimeoutError is returned when a firewall stack health wait
// exceeds its deadline. Err wraps one or more of the stack health
// sentinel errors.
type HealthTimeoutError struct {
	Timeout time.Duration
	Err     error
}

func (e *HealthTimeoutError) Error() string {
	return fmt.Sprintf("%v after %s — this can happen during first run when firewall container images are being pulled, or may indicate a bug. Run 'docker ps -a --filter label=dev.clawker.purpose=firewall' to check container status, or try the command again", e.Err, e.Timeout)
}

func (e *HealthTimeoutError) Unwrap() error { return e.Err }
