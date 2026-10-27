// Package hostgateway is the CP side of the AgentService host actions. The
// agent handler resolves and authorizes the caller; the gateway keeps the
// callback-session ownership records and sends each action to the host
// proxy over the host proxy's own HostProxyService.Connect stream. The
// host proxy dials out to the CP admin listener; the CP never dials the
// host. The host proxy applies the egress rules, validates the callback
// redirect, binds the callback port and opens the browser. agent imports
// hostgateway; hostgateway never imports agent.
package hostgateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	hostproxyv1 "github.com/schmitthub/clawker/api/hostproxy/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// Errors the agent handler maps to gRPC status codes.
var (
	// ErrNotOwner: the callback session is unknown, expired, or belongs to
	// another container. One error for all three, so a caller cannot probe
	// for sessions of other containers.
	ErrNotOwner = errors.New("callback session not owned by caller")
	// ErrDenied: the host proxy refused the action (egress policy).
	ErrDenied = errors.New("host proxy denied the action")
	// ErrInvalidRequest: the host proxy rejected the request as malformed.
	ErrInvalidRequest = errors.New("host proxy rejected the request")
	// ErrNotFound: the host proxy has no such callback session.
	ErrNotFound = errors.New("host proxy has no such callback session")
	// ErrActionFailed: the host proxy ran the action and it failed (for
	// example, the git credential helper exited non-zero).
	ErrActionFailed = errors.New("host action failed")
	// ErrUnavailable: no host proxy is connected, its stream ended, or it
	// had a fault.
	ErrUnavailable = errors.New("host proxy unavailable")
)

// callTimeout bounds one host action. A git credential helper can prompt
// the OS keychain, so it is longer than a plain request.
const callTimeout = 60 * time.Second

// Identity is the CP-resolved caller. The gateway uses ContainerID as the
// owner of a callback session.
type Identity struct {
	Project     string
	Agent       string
	ContainerID string
}

// Gateway sends authorized host actions to the connected host proxy. It
// is the HostProxyService server: the host proxy's Connect stream is the
// only path to the host.
type Gateway struct {
	hostproxyv1.UnimplementedHostProxyServiceServer

	// Clock returns the current time; nil means time.Now.
	Clock func() time.Time

	log    *logger.Logger
	nextID atomic.Uint64

	mu     sync.Mutex
	stream *hostStream
	owners map[string]owner
}

type owner struct {
	containerID string
	expires     time.Time
}

// New returns a Gateway with no host proxy connected. Every host action
// answers ErrUnavailable until a host proxy opens a Connect stream.
func New(log *logger.Logger) *Gateway {
	if log == nil {
		log = logger.Nop()
	}
	return &Gateway{
		UnimplementedHostProxyServiceServer: hostproxyv1.UnimplementedHostProxyServiceServer{},
		Clock:                               nil,
		log:                                 log,
		nextID:                              atomic.Uint64{},
		mu:                                  sync.Mutex{},
		stream:                              nil,
		owners:                              make(map[string]owner),
	}
}

// errReplaced ends a stream that a newer host proxy stream replaced.
var errReplaced = status.Errorf(codes.Aborted, "replaced by a newer host proxy stream")

// Connect is the host proxy's stream. The newest stream replaces the
// previous one; calls in flight on the previous stream fail as
// unavailable. The stream ends when the host proxy goes away or a newer
// stream replaces it.
func (g *Gateway) Connect(srv hostproxyv1.HostProxyService_ConnectServer) error {
	if !isHostProxyPeer(srv.Context()) {
		g.log.Warn().
			Str("event", "host_proxy_stream_rejected").
			Msg("HostProxyService stream from a client cert that is not the host proxy")
		return status.Errorf(codes.PermissionDenied, "not the host proxy")
	}

	hs := newHostStream(srv)
	g.mu.Lock()
	old := g.stream
	g.stream = hs
	g.mu.Unlock()
	if old != nil {
		old.close(errReplaced)
	}
	// The response header tells the host proxy the CP accepted the stream.
	if err := srv.SendHeader(metadata.Pairs(hostproxyv1.AcceptedHeader, "true")); err != nil {
		g.drop(hs, err)
		return fmt.Errorf("send stream header: %w", err)
	}
	g.log.Info().Str("event", "host_proxy_connected").Bool("replaced", old != nil).Msg("host proxy stream connected")

	recvDone := make(chan error, 1)
	go func() { recvDone <- hs.recvLoop() }()
	var err error
	select {
	case err = <-recvDone:
	case <-hs.done:
		err = hs.err
	}
	g.drop(hs, err)
	g.log.Info().Str("event", "host_proxy_disconnected").Err(err).Msg("host proxy stream ended")
	if errors.Is(err, errReplaced) {
		return errReplaced
	}
	return nil
}

// drop closes hs and clears it unless a newer stream already replaced it.
func (g *Gateway) drop(hs *hostStream, cause error) {
	g.mu.Lock()
	if g.stream == hs {
		g.stream = nil
	}
	g.mu.Unlock()
	hs.close(cause)
}

