package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/controlplane/otelgateway"
)

const exportIdentityRejected = "export: identity check failed"

// ExportTelemetry serves the agent telemetry lane. IdentityInterceptor has already
// pinned the CN, resolved the peer IP to a container, and compared the
// cert SAN against the labels. This handler adds the registry thumbprint
// check the dialer performs at Hello: the live peer cert must be the one
// Register bound to this container. Only then does the body reach the
// OTel gateway, with the identity the CP resolved itself.
//
// Every identity rejection returns PermissionDenied with the same
// envelope as Register; the structured log line carries the reason.
// Client faults (unknown signal, oversize body, body that does not
// decode) and a batch the collector refused as invalid return
// InvalidArgument or ResourceExhausted. A missing or
// failing gateway returns Unavailable so the clawkerd relay answers the
// SDK with a retryable status.
func (h *Handler) ExportTelemetry(
	ctx context.Context, req *agentv1.ExportTelemetryRequest,
) (*agentv1.ExportTelemetryResult, error) {
	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "export: nil request")
	}
	switch req.GetSignal() {
	case agentv1.Signal_SIGNAL_LOGS, agentv1.Signal_SIGNAL_METRICS, agentv1.Signal_SIGNAL_TRACES:
	case agentv1.Signal_SIGNAL_UNSPECIFIED:
		return nil, status.Errorf(codes.InvalidArgument, "export: signal required")
	default:
		return nil, status.Errorf(codes.InvalidArgument, "export: unknown signal")
	}
	if len(req.GetBody()) > agentv1.ExportTelemetryMaxBodyBytes {
		return nil, status.Errorf(codes.ResourceExhausted, "export: body over the size cap")
	}

	resolved, err := h.verifyTelemetryPeer(ctx)
	if err != nil {
		return nil, err
	}
	if h.OtelGateway == nil {
		return nil, status.Errorf(codes.Unavailable, "export telemetry: otel gateway unavailable")
	}
	id := otelgateway.Identity{
		Project:     resolved.Project.String(),
		Agent:       resolved.AgentName.String(),
		ContainerID: resolved.ContainerID,
	}
	if ingestErr := h.OtelGateway.Forward(ctx, req.GetSignal(), req.GetBody(), id); ingestErr != nil {
		if errors.Is(ingestErr, otelgateway.ErrBatchTooLarge) {
			return nil, status.Errorf(codes.ResourceExhausted, "export: batch over the size cap")
		}
		if errors.Is(ingestErr, otelgateway.ErrMalformedBody) {
			h.Log.Warn().Err(ingestErr).
				Str("event", "agent_export_malformed_body").
				Str("container_id", resolved.ContainerID).
				Msg("ExportTelemetry body did not decode as the named signal")
			return nil, status.Errorf(codes.InvalidArgument, "export: body does not decode")
		}
		if errors.Is(ingestErr, otelgateway.ErrCollectorRejected) {
			return nil, status.Errorf(codes.InvalidArgument, "export: collector rejected the batch")
		}
		return nil, status.Errorf(codes.Unavailable, "export: forward failed")
	}
	return &agentv1.ExportTelemetryResult{}, nil
}

// verifyTelemetryPeer reads the interceptor-resolved container and checks
// the live peer cert against the registry row Register wrote. The
// returned error is already a gRPC status.
func (h *Handler) verifyTelemetryPeer(ctx context.Context) (ResolvedContainer, error) {
	resolved, ok := ResolvedContainerFromContext(ctx)
	if !ok {
		h.Log.Error().
			Str("event", "agent_export_no_resolved_container").
			Msg("middleware did not attach ResolvedContainer to ctx — wiring bug")
		return ResolvedContainer{}, status.Errorf(codes.Internal, "export: identity not resolved")
	}
	leaf, err := peerLeafFromContext(ctx)
	if err != nil {
		h.Log.Warn().Err(err).
			Str("event", "agent_export_peer_cert_missing").
			Str("container_id", resolved.ContainerID).
			Msg("ctx carried resolved container but no peer cert — defensive reject")
		return ResolvedContainer{}, status.Errorf(codes.PermissionDenied, exportIdentityRejected)
	}
	thumbprint := sha256.Sum256(leaf.Raw)

	entry, err := h.Registry.LookupByContainerID(resolved.ContainerID)
	switch {
	case errors.Is(err, ErrUnknownAgent):
		h.Log.Warn().
			Str("event", "agent_export_unregistered").
			Str("container_id", resolved.ContainerID).
			Msg("ExportTelemetry from a container with no registry row; Register has not completed")
		return ResolvedContainer{}, status.Errorf(codes.PermissionDenied, exportIdentityRejected)
	case err != nil:
		h.Log.Warn().Err(err).
			Str("event", "agent_export_lookup_error").
			Str("container_id", resolved.ContainerID).
			Msg("registry lookup failed")
		return ResolvedContainer{}, status.Errorf(codes.Internal, "export: lookup failed")
	case subtle.ConstantTimeCompare(entry.Thumbprint[:], thumbprint[:]) != 1:
		h.Log.Warn().
			Str("event", "agent_export_thumbprint_mismatch").
			Str("container_id", resolved.ContainerID).
			Msg("peer cert thumbprint does not match the registry row")
		return ResolvedContainer{}, status.Errorf(codes.PermissionDenied, exportIdentityRejected)
	}
	return resolved, nil
}
