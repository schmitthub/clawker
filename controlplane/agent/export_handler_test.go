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
	"github.com/schmitthub/clawker/controlplane/ingest"
	"github.com/schmitthub/clawker/internal/consts"
)

// exportFixture is a registered container: a leaf, the registry row that
// binds its thumbprint, and the handler wired with a recording gateway.
type exportFixture struct {
	leaf     *x509.Certificate
	resolved agent.ResolvedContainer
	registry *registrymock.RegistryMock
	ingest   *registrymock.TelemetryIngestMock
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
	ing := new(registrymock.TelemetryIngestMock)
	ing.IngestFunc = func(context.Context, agentv1.Signal, []byte, ingest.Identity) error { return nil }
	h := newTestHandler(registry)
	h.Ingest = ing
	return &exportFixture{
		leaf:     leaf,
		resolved: resolved,
		registry: registry,
		ingest:   ing,
		handler:  h,
	}
}

// ctx carries both the peer cert and the resolved identity, the state
// the interceptor leaves for the handler.
func (f *exportFixture) ctx(t *testing.T) context.Context {
	t.Helper()
	return resolvedCtx(t, f.leaf, f.resolved)
}

func exportReq(signal agentv1.Signal, body []byte) *agentv1.ExportRequest {
	req := new(agentv1.ExportRequest)
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

// TestExport_HappyPath: a registered peer's batch reaches the gateway
// with the CP-resolved identity, never anything from the request.
func TestExport_HappyPath(t *testing.T) {
	f := newExportFixture(t)
	body := []byte("otlp-bytes")

	res, err := f.handler.Export(f.ctx(t), exportReq(agentv1.Signal_SIGNAL_METRICS, body))
	require.NoError(t, err)
	require.NotNil(t, res)

	calls := f.ingest.IngestCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, agentv1.Signal_SIGNAL_METRICS, calls[0].Signal)
	assert.Equal(t, body, calls[0].Body)
	assert.Equal(t, ingest.Identity{
		Project:     project,
		Agent:       agentName,
		ContainerID: "ctr-id",
	}, calls[0].ID)
}

// TestExport_RequestValidation: client faults are classified before any
// identity work, so a bad request never touches the registry or the
// gateway.
func TestExport_RequestValidation(t *testing.T) {
	oversize := make([]byte, agentv1.ExportMaxBodyBytes+1)
	cases := []struct {
		name     string
		req      *agentv1.ExportRequest
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
			_, err := f.handler.Export(f.ctx(t), tc.req)
			assertCode(t, err, tc.wantCode)
			assert.Empty(t, f.registry.LookupByContainerIDCalls())
			assert.Empty(t, f.ingest.IngestCalls())
		})
	}
}

// TestExport_BodyAtCapAccepted pins the boundary: exactly the cap is
// forwarded, one byte more is rejected (covered above).
func TestExport_BodyAtCapAccepted(t *testing.T) {
	f := newExportFixture(t)
	body := make([]byte, agentv1.ExportMaxBodyBytes)
	_, err := f.handler.Export(f.ctx(t), exportReq(agentv1.Signal_SIGNAL_LOGS, body))
	require.NoError(t, err)
	assert.Len(t, f.ingest.IngestCalls(), 1)
}

// TestExport_CtxGates mirrors TestRegister_CtxGates: no resolved container
// is a wiring bug (Internal); a resolved container without a peer cert
// is a defensive reject.
func TestExport_CtxGates(t *testing.T) {
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
			_, err := f.handler.Export(tc.ctx(), exportReq(agentv1.Signal_SIGNAL_LOGS, []byte("x")))
			assertCode(t, err, tc.wantCode)
			assert.Empty(t, f.ingest.IngestCalls())
		})
	}
}

// TestExport_RegistryGate: the live peer cert must be the one Register
// bound. No row, a different thumbprint, and a failing lookup each stop
// the batch before the gateway.
func TestExport_RegistryGate(t *testing.T) {
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

			_, err := f.handler.Export(f.ctx(t), exportReq(agentv1.Signal_SIGNAL_LOGS, []byte("x")))
			assertCode(t, err, tc.wantCode)
			assert.Empty(t, f.ingest.IngestCalls())
		})
	}
}

// TestExport_IngestOutcomes: the gateway's verdict maps onto the wire.
// A nil gateway (degraded CP) and a forward failure are Unavailable so
// the relay answers the SDK with a retryable status; a body the gateway
// cannot decode is the client's fault.
func TestExport_IngestOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		ingest   func(f *exportFixture)
		wantCode codes.Code
	}{
		{"gateway unavailable", func(f *exportFixture) { f.handler.Ingest = nil }, codes.Unavailable},
		{
			"forward failed",
			func(f *exportFixture) {
				f.ingest.IngestFunc = func(context.Context, agentv1.Signal, []byte, ingest.Identity) error {
					return errors.New("ingest: forward logs: rpc error: code = Unavailable")
				}
			},
			codes.Unavailable,
		},
		{
			"malformed body",
			func(f *exportFixture) {
				f.ingest.IngestFunc = func(context.Context, agentv1.Signal, []byte, ingest.Identity) error {
					return fmt.Errorf("%w: logs: boom", ingest.ErrMalformedBody)
				}
			},
			codes.InvalidArgument,
		},
		{
			"batch over the resource cap",
			func(f *exportFixture) {
				f.ingest.IngestFunc = func(context.Context, agentv1.Signal, []byte, ingest.Identity) error {
					return ingest.ErrBatchTooLarge
				}
			},
			codes.ResourceExhausted,
		},
		{
			"gateway busy",
			func(f *exportFixture) {
				f.ingest.IngestFunc = func(context.Context, agentv1.Signal, []byte, ingest.Identity) error {
					return ingest.ErrBusy
				}
			},
			codes.Unavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newExportFixture(t)
			tc.ingest(f)
			_, err := f.handler.Export(f.ctx(t), exportReq(agentv1.Signal_SIGNAL_LOGS, []byte("x")))
			assertCode(t, err, tc.wantCode)
		})
	}
}
