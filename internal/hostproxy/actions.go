// Package hostproxy runs host actions for agents: open a URL in the host
// browser, run the host git credential helper, and capture OAuth
// callbacks. The daemon dials out to the control plane and serves the
// actions the CP sends on its HostProxyService stream; it has no listener
// for actions. Only the short-lived OAuth callback listeners bind host
// loopback ports.
package hostproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	hostproxyv1 "github.com/schmitthub/clawker/api/hostproxy/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// URL schemes the browser may open.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// errEgressRulesUnavailable is the error message for an egress rules file
// that is present but corrupt or unreadable: an infrastructure fault that
// fails closed, not a per-request policy decision.
const errEgressRulesUnavailable = "egress rules unavailable"

// dynamicListener tracks a temporary HTTP listener for OAuth callbacks.
type dynamicListener struct {
	port      int
	sessionID string
	listeners []net.Listener
	servers   []*http.Server
}

// Actions runs the host actions the CP sends. The CP has already
// authorized the agent; Actions applies the egress rules to URLs,
// validates the callback redirect, binds the callback port and opens the
// browser.
type Actions struct {
	log              *logger.Logger
	rulesFilePath    string             // egress rules file path; empty = skip check (firewall disabled)
	browserFunc      func(string) error // opens URL in host browser; defaults to openBrowser
	mu               sync.RWMutex
	closed           bool
	sessionStore     *SessionStore
	callbackChannel  *CallbackChannel
	dynamicListeners map[int]*dynamicListener // port -> listener
	portToSession    map[int]string           // port -> sessionID for lookups
}

// NewActions returns Actions that check URLs against the egress rules at
// rulesFilePath (empty skips the check: the firewall is off). Close
// releases the callback listeners and the session store.
func NewActions(log *logger.Logger, rulesFilePath string) *Actions {
	sessionStore := NewSessionStore()
	a := &Actions{
		log:              log,
		rulesFilePath:    rulesFilePath,
		browserFunc:      openBrowser,
		mu:               sync.RWMutex{},
		closed:           false,
		sessionStore:     sessionStore,
		callbackChannel:  NewCallbackChannel(sessionStore, log),
		dynamicListeners: make(map[int]*dynamicListener),
		portToSession:    make(map[int]string),
	}

	// A deleted or expired callback session releases its listener.
	sessionStore.SetOnDelete(func(session *Session) {
		if session.Type != CallbackSessionType {
			return
		}
		portVal, ok := session.GetMetadata(metadataPort)
		if !ok {
			return
		}
		port, ok := portVal.(int)
		if !ok {
			return
		}
		// Session expiry runs on the store's cleanup goroutine; there is
		// no caller context.
		a.closeDynamicListener(context.Background(), port)
	})
	return a
}

// Close closes every callback listener and stops the session store.
func (s *Actions) Close(ctx context.Context) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for port := range s.dynamicListeners {
		s.closeDynamicListenerLocked(ctx, port)
	}
	s.mu.Unlock()
	s.sessionStore.Stop()
}

// Handle runs one host action and returns its result with the request id.
// A refused or failed action is a HostActionError with a gRPC code the CP
// maps for the agent.
func (s *Actions) Handle(ctx context.Context, req *hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
	res := s.handle(ctx, req)
	res.Id = req.GetId()
	return res
}

func (s *Actions) handle(ctx context.Context, req *hostproxyv1.HostActionRequest) *hostproxyv1.HostActionResult {
	switch action := req.GetAction().(type) {
	case *hostproxyv1.HostActionRequest_OpenUrl:
		return s.openURL(action.OpenUrl)
	case *hostproxyv1.HostActionRequest_GitCredential:
		return s.gitCredential(ctx, action.GitCredential)
	case *hostproxyv1.HostActionRequest_RegisterCallback:
		return s.registerCallback(ctx, action.RegisterCallback)
	case *hostproxyv1.HostActionRequest_GetCallbackData:
		return s.getCallbackData(action.GetCallbackData.GetSessionId())
	case *hostproxyv1.HostActionRequest_DeleteCallback:
		return s.deleteCallback(action.DeleteCallback.GetSessionId())
	}
	return failure(codes.InvalidArgument, "unknown host action")
}

