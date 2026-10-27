// Package hostgateway is the CP side of the AgentService host actions. The
// agent handler resolves and authorizes the caller; the gateway keeps the
// callback-session ownership records and calls the host proxy over mTLS.
// The host proxy applies the egress rules, validates the callback
// redirect, binds the callback port and opens the browser. agent imports
// hostgateway; hostgateway never imports agent.
package hostgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
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
	// ErrUnavailable: the host proxy is unreachable or had a fault.
	ErrUnavailable = errors.New("host proxy unavailable")
)

// maxResponseBytes caps a host-proxy response body.
const maxResponseBytes = 1 << 20

// Identity is the CP-resolved caller. The gateway uses ContainerID as the
// owner of a callback session.
type Identity struct {
	Project     string
	Agent       string
	ContainerID string
}

// Gateway calls the host proxy for authorized host actions.
type Gateway struct {
	// Clock returns the current time; nil means time.Now.
	Clock func() time.Time

	baseURL string
	client  *http.Client
	log     *logger.Logger

	mu     sync.Mutex
	owners map[string]owner
}

type owner struct {
	containerID string
	expires     time.Time
}

// New returns a Gateway that calls the host proxy at baseURL with client.
// client carries the CP's mTLS client certificate.
func New(baseURL string, client *http.Client, log *logger.Logger) (*Gateway, error) {
	if baseURL == "" {
		return nil, errors.New("hostgateway.New: base URL is required")
	}
	if client == nil {
		return nil, errors.New("hostgateway.New: HTTP client is required")
	}
	if log == nil {
		log = logger.Nop()
	}
	return &Gateway{
		Clock:   nil,
		mu:      sync.Mutex{},
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  client,
		log:     log,
		owners:  make(map[string]owner),
	}, nil
}

// OpenURL asks the host proxy to open rawURL in the host browser.
func (g *Gateway) OpenURL(ctx context.Context, id Identity, rawURL string) error {
	var res successResponse
	return g.call(ctx, id, http.MethodPost, "/open/url", openURLRequest{URL: rawURL}, &res)
}

// gitAction maps the proto action to the host proxy's action name.
func gitAction(a agentv1.GitCredentialAction) (string, bool) {
	switch a {
	case agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET:
		return "get", true
	case agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_STORE:
		return "store", true
	case agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_ERASE:
		return "erase", true
	case agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_UNSPECIFIED:
	}
	return "", false
}

// GitCredential runs one git credential helper action on the host.
func (g *Gateway) GitCredential(
	ctx context.Context, id Identity, req *agentv1.GitCredentialRequest,
) (*agentv1.GitCredentialResult, error) {
	action, ok := gitAction(req.GetAction())
	if !ok {
		return nil, fmt.Errorf("%w: unknown git credential action", ErrInvalidRequest)
	}
	body := gitCredentialRequest{
		Action:   action,
		Protocol: req.GetProtocol(),
		Host:     req.GetHost(),
		Path:     req.GetPath(),
		Username: req.GetUsername(),
		Password: req.GetPassword(),
	}
	var res gitCredentialResponse
	if err := g.call(ctx, id, http.MethodPost, "/git/credential", body, &res); err != nil {
		return nil, err
	}
	if !res.Success {
		return nil, fmt.Errorf("%w: %s", ErrActionFailed, res.Error)
	}
	return &agentv1.GitCredentialResult{
		Protocol: res.Protocol,
		Host:     res.Host,
		Username: res.Username,
		Password: res.Password,
	}, nil
}