// isHostProxyPeer reports whether the verified client cert names the host
// proxy. The TLS layer already verified the chain to the CLI CA.
func isHostProxyPeer(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return false
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.VerifiedChains[0]) == 0 {
		return false
	}
	cn := info.State.VerifiedChains[0][0].Subject.CommonName
	return subtle.ConstantTimeCompare([]byte(cn), []byte(consts.HostProxyClientName)) == 1
}

// OpenURL asks the host proxy to open rawURL in the host browser.
func (g *Gateway) OpenURL(ctx context.Context, id Identity, rawURL string) error {
	req := &hostproxyv1.HostActionRequest{Id: 0, Action: &hostproxyv1.HostActionRequest_OpenUrl{
		OpenUrl: &agentv1.OpenURLRequest{Url: rawURL},
	}}
	res, err := g.call(ctx, id, req)
	if err != nil {
		return err
	}
	if res.GetOpenUrl() == nil {
		return wrongResult()
	}
	return nil
}

// GitCredential runs one git credential helper action on the host.
func (g *Gateway) GitCredential(
	ctx context.Context, id Identity, req *agentv1.GitCredentialRequest,
) (*agentv1.GitCredentialResult, error) {
	res, err := g.call(
		ctx,
		id,
		&hostproxyv1.HostActionRequest{Id: 0, Action: &hostproxyv1.HostActionRequest_GitCredential{
			GitCredential: req,
		}},
	)
	if err != nil {
		return nil, err
	}
	out := res.GetGitCredential()
	if out == nil {
		return nil, wrongResult()
	}
	return out, nil
}

// RegisterCallback starts the one-shot OAuth flow on the host and records
// the caller as the owner of the new session.
func (g *Gateway) RegisterCallback(
	ctx context.Context, id Identity, req *agentv1.RegisterCallbackRequest,
) (*agentv1.RegisterCallbackResult, error) {
	res, err := g.call(
		ctx,
		id,
		&hostproxyv1.HostActionRequest{Id: 0, Action: &hostproxyv1.HostActionRequest_RegisterCallback{
			RegisterCallback: req,
		}},
	)
	if err != nil {
		return nil, err
	}
	out := res.GetRegisterCallback()
	if out == nil || out.GetSessionId() == "" {
		return nil, wrongResult()
	}

	g.mu.Lock()
	now := g.now()
	for sid, o := range g.owners {
		if !now.Before(o.expires) {
			delete(g.owners, sid)
		}
	}
	g.owners[out.GetSessionId()] = owner{containerID: id.ContainerID, expires: time.Unix(out.GetExpiresAtUnix(), 0)}
	g.mu.Unlock()
	return out, nil
}

// GetCallbackData polls a callback session the caller owns.
func (g *Gateway) GetCallbackData(
	ctx context.Context, id Identity, sessionID string,
) (*agentv1.GetCallbackDataResult, error) {
	if err := g.checkOwner(id, sessionID); err != nil {
		return nil, err
	}
	res, err := g.call(
		ctx,
		id,
		&hostproxyv1.HostActionRequest{Id: 0, Action: &hostproxyv1.HostActionRequest_GetCallbackData{
			GetCallbackData: &agentv1.GetCallbackDataRequest{SessionId: sessionID},
		}},
	)
	if err != nil {
		return nil, err
	}
	out := res.GetGetCallbackData()
	if out == nil {
		return nil, wrongResult()
	}
	return out, nil
}

// DeleteCallback ends a callback session the caller owns.
func (g *Gateway) DeleteCallback(ctx context.Context, id Identity, sessionID string) error {
	if err := g.checkOwner(id, sessionID); err != nil {
		return err
	}
	g.mu.Lock()
	delete(g.owners, sessionID)
	g.mu.Unlock()
	res, err := g.call(
		ctx,
		id,
		&hostproxyv1.HostActionRequest{Id: 0, Action: &hostproxyv1.HostActionRequest_DeleteCallback{
			DeleteCallback: &agentv1.DeleteCallbackRequest{SessionId: sessionID},
		}},
	)
	if err != nil {
		return err
	}
	if res.GetDeleteCallback() == nil {
		return wrongResult()
	}
	return nil
}

func (g *Gateway) checkOwner(id Identity, sessionID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	o, ok := g.owners[sessionID]
	switch {
	case !ok:
	case !g.now().Before(o.expires):
		delete(g.owners, sessionID)
	case o.containerID == id.ContainerID:
		return nil
	}
	g.log.Warn().
		Str("event", "host_callback_not_owner").
		Str("container_id", id.ContainerID).
		Msg("callback session request from a container that does not own the session")
	return ErrNotOwner
}

func (g *Gateway) now() time.Time {
	if g.Clock != nil {
		return g.Clock()
	}
	return time.Now()
}

func wrongResult() error {
	return fmt.Errorf("%w: result does not match the request", ErrUnavailable)
}