// failure is a refused or failed action.
func failure(code codes.Code, msg string) *hostproxyv1.HostActionResult {
	return &hostproxyv1.HostActionResult{Id: 0, Outcome: &hostproxyv1.HostActionResult_Error{
		Error: &hostproxyv1.HostActionError{Code: uint32(code), Message: msg},
	}}
}

// openURL opens an http or https URL in the host browser after the
// egress check.
func (s *Actions) openURL(req *agentv1.OpenURLRequest) *hostproxyv1.HostActionResult {
	rawURL := req.GetUrl()
	if rawURL == "" {
		return failure(codes.InvalidArgument, "url is required")
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return failure(codes.InvalidArgument, "invalid URL format")
	}
	if parsedURL.Scheme != schemeHTTP && parsedURL.Scheme != schemeHTTPS {
		return failure(codes.InvalidArgument, "only http and https URLs are allowed")
	}
	if denied := s.checkEgress(rawURL); denied != nil {
		return denied
	}

	s.log.Debug().Str("url", rawURL).Msg("opening URL in browser")
	if openErr := s.browserFunc(rawURL); openErr != nil {
		s.log.Error().Err(openErr).Str("url", rawURL).Msg("failed to open URL in browser")
		return failure(codes.Internal, "failed to open browser")
	}
	return &hostproxyv1.HostActionResult{
		Id:      0,
		Outcome: &hostproxyv1.HostActionResult_OpenUrl{OpenUrl: &agentv1.OpenURLResult{}},
	}
}

// checkEgress applies the egress rules to a URL the host browser will
// open. It returns nil when allowed (or the firewall is off), otherwise
// the failure to answer with.
func (s *Actions) checkEgress(rawURL string) *hostproxyv1.HostActionResult {
	if s.rulesFilePath == "" {
		return nil
	}
	err := CheckURLAgainstEgressRules(rawURL, s.rulesFilePath)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errEgressRulesInvalid):
		// Present-but-corrupt rules file (e.g. tampered after the daemon
		// started). Not a policy decision — log loudly with the real cause
		// and fail closed, mirroring Envoy rejecting an invalid config
		// rather than the soft per-request deny below.
		s.log.Error().Err(err).Str("url", rawURL).Msg("egress rules file invalid; denying all URL opens until fixed")
		return failure(codes.Internal, errEgressRulesUnavailable)
	default:
		s.log.Warn().Err(err).Str("url", rawURL).Msg("blocked by egress rules")
		return failure(codes.PermissionDenied, "blocked by egress policy")
	}
}

// gitCredential runs one git credential helper action on the host: get
// (fill), store (approve) or erase (reject).
func (s *Actions) gitCredential(ctx context.Context, req *agentv1.GitCredentialRequest) *hostproxyv1.HostActionResult {
	gitAction, refused := validateGitCredential(req)
	if refused != nil {
		if refused.GetError().GetMessage() == msgInjectionChars {
			s.log.Warn().Str("host", req.GetHost()).
				Msg("rejected git credential request: fields contain injection characters")
		}
		return refused
	}
	s.log.Debug().
		Str("action", gitAction).
		Str("protocol", req.GetProtocol()).
		Str("host", req.GetHost()).
		Str("username", req.GetUsername()).
		Msg("git credential request")

	cmd := exec.CommandContext(ctx, "git", "credential", gitAction)
	cmd.Stdin = strings.NewReader(formatGitCredentialInput(req))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		s.log.Debug().Str("action", gitAction).Str("host", req.GetHost()).Str("stderr", errMsg).
			Msg("git credential command failed")
		return failure(codes.FailedPrecondition, "credential helper failed: "+errMsg)
	}

	out := &agentv1.GitCredentialResult{Protocol: "", Host: "", Username: "", Password: ""}
	if gitAction == gitFill {
		creds := parseGitCredentialOutput(stdout.String(), s.log)
		s.log.Debug().
			Str("host", req.GetHost()).
			Str("username", creds.Username).
			Bool("has_password", creds.Password != "").
			Msg("git credential retrieved")
		out = &agentv1.GitCredentialResult{
			Protocol: creds.Protocol, Host: creds.Host, Username: creds.Username, Password: creds.Password,
		}
	}
	return &hostproxyv1.HostActionResult{
		Id:      0,
		Outcome: &hostproxyv1.HostActionResult_GitCredential{GitCredential: out},
	}
}