// RegisterCallback starts the one-shot OAuth flow on the host and records
// the caller as the owner of the new session.
func (g *Gateway) RegisterCallback(
	ctx context.Context, id Identity, req *agentv1.RegisterCallbackRequest,
) (*agentv1.RegisterCallbackResult, error) {
	body := registerCallbackRequest{AuthURL: req.GetAuthUrl(), TTLSeconds: req.GetTtlSeconds()}
	var res registerCallbackResponse
	if err := g.call(ctx, id, http.MethodPost, "/callback/register", body, &res); err != nil {
		return nil, err
	}
	expires, err := time.Parse(time.RFC3339, res.ExpiresAt)
	if err != nil || res.SessionID == "" {
		return nil, fmt.Errorf("%w: malformed register response", ErrUnavailable)
	}

	g.mu.Lock()
	now := g.now()
	for sid, o := range g.owners {
		if !now.Before(o.expires) {
			delete(g.owners, sid)
		}
	}
	g.owners[res.SessionID] = owner{containerID: id.ContainerID, expires: expires}
	g.mu.Unlock()

	return &agentv1.RegisterCallbackResult{
		SessionId:     res.SessionID,
		Port:          res.Port,
		ExpiresAtUnix: expires.Unix(),
	}, nil
}

// GetCallbackData polls a callback session the caller owns.
func (g *Gateway) GetCallbackData(
	ctx context.Context, id Identity, sessionID string,
) (*agentv1.GetCallbackDataResult, error) {
	if err := g.checkOwner(id, sessionID); err != nil {
		return nil, err
	}
	var res callbackDataResponse
	path := "/callback/" + url.PathEscape(sessionID) + "/data"
	if err := g.call(ctx, id, http.MethodGet, path, nil, &res); err != nil {
		return nil, err
	}
	out := &agentv1.GetCallbackDataResult{Received: res.Received, Callback: nil}
	if cb := res.Callback; res.Received && cb != nil {
		out.Callback = &agentv1.CallbackData{
			Method:         cb.Method,
			Path:           cb.Path,
			Query:          cb.Query,
			Headers:        cb.Headers,
			Body:           cb.Body,
			ReceivedAtUnix: cb.ReceivedAt.Unix(),
		}
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
	var res successResponse
	return g.call(ctx, id, http.MethodDelete, "/callback/"+url.PathEscape(sessionID), nil, &res)
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

// call sends one request to the host proxy and decodes a 200 response
// into out. Any other status maps to one of the package errors.
func (g *Gateway) call(ctx context.Context, id Identity, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%w: encode request: %w", ErrInvalidRequest, err)
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("%w: build request: %w", ErrInvalidRequest, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		g.log.Warn().Err(err).
			Str("event", "host_proxy_unreachable").
			Str("container_id", id.ContainerID).
			Str("path", path).
			Msg("host proxy request failed")
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: read response: %w", ErrUnavailable, err)
	}

	g.log.Info().
		Str("event", "host_action").
		Str("container_id", id.ContainerID).
		Str("project", id.Project).
		Str("agent", id.Agent).
		Str("method", method).
		Str("path", path).
		Int("status", resp.StatusCode).
		Msg("host proxy action")

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return ErrDenied
	case http.StatusBadRequest:
		return ErrInvalidRequest
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	if decodeErr := json.Unmarshal(raw, out); decodeErr != nil {
		return fmt.Errorf("%w: decode response: %w", ErrUnavailable, decodeErr)
	}
	return nil
}

// The host proxy's CP-facing JSON API. internal/hostproxy holds the server
// side of the same shapes.

type successResponse struct {
	Success bool `json:"success"`
}

type openURLRequest struct {
	URL string `json:"url"`
}

type gitCredentialRequest struct {
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Path     string `json:"path"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type gitCredentialResponse struct {
	Success  bool   `json:"success"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
	Error    string `json:"error"`
}

type registerCallbackRequest struct {
	AuthURL    string `json:"authUrl"`
	TTLSeconds int32  `json:"ttlSeconds"`
}

type registerCallbackResponse struct {
	SessionID string `json:"sessionId"`
	Port      int32  `json:"port"`
	ExpiresAt string `json:"expiresAt"`
}

type callbackDataResponse struct {
	Received bool `json:"received"`
	Callback *struct {
		Method     string            `json:"method"`
		Path       string            `json:"path"`
		Query      string            `json:"query"`
		Headers    map[string]string `json:"headers"`
		Body       string            `json:"body"`
		ReceivedAt time.Time         `json:"receivedAt"`
	} `json:"callback"`
}
