package agent_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/controlplane/agent"
	registrymock "github.com/schmitthub/clawker/controlplane/agent/mocks"
	"github.com/schmitthub/clawker/controlplane/otelgateway"
	"github.com/schmitthub/clawker/internal/consts"
)

// exportFixture is a registered container: a leaf, the registry row that
// binds its thumbprint, and the handler wired with a recording gateway.
type exportFixture struct {
	leaf     *x509.Certificate
	resolved agent.ResolvedContainer
	registry *registrymock.RegistryMock
	gateway  *registrymock.OtelForwarderMock
	handler  *agent.Handler
}

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()
	caCert, caKey := genTestCA(t)
	leaf := signTestLeaf(t, caCert, caKey, consts.ContainerClawkerd, sanFullName, "ctr-id")
	resolved := resolvedFor(t, project, "ctr-id")
	thumb := sha256.Sum256(leaf.Raw)
	registry := new(registrymock.RegistryMock)
	registry.LookupByContainerIDFunc = func(containerID string) (*agent.Entry, error) {
		require.Equal(t, "ctr-id", containerID)
		e := new(agent.Entry)
		e.AgentName = resolved.AgentName
		e.Project = resolved.Project
		e.ContainerID = containerID
		e.Thumbprint = thumb
		return e, nil
	}
	ing := new(registrymock.OtelForwarderMock)
	ing.ForwardFunc = func(context.Context, agentv1.Signal, []byte, otelgateway.Identity) error { return nil }
	h := newTestHandler(registry)
	h.OtelGateway = ing
	return &exportFixture{
		leaf:     leaf,
		resolved: resolved,
		registry: registry,
		gateway:  ing,
		handler:  h,
	}
}

// ctx carries both the peer cert and the resolved identity, the state
// the interceptor leaves for the handler.
func (f *exportFixture) ctx(t *testing.T) context.Context {
	t.Helper()
	return resolvedCtx(t, f.leaf, f.resolved)
}

func exportReq(signal agentv1.Signal, body []byte) *agentv1.ExportTelemetryRequest {
	req := new(agentv1.ExportTelemetryRequest)
	req.Signal = signal
	req.Body = body
	return req
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "status error expected, got %v", err)
	assert.Equal(t, want, st.Code(), "status message: %s", st.Message())
}

// TestExportTelemetry_HappyPath: a registered peer's batch reaches the gateway
// with the CP-resolved identity, never anything from the request.
func TestExportTelemetry_HappyPath(t *testing.T) {
	f := newExportFixture(t)
	body := []byte("otlp-bytes")

	res, err := f.handler.ExportTelemetry(f.ctx(t), exportReq(agentv1.Signal_SIGNAL_METRICS, body))
	require.NoError(t, err)
	require.NotNil(t, res)

	calls := f.gateway.ForwardCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, agentv1.Signal_SIGNAL_METRICS, calls[0].Signal)
	assert.Equal(t, body, calls[0].Body)
	assert.Equal(t, otelgateway.Identity{
		Project:     project,
		Agent:       agentName,
		ContainerID: "ctr-id",
	}, calls[0].ID)
}

// TestExportTelemetry_RequestValidation: client faults are classified before any
// identity work, so a bad request never touches the registry or the
// gateway.
func TestExportTelemetry_RequestValidation(t *testing.T) {
	oversize := make([]byte, agentv1.ExportTelemetryMaxBodyBytes+1)
	cases := []struct {
		name     string
		req      *agentv1.ExportTelemetryRequest
		wantCode codes.Code
	}{
		{"nil request", nil, codes.InvalidArgument},
		{"unspecified signal", exportReq(agentv1.Signal_SIGNAL_UNSPECIFIED, []byte("x")), codes.InvalidArgument},
		{"unknown signal value", exportReq(agentv1.Signal(99), []byte("x")), codes.InvalidArgument},
		{"body over cap", exportReq(agentv1.Signal_SIGNAL_LOGS, oversize), codes.ResourceExhausted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newExportFixture(t)
			_, err := f.handler.ExportTelemetry(f.ctx(t), tc.req)
			assertCode(t, err, tc.wantCode)
			assert.Empty(t, f.registry.LookupByContainerIDCalls())
			assert.Empty(t, f.gateway.ForwardCalls())
		})
	}
}

// TestExportTelemetry_BodyAtCapAccepted pins the boundary: exactly the cap is
// forwarded, one byte more is rejected (covered above).
func TestExportTelemetry_BodyAtCapAccepted(t *testing.T) {
	f := newExportFixture(t)
	body := make([]byte, agentv1.ExportTelemetryMaxBodyBytes)
	_, err := f.handler.ExportTelemetry(f.ctx(t), exportReq(agentv1.Signal_SIGNAL_LOGS, body))
	require.NoError(t, err)
	assert.Len(t, f.gateway.ForwardCalls(), 1)
}