// gitFill is the git credential subcommand for get.
const gitFill = "fill"

// msgInjectionChars is the refusal for a field that could inject
// key=value pairs into the git credential protocol.
const msgInjectionChars = "credential fields must not contain newlines or null bytes"

// validateGitCredential maps the action to its git credential subcommand
// (get=fill, store=approve, erase=reject) and checks the fields. A
// non-nil result is the refusal.
func validateGitCredential(req *agentv1.GitCredentialRequest) (string, *hostproxyv1.HostActionResult) {
	var gitAction string
	switch req.GetAction() {
	case agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET:
		gitAction = gitFill
	case agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_STORE:
		gitAction = "approve"
	case agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_ERASE:
		gitAction = "reject"
	case agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_UNSPECIFIED:
	}
	switch {
	case gitAction == "":
		return "", failure(codes.InvalidArgument, "action must be get, store, or erase")
	case req.GetProtocol() == "":
		return "", failure(codes.InvalidArgument, "protocol is required")
	case req.GetHost() == "":
		return "", failure(codes.InvalidArgument, "host is required")
	}
	fields := []string{req.GetProtocol(), req.GetHost(), req.GetPath(), req.GetUsername(), req.GetPassword()}
	if slices.ContainsFunc(fields, containsCredentialInjectionChars) {
		return "", failure(codes.InvalidArgument, msgInjectionChars)
	}
	return gitAction, nil
}

// Callback port bounds: the listener stays off privileged ports.
const (
	minCallbackPort = 1024
	maxPort         = 65535
)

// callbackTarget extracts the localhost callback port and path from the
// auth URL's redirect_uri (or callback) parameter. Only an http redirect
// to localhost or 127.0.0.1 on an unprivileged port is accepted.
func callbackTarget(authURL string) (int32, string, error) {
	parsed, err := url.Parse(authURL)
	if err != nil {
		return 0, "", fmt.Errorf("invalid auth URL: %w", err)
	}
	q := parsed.Query()
	raw := q.Get("redirect_uri")
	if raw == "" {
		raw = q.Get("callback")
	}
	if raw == "" {
		return 0, "", errors.New("auth URL has no redirect_uri")
	}
	redirect, err := url.Parse(raw)
	if err != nil {
		return 0, "", fmt.Errorf("invalid redirect_uri: %w", err)
	}
	host := redirect.Hostname()
	if redirect.Scheme != schemeHTTP || redirect.User != nil || (host != "localhost" && host != consts.Localhost) {
		return 0, "", errors.New("redirect_uri must be http://localhost:<port> or http://127.0.0.1:<port>")
	}
	port, err := strconv.ParseInt(redirect.Port(), 10, 32)
	if err != nil || port < minCallbackPort || port > maxPort {
		return 0, "", fmt.Errorf("redirect_uri port must be between %d and %d", minCallbackPort, maxPort)
	}
	path := redirect.Path
	if path == "" {
		path = "/callback"
	}
	return int32(port), path, nil
}

