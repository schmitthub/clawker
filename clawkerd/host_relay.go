package clawkerd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

const (
	// hostRelayTimeout bounds one host action. A git credential helper
	// can prompt the OS keychain, so it is longer than the telemetry
	// forward; it exceeds the CP's own host-proxy timeout so a slow host
	// surfaces as the CP's verdict.
	hostRelayTimeout = 90 * time.Second
	// hostRelayMaxBody caps a host-action request body.
	hostRelayMaxBody = 1 << 20
)

// HostRelay is the loopback HTTP endpoint the container scripts
// (host-open, git-credential-clawker, callback-forwarder) call as
// CLAWKER_HOST_PROXY. Each request maps to one AgentService host action
// over clawkerd's mTLS leaf; the CP authorizes it and calls the host
// proxy. The relay holds no policy. A nil *HostRelay is the degraded
// daemon; its methods are nil-safe.
type HostRelay struct {
	srv  *http.Server
	lis  net.Listener
	conn *grpc.ClientConn
	log  *logger.Logger
}

// StartHostRelay dials the CP agent listener with the bootstrap leaf and
// serves the relay on listenAddr. Every failure degrades to nil with
// event=host_relay_unavailable: host actions are optional and must never
// delay the listener, the Session, or the spawn.
func StartHostRelay(
	ctx context.Context, boot *bootstrap, agentAddr, listenAddr string, log *logger.Logger,
) *HostRelay {
	if agentAddr == "" {
		log.Warn().Str("event", "host_relay_unavailable").
			Msg("CLAWKER_CP_AGENT_ADDR unset; host actions are unavailable")
		return nil
	}
	conn, err := dialAgentService(boot, agentAddr)
	if err != nil {
		log.Error().Err(err).Str("event", "host_relay_unavailable").Msg("dial CP agent listener")
		return nil
	}
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", listenAddr)
	if err != nil {
		if cerr := conn.Close(); cerr != nil {
			log.Error().Err(cerr).Str("event", "host_relay_conn_close_failed").Msg("close")
		}
		log.Error().Err(err).Str("event", "host_relay_unavailable").Msg("listen " + listenAddr)
		return nil
	}
	srv := new(http.Server)
	srv.Handler = newHostRelayHandler(agentv1.NewAgentServiceClient(conn), log)
	srv.ReadHeaderTimeout = relayReadHeaderTimeout
	srv.ReadTimeout = relayReadTimeout
	r := &HostRelay{srv: srv, lis: lis, conn: conn, log: log}
	go func() {
		defer recoverGoroutine(log, "host_relay_serve", nil)
		if serr := srv.Serve(lis); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			log.Error().Err(serr).
				Str("event", "host_relay_serve_failed").
				Msg("host relay listener died; host actions are unavailable")
		}
	}()
	log.Info().Str("event", "host_relay_ready").
		Str("listen", lis.Addr().String()).
		Str("cp", agentAddr).
		Msg("host action relay listening")
	return r
}

// Shutdown stops the listener, waits up to grace for in-flight actions,
// and closes the CP connection. Nil-safe.
func (r *HostRelay) Shutdown(ctx context.Context, grace time.Duration) {
	if r == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	if err := r.srv.Shutdown(ctx); err != nil {
		r.log.Warn().Err(err).Str("event", "host_relay_shutdown_failed").Msg("relay shutdown")
	}
	if err := r.conn.Close(); err != nil {
		r.log.Error().Err(err).Str("event", "host_relay_conn_close_failed").Msg("close")
	}
}

// hostRelayHandler maps the scripts' HTTP paths to the AgentService host
// actions.
type hostRelayHandler struct {
	client agentv1.AgentServiceClient
	log    *logger.Logger
}

func newHostRelayHandler(client agentv1.AgentServiceClient, log *logger.Logger) http.Handler {
	h := &hostRelayHandler{client: client, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /open/url", h.openURL)
	mux.HandleFunc("POST /git/credential", h.gitCredential)
	mux.HandleFunc("POST /callback/register", h.registerCallback)
	mux.HandleFunc("GET /callback/{session}/data", h.callbackData)
	mux.HandleFunc("DELETE /callback/{session}", h.deleteCallback)
	return mux
}

type relayResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (h *hostRelayHandler) openURL(w http.ResponseWriter, req *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !h.decodeBody(w, req, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), hostRelayTimeout)
	defer cancel()
	if _, err := h.client.OpenURL(ctx, &agentv1.OpenURLRequest{Url: in.URL}); err != nil {
		h.reject(w, "open_url", err)
		return
	}
	h.writeJSON(w, http.StatusOK, relayResult{Success: true, Error: ""})
}

// relayGitActions maps the git credential helper verb to the proto action.
func relayGitAction(verb string) (agentv1.GitCredentialAction, bool) {
	switch verb {
	case "get":
		return agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET, true
	case "store":
		return agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_STORE, true
	case "erase":
		return agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_ERASE, true
	}
	return agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_UNSPECIFIED, false
}

