package agent

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/controlplane/hostgateway"
)

const hostIdentityRejected = "host action: identity check failed"

// The host actions put the host proxy behind the CP. IdentityInterceptor
// has already resolved the peer to a container. authorizeHost adds the
// registry thumbprint check and the container's grants; only then does the
// request reach Handler.HostGateway, with the identity the CP resolved.

// OpenURL opens a URL in the host browser.
func (h *Handler) OpenURL(ctx context.Context, req *agentv1.OpenURLRequest) (*agentv1.OpenURLResult, error) {
	if req.GetUrl() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "open url: url required")
	}
	id, err := h.authorizeHost(ctx, false)
	if err != nil {
		return nil, err
	}
	if fwdErr := h.HostGateway.OpenURL(ctx, id, req.GetUrl()); fwdErr != nil {
		return nil, hostStatus(fwdErr)
	}
	return &agentv1.OpenURLResult{}, nil
}

// GitCredential runs one git credential helper action on the host. It
// needs the container's HTTPS git credential grant.
func (h *Handler) GitCredential(
	ctx context.Context, req *agentv1.GitCredentialRequest,
) (*agentv1.GitCredentialResult, error) {
	if req.GetAction() == agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_UNSPECIFIED {
		return nil, status.Errorf(codes.InvalidArgument, "git credential: action required")
	}
	id, err := h.authorizeHost(ctx, true)
	if err != nil {
		return nil, err
	}
	res, err := h.HostGateway.GitCredential(ctx, id, req)
	if err != nil {
		return nil, hostStatus(err)
	}
	return res, nil
}

// RegisterCallback starts the one-shot OAuth callback flow on the host.
func (h *Handler) RegisterCallback(
	ctx context.Context, req *agentv1.RegisterCallbackRequest,
) (*agentv1.RegisterCallbackResult, error) {
	if req.GetAuthUrl() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "register callback: auth_url required")
	}
	id, err := h.authorizeHost(ctx, false)
	if err != nil {
		return nil, err
	}
	res, err := h.HostGateway.RegisterCallback(ctx, id, req)
	if err != nil {
		return nil, hostStatus(err)
	}
	return res, nil
}

// GetCallbackData polls a callback session the caller owns.
func (h *Handler) GetCallbackData(
	ctx context.Context, req *agentv1.GetCallbackDataRequest,
) (*agentv1.GetCallbackDataResult, error) {
	if req.GetSessionId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "callback data: session_id required")
	}
	id, err := h.authorizeHost(ctx, false)
	if err != nil {
		return nil, err
	}
	res, err := h.HostGateway.GetCallbackData(ctx, id, req.GetSessionId())
	if err != nil {
		return nil, hostStatus(err)
	}
	return res, nil
}

// DeleteCallback ends a callback session the caller owns.
func (h *Handler) DeleteCallback(
	ctx context.Context, req *agentv1.DeleteCallbackRequest,
) (*agentv1.DeleteCallbackResult, error) {
	if req.GetSessionId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "delete callback: session_id required")
	}
	id, err := h.authorizeHost(ctx, false)
	if err != nil {
		return nil, err
	}
	if fwdErr := h.HostGateway.DeleteCallback(ctx, id, req.GetSessionId()); fwdErr != nil {
		return nil, hostStatus(fwdErr)
	}
	return &agentv1.DeleteCallbackResult{}, nil
}

// authorizeHost verifies the registered peer and its grants: the host
// proxy grant for every action, the git HTTPS grant when needGit is set.
// The returned error is already a gRPC status.
func (h *Handler) authorizeHost(ctx context.Context, needGit bool) (hostgateway.Identity, error) {
	resolved, err := h.verifyRegisteredPeer(ctx, "host", hostIdentityRejected)
	if err != nil {
		return hostgateway.Identity{}, err
	}
	if !resolved.HostProxy || (needGit && !resolved.GitHTTPS) {
		h.Log.Warn().
			Str("event", "agent_host_not_granted").
			Str("container_id", resolved.ContainerID).
			Bool("host_proxy", resolved.HostProxy).
			Bool("git_https", resolved.GitHTTPS).
			Bool("git_requested", needGit).
			Msg("host action not granted to this container")
		return hostgateway.Identity{}, status.Errorf(codes.PermissionDenied, "host action not granted to this agent")
	}
	if h.HostGateway == nil {
		return hostgateway.Identity{}, status.Errorf(codes.Unavailable, "host action: host gateway unavailable")
	}
	return hostgateway.Identity{
		Project:     resolved.Project.String(),
		Agent:       resolved.AgentName.String(),
		ContainerID: resolved.ContainerID,
	}, nil
}

// hostStatus maps a gateway error to the status the clawkerd relay turns
// into an HTTP answer.
func hostStatus(err error) error {
	switch {
	case errors.Is(err, hostgateway.ErrNotOwner), errors.Is(err, hostgateway.ErrDenied):
		return status.Errorf(codes.PermissionDenied, "%s", err)
	case errors.Is(err, hostgateway.ErrInvalidRequest):
		return status.Errorf(codes.InvalidArgument, "%s", err)
	case errors.Is(err, hostgateway.ErrNotFound):
		return status.Errorf(codes.NotFound, "%s", err)
	case errors.Is(err, hostgateway.ErrActionFailed):
		return status.Errorf(codes.FailedPrecondition, "%s", err)
	default:
		return status.Errorf(codes.Unavailable, "host action: host proxy unavailable")
	}
}