// TestExportTelemetry_CtxGates mirrors TestRegister_CtxGates: no resolved container
// is a wiring bug (Internal); a resolved container without a peer cert
// is a defensive reject.
func TestExportTelemetry_CtxGates(t *testing.T) {
	resolved := resolvedFor(t, "p", "ctr-id")
	cases := []struct {
		name     string
		ctx      func() context.Context
		wantCode codes.Code
	}{
		{"missing resolved container", context.Background, codes.Internal},
		{
			"resolved present but peer cert stripped",
			func() context.Context { return agent.WithResolvedContainer(context.Background(), resolved) },
			codes.PermissionDenied,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newExportFixture(t)
			_, err := f.handler.ExportTelemetry(tc.ctx(), exportReq(agentv1.Signal_SIGNAL_LOGS, []byte("x")))
			assertCode(t, err, tc.wantCode)
			assert.Empty(t, f.gateway.ForwardCalls())
		})
	}
}

// TestExportTelemetry_RegistryGate: the live peer cert must be the one Register
// bound. No row, a different thumbprint, and a failing lookup each stop
// the batch before the gateway.
func TestExportTelemetry_RegistryGate(t *testing.T) {
	cases := []struct {
		name     string
		lookup   func(thumb [sha256.Size]byte) (*agent.Entry, error)
		wantCode codes.Code
	}{
		{
			"no registry row",
			func([sha256.Size]byte) (*agent.Entry, error) { return nil, agent.ErrUnknownAgent },
			codes.PermissionDenied,
		},
		{
			"thumbprint mismatch",
			func(thumb [sha256.Size]byte) (*agent.Entry, error) {
				thumb[0] ^= 0xff
				e := new(agent.Entry)
				e.ContainerID = "ctr-id"
				e.Thumbprint = thumb
				return e, nil
			},
			codes.PermissionDenied,
		},
		{
			"lookup error",
			func([sha256.Size]byte) (*agent.Entry, error) { return nil, errors.New("sqlite: locked") },
			codes.Internal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newExportFixture(t)
			// The mismatch case flips the live thumbprint, not a zero one.
			live := sha256.Sum256(f.leaf.Raw)
			f.registry.LookupByContainerIDFunc = func(string) (*agent.Entry, error) { return tc.lookup(live) }

			_, err := f.handler.ExportTelemetry(f.ctx(t), exportReq(agentv1.Signal_SIGNAL_LOGS, []byte("x")))
			assertCode(t, err, tc.wantCode)
			assert.Empty(t, f.gateway.ForwardCalls())
		})
	}
}

// TestExportTelemetry_ForwardOutcomes: the gateway's verdict maps onto the wire.
// A nil gateway (degraded CP) and a forward failure are Unavailable so
// the relay answers the SDK with a retryable status; a body the gateway
// cannot decode is the client's fault.
func TestExportTelemetry_ForwardOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		gateway  func(f *exportFixture)
		wantCode codes.Code
	}{
		{"gateway unavailable", func(f *exportFixture) { f.handler.OtelGateway = nil }, codes.Unavailable},
		{
			"forward failed",
			func(f *exportFixture) {
				f.gateway.ForwardFunc = func(context.Context, agentv1.Signal, []byte, otelgateway.Identity) error {
					return errors.New("otelgateway: forward logs: rpc error: code = Unavailable")
				}
			},
			codes.Unavailable,
		},
		{
			"malformed body",
			func(f *exportFixture) {
				f.gateway.ForwardFunc = func(context.Context, agentv1.Signal, []byte, otelgateway.Identity) error {
					return fmt.Errorf("%w: logs: boom", otelgateway.ErrMalformedBody)
				}
			},
			codes.InvalidArgument,
		},
		{
			"collector rejected the batch",
			func(f *exportFixture) {
				f.gateway.ForwardFunc = func(context.Context, agentv1.Signal, []byte, otelgateway.Identity) error {
					return fmt.Errorf("%w: %w", otelgateway.ErrCollectorRejected,
						status.Error(codes.InvalidArgument, "bad attribute"))
				}
			},
			codes.InvalidArgument,
		},
		{
			"batch over the resource cap",
			func(f *exportFixture) {
				f.gateway.ForwardFunc = func(context.Context, agentv1.Signal, []byte, otelgateway.Identity) error {
					return otelgateway.ErrBatchTooLarge
				}
			},
			codes.ResourceExhausted,
		},
		{
			"gateway busy",
			func(f *exportFixture) {
				f.gateway.ForwardFunc = func(context.Context, agentv1.Signal, []byte, otelgateway.Identity) error {
					return otelgateway.ErrBusy
				}
			},
			codes.Unavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newExportFixture(t)
			tc.gateway(f)
			_, err := f.handler.ExportTelemetry(f.ctx(t), exportReq(agentv1.Signal_SIGNAL_LOGS, []byte("x")))
			assertCode(t, err, tc.wantCode)
		})
	}
}