// registerCallback is the one-shot OAuth flow. It egress-checks the auth
// URL, takes the callback port and path from its localhost redirect, binds
// that port, and opens the auth URL in the host browser. The listener
// closes after the first callback.
func (s *Actions) registerCallback(
	ctx context.Context,
	req *agentv1.RegisterCallbackRequest,
) *hostproxyv1.HostActionResult {
	authURL := req.GetAuthUrl()
	port, path, err := callbackTarget(authURL)
	if err != nil {
		return failure(codes.InvalidArgument, err.Error())
	}
	if denied := s.checkEgress(authURL); denied != nil {
		return denied
	}

	ttl := DefaultCallbackTTL
	if req.GetTtlSeconds() > 0 {
		ttl = min(time.Duration(req.GetTtlSeconds())*time.Second, DefaultCallbackTTL)
	}

	session, err := s.callbackChannel.Register(int(port), path, ttl)
	if err != nil {
		s.log.Error().Err(err).Msg("failed to register callback session")
		return failure(codes.Internal, "failed to create session")
	}
	if listenErr := s.startDynamicListener(ctx, int(port), session.ID); listenErr != nil {
		s.log.Error().Err(listenErr).Int32("port", port).Msg("failed to start dynamic listener")
		s.callbackChannel.Delete(session.ID)
		return failure(codes.Internal, fmt.Sprintf("failed to start listener on port %d", port))
	}
	if openErr := s.browserFunc(authURL); openErr != nil {
		s.log.Error().Err(openErr).Msg("failed to open auth URL in browser")
		s.callbackChannel.Delete(session.ID)
		return failure(codes.Internal, "failed to open browser")
	}

	s.log.Info().
		Str("event", "host_proxy_callback_registered").
		Str("session_id", session.ID).
		Int32("port", port).
		Str("path", path).
		Msg("callback session registered and auth URL opened")

	return &hostproxyv1.HostActionResult{Id: 0, Outcome: &hostproxyv1.HostActionResult_RegisterCallback{
		RegisterCallback: &agentv1.RegisterCallbackResult{
			SessionId:     session.ID,
			Port:          port,
			ExpiresAtUnix: session.ExpiresAt.Unix(),
		},
	}}
}

// getCallbackData reports whether the browser callback for a session
// arrived and, if so, the captured request.
func (s *Actions) getCallbackData(sessionID string) *hostproxyv1.HostActionResult {
	if sessionID == "" {
		return failure(codes.InvalidArgument, "session ID required")
	}
	out := &agentv1.GetCallbackDataResult{Received: false, Callback: nil}
	data, received := s.callbackChannel.GetData(sessionID)
	switch {
	case received:
		out.Received = true
		out.Callback = &agentv1.CallbackData{
			Method:         data.Method,
			Path:           data.Path,
			Query:          data.Query,
			Headers:        data.Headers,
			Body:           data.Body,
			ReceivedAtUnix: data.ReceivedAt.Unix(),
		}
	case s.sessionStore.Get(sessionID) == nil:
		return failure(codes.NotFound, "session not found or expired")
	}
	return &hostproxyv1.HostActionResult{
		Id:      0,
		Outcome: &hostproxyv1.HostActionResult_GetCallbackData{GetCallbackData: out},
	}
}

// deleteCallback ends a callback session and releases its listener.
func (s *Actions) deleteCallback(sessionID string) *hostproxyv1.HostActionResult {
	if sessionID == "" {
		return failure(codes.InvalidArgument, "session ID required")
	}
	s.callbackChannel.Delete(sessionID)
	return &hostproxyv1.HostActionResult{Id: 0, Outcome: &hostproxyv1.HostActionResult_DeleteCallback{
		DeleteCallback: &agentv1.DeleteCallbackResult{},
	}}
}

// Callback listener timeouts.
const (
	callbackIOTimeout       = 10 * time.Second
	callbackShutdownTimeout = 500 * time.Millisecond
)

// startDynamicListener binds the callback port on IPv4 and IPv6 loopback
// and serves the one-shot OAuth callback on it.
func (s *Actions) startDynamicListener(ctx context.Context, port int, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.dynamicListeners[port]; exists {
		return fmt.Errorf("listener already exists on port %d", port)
	}
	listeners := s.listenLoopback(ctx, port)
	if len(listeners) == 0 {
		return fmt.Errorf("failed to listen on any loopback address for callback port %d", port)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handleDynamicCallback(port, w, r)
	})
	dl := &dynamicListener{
		port:      port,
		sessionID: sessionID,
		listeners: listeners,
		servers:   make([]*http.Server, 0, len(listeners)),
	}
	for _, l := range listeners {
		srv := &http.Server{Handler: handler, ReadTimeout: callbackIOTimeout, WriteTimeout: callbackIOTimeout}
		dl.servers = append(dl.servers, srv)
		go func() {
			if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.log.Error().Err(err).Int("port", port).Str("addr", l.Addr().String()).Msg("dynamic listener error")
			}
		}()
	}
	s.dynamicListeners[port] = dl
	s.portToSession[port] = sessionID

	s.log.Debug().Int("port", port).Str("session_id", sessionID).Int("listeners", len(listeners)).
		Msg("dynamic listener started")
	return nil
}