func (h *hostRelayHandler) gitCredential(w http.ResponseWriter, req *http.Request) {
	var in struct {
		Action   string `json:"action"`
		Protocol string `json:"protocol"`
		Host     string `json:"host"`
		Path     string `json:"path"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !h.decodeBody(w, req, &in) {
		return
	}
	action, ok := relayGitAction(in.Action)
	if !ok {
		h.writeJSON(w, http.StatusBadRequest,
			relayResult{Success: false, Error: "action must be get, store, or erase"})
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), hostRelayTimeout)
	defer cancel()
	res, err := h.client.GitCredential(ctx, &agentv1.GitCredentialRequest{
		Action:   action,
		Protocol: in.Protocol,
		Host:     in.Host,
		Path:     in.Path,
		Username: in.Username,
		Password: in.Password,
	})
	if status.Code(err) == codes.FailedPrecondition {
		// The host helper ran and failed: a failed lookup for git.
		h.writeJSON(w, http.StatusOK, relayResult{Success: false, Error: status.Convert(err).Message()})
		return
	}
	if err != nil {
		h.reject(w, "git_credential", err)
		return
	}
	h.writeJSON(w, http.StatusOK, struct {
		Success  bool   `json:"success"`
		Protocol string `json:"protocol,omitempty"`
		Host     string `json:"host,omitempty"`
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	}{true, res.GetProtocol(), res.GetHost(), res.GetUsername(), res.GetPassword()})
}

func (h *hostRelayHandler) registerCallback(w http.ResponseWriter, req *http.Request) {
	var in struct {
		AuthURL    string `json:"authUrl"`
		TTLSeconds int32  `json:"ttlSeconds"`
	}
	if !h.decodeBody(w, req, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), hostRelayTimeout)
	defer cancel()
	res, err := h.client.RegisterCallback(ctx, &agentv1.RegisterCallbackRequest{
		AuthUrl:    in.AuthURL,
		TtlSeconds: in.TTLSeconds,
	})
	if err != nil {
		h.reject(w, "register_callback", err)
		return
	}
	h.writeJSON(w, http.StatusOK, struct {
		Success   bool   `json:"success"`
		SessionID string `json:"sessionId"`
		Port      int32  `json:"port"`
		ExpiresAt string `json:"expiresAt"`
	}{true, res.GetSessionId(), res.GetPort(), time.Unix(res.GetExpiresAtUnix(), 0).UTC().Format(time.RFC3339)})
}

type relayCallbackData struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	ReceivedAt string            `json:"receivedAt"`
}

func (h *hostRelayHandler) callbackData(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), hostRelayTimeout)
	defer cancel()
	res, err := h.client.GetCallbackData(ctx, &agentv1.GetCallbackDataRequest{SessionId: req.PathValue("session")})
	if err != nil {
		h.reject(w, "callback_data", err)
		return
	}
	out := struct {
		Received bool               `json:"received"`
		Callback *relayCallbackData `json:"callback,omitempty"`
	}{res.GetReceived(), nil}
	if cb := res.GetCallback(); res.GetReceived() && cb != nil {
		out.Callback = &relayCallbackData{
			Method:     cb.GetMethod(),
			Path:       cb.GetPath(),
			Query:      cb.GetQuery(),
			Headers:    cb.GetHeaders(),
			Body:       cb.GetBody(),
			ReceivedAt: time.Unix(cb.GetReceivedAtUnix(), 0).UTC().Format(time.RFC3339),
		}
	}
	h.writeJSON(w, http.StatusOK, out)
}

func (h *hostRelayHandler) deleteCallback(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), hostRelayTimeout)
	defer cancel()
	in := &agentv1.DeleteCallbackRequest{SessionId: req.PathValue("session")}
	if _, err := h.client.DeleteCallback(ctx, in); err != nil {
		h.reject(w, "delete_callback", err)
		return
	}
	h.writeJSON(w, http.StatusOK, relayResult{Success: true, Error: ""})
}

// reject maps the CP verdict onto HTTP for the scripts.
func (h *hostRelayHandler) reject(w http.ResponseWriter, action string, err error) {
	st := status.Convert(err)
	code := relayHTTPStatus(st.Code())
	if st.Code() == codes.NotFound {
		code = http.StatusNotFound
	}
	h.log.Warn().Err(err).
		Str("event", "host_relay_rejected").
		Str("action", action).
		Str("grpc_code", st.Code().String()).
		Int("http_status", code).
		Msg("host action not completed")
	h.writeJSON(w, code, relayResult{Success: false, Error: st.Message()})
}

// decodeBody decodes a JSON request body of at most hostRelayMaxBody
// into out, answering 400 when it does not decode.
func (h *hostRelayHandler) decodeBody(w http.ResponseWriter, req *http.Request, out any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, hostRelayMaxBody)).Decode(out); err != nil {
		h.writeJSON(w, http.StatusBadRequest, relayResult{Success: false, Error: fmt.Sprintf("invalid JSON: %v", err)})
		return false
	}
	return true
}

func (h *hostRelayHandler) writeJSON(w http.ResponseWriter, code int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, writeErr := w.Write(raw); writeErr != nil {
		h.log.Debug().Err(writeErr).Str("event", "host_relay_write_failed").Msg("host relay response not written")
	}
}