// call sends one request down the host proxy stream and waits for its
// result. A result error maps to one of the package errors.
func (g *Gateway) call(
	ctx context.Context, id Identity, req *hostproxyv1.HostActionRequest,
) (*hostproxyv1.HostActionResult, error) {
	g.mu.Lock()
	hs := g.stream
	g.mu.Unlock()
	if hs == nil {
		g.log.Warn().
			Str("event", "host_proxy_not_connected").
			Str("container_id", id.ContainerID).
			Msg("host action with no host proxy connected")
		return nil, fmt.Errorf("%w: no host proxy connected", ErrUnavailable)
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req.Id = g.nextID.Add(1)
	res, err := hs.roundTrip(ctx, req)
	action := actionName(req)
	if err != nil {
		g.log.Warn().Err(err).
			Str("event", "host_proxy_unreachable").
			Str("container_id", id.ContainerID).
			Str("action", action).
			Msg("host proxy request failed")
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	code := codes.OK
	if e := res.GetError(); e != nil {
		code = codes.Code(e.GetCode())
	}
	g.log.Info().
		Str("event", "host_action").
		Str("container_id", id.ContainerID).
		Str("project", id.Project).
		Str("agent", id.Agent).
		Str("action", action).
		Str("code", code.String()).
		Msg("host proxy action")

	if e := res.GetError(); e != nil {
		return nil, resultError(code, e.GetMessage())
	}
	return res, nil
}

// resultError maps a host proxy error code to a package error.
func resultError(code codes.Code, msg string) error {
	switch code {
	case codes.PermissionDenied:
		return fmt.Errorf("%w: %s", ErrDenied, msg)
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", ErrInvalidRequest, msg)
	case codes.NotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case codes.FailedPrecondition:
		return fmt.Errorf("%w: %s", ErrActionFailed, msg)
	case codes.OK, codes.Canceled, codes.Unknown, codes.DeadlineExceeded, codes.AlreadyExists,
		codes.ResourceExhausted, codes.Aborted, codes.OutOfRange, codes.Unimplemented, codes.Internal,
		codes.Unavailable, codes.DataLoss, codes.Unauthenticated:
	}
	return fmt.Errorf("%w: %s: %s", ErrUnavailable, code, msg)
}

func actionName(req *hostproxyv1.HostActionRequest) string {
	switch req.GetAction().(type) {
	case *hostproxyv1.HostActionRequest_OpenUrl:
		return "open_url"
	case *hostproxyv1.HostActionRequest_GitCredential:
		return "git_credential"
	case *hostproxyv1.HostActionRequest_RegisterCallback:
		return "register_callback"
	case *hostproxyv1.HostActionRequest_GetCallbackData:
		return "get_callback_data"
	case *hostproxyv1.HostActionRequest_DeleteCallback:
		return "delete_callback"
	}
	return "unknown"
}

// hostStream is one connected host proxy stream with its calls in flight.
type hostStream struct {
	srv hostproxyv1.HostProxyService_ConnectServer

	sendMu sync.Mutex // gRPC allows one Send at a time

	mu      sync.Mutex
	pending map[uint64]chan *hostproxyv1.HostActionResult
	closed  bool
	err     error
	done    chan struct{}
}

func newHostStream(srv hostproxyv1.HostProxyService_ConnectServer) *hostStream {
	return &hostStream{
		srv:     srv,
		sendMu:  sync.Mutex{},
		mu:      sync.Mutex{},
		pending: make(map[uint64]chan *hostproxyv1.HostActionResult),
		closed:  false,
		err:     nil,
		done:    make(chan struct{}),
	}
}

// roundTrip sends req and waits for the result with the same id.
func (hs *hostStream) roundTrip(
	ctx context.Context, req *hostproxyv1.HostActionRequest,
) (*hostproxyv1.HostActionResult, error) {
	ch := make(chan *hostproxyv1.HostActionResult, 1)
	hs.mu.Lock()
	if hs.closed {
		hs.mu.Unlock()
		return nil, errors.New("host proxy stream closed")
	}
	hs.pending[req.GetId()] = ch
	hs.mu.Unlock()
	defer func() {
		hs.mu.Lock()
		delete(hs.pending, req.GetId())
		hs.mu.Unlock()
	}()

	hs.sendMu.Lock()
	err := hs.srv.Send(req)
	hs.sendMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("send to host proxy: %w", err)
	}

	select {
	case res := <-ch:
		return res, nil
	case <-hs.done:
		return nil, fmt.Errorf("host proxy stream ended: %w", hs.err)
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for host proxy: %w", ctx.Err())
	}
}

// recvLoop delivers each result to the call waiting for its id. A result
// with no waiting call (the call already timed out) or a second result for
// the same id is dropped.
func (hs *hostStream) recvLoop() error {
	for {
		res, err := hs.srv.Recv()
		if err != nil {
			return fmt.Errorf("receive from host proxy: %w", err)
		}
		hs.mu.Lock()
		ch, ok := hs.pending[res.GetId()]
		hs.mu.Unlock()
		if ok {
			select {
			case ch <- res:
			default:
			}
		}
	}
}

// close ends the stream once and fails every call in flight.
func (hs *hostStream) close(cause error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.closed {
		return
	}
	hs.closed = true
	hs.err = cause
	close(hs.done)
}