// listenLoopback binds port on IPv4 and IPv6 loopback. A family that is
// not available is skipped.
func (s *Actions) listenLoopback(ctx context.Context, port int) []net.Listener {
	var lc net.ListenConfig
	var listeners []net.Listener
	for _, host := range []string{consts.Localhost, "::1"} {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		l, err := lc.Listen(ctx, "tcp", addr)
		if err != nil {
			s.log.Debug().Str("addr", addr).Err(err).
				Msg("failed to listen for dynamic callback (may be expected if protocol not available)")
			continue
		}
		listeners = append(listeners, l)
	}
	return listeners
}

// closeDynamicListener closes a dynamic listener on the specified port.
func (s *Actions) closeDynamicListener(ctx context.Context, port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeDynamicListenerLocked(ctx, port)
}

// closeDynamicListenerLocked closes a dynamic listener (must hold s.mu).
func (s *Actions) closeDynamicListenerLocked(ctx context.Context, port int) {
	dl, exists := s.dynamicListeners[port]
	if !exists {
		return
	}

	s.log.Debug().Int("port", port).Msg("closing dynamic listener")

	// Shutdown the servers gracefully with a short timeout
	ctx, cancel := context.WithTimeout(ctx, callbackShutdownTimeout)
	defer cancel()

	for i, server := range dl.servers {
		if err := server.Shutdown(ctx); err != nil {
			// Timeout during cleanup is expected - just force close
			s.log.Debug().Int("port", port).Int("listener_index", i).Msg("force closing dynamic listener")
			if closeErr := dl.listeners[i].Close(); closeErr != nil {
				s.log.Debug().
					Err(closeErr).
					Int("port", port).
					Int("listener_index", i).
					Msg("dynamic listener close returned error")
			}
		}
	}

	delete(s.dynamicListeners, port)
	delete(s.portToSession, port)
}

// handleDynamicCallback handles incoming requests on a dynamic listener port.
func (s *Actions) handleDynamicCallback(port int, w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	sessionID, exists := s.portToSession[port]
	s.mu.RUnlock()

	if !exists {
		s.log.Error().Int("port", port).Msg("no session found for dynamic listener port")
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	s.log.Debug().
		Int("port", port).
		Str("session_id", sessionID).
		Str("path", r.URL.Path).
		Str("query", r.URL.RawQuery).
		Msg("received callback on dynamic listener")

	err := s.callbackChannel.Capture(sessionID, r)
	if err != nil {
		// ErrCallbackAlreadyReceived is expected (browsers often send duplicate requests)
		// Handle it silently by showing success page
		if errors.Is(err, ErrCallbackAlreadyReceived) {
			s.writeCallbackSuccessPage(w)
			return
		}

		// Log actual errors
		s.log.Error().Err(err).Str("session_id", sessionID).Msg("failed to capture callback")
		s.writeCallbackErrorPage(w, "An error occurred. Please try again.")
		return
	}

	s.writeCallbackSuccessPage(w)
	// One-shot: the first callback is captured, so release the host port.
	// The session keeps the data for the agent to poll. Close off the
	// handler goroutine: Shutdown waits for in-flight handlers.
	go s.closeDynamicListener(context.WithoutCancel(r.Context()), port)
}

// writeCallbackSuccessPage writes an HTML success page for OAuth callbacks.
func (s *Actions) writeCallbackSuccessPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(callbackPage("Authentication Complete", callbackSuccessBody))); err != nil {
		s.log.Debug().Err(err).Msg("failed to write callback success page")
	}
}

// writeCallbackErrorPage writes an HTML error page for OAuth callbacks.
func (s *Actions) writeCallbackErrorPage(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write([]byte(callbackPage("Authentication Error", callbackErrorBody(message)))); err != nil {
		s.log.Debug().Err(err).Msg("failed to write callback error page")
	}
}
