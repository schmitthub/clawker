package clawkerd

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

const (
	// otlpContentType is the only request body encoding the relay
	// accepts; the body crosses to the CP as the serialized
	// Export<Signal>ServiceRequest it already is.
	otlpContentType = "application/x-protobuf"
	// relayExportTimeout bounds one forward. It exceeds the CP gateway's
	// own forward timeout so a slow collector surfaces as the CP's
	// verdict, not as a relay-side deadline.
	relayExportTimeout = 15 * time.Second
	// relayRetryAfterSeconds is the Retry-After the SDK sees on a 503 so
	// its bounded retry backs off while the CP is unreachable.
	relayRetryAfterSeconds = 2
	// relayLogInterval rate-limits the rejection log: a CP outage must
	// not turn every SDK batch into a warn line.
	relayLogInterval = time.Minute
	// relayReadHeaderTimeout bounds a loopback client that opens a
	// connection and never sends headers.
	relayReadHeaderTimeout = 5 * time.Second
	// relayReadTimeout bounds the whole request read, so a loopback
	// client that sends headers and then stalls the body cannot hold a
	// PID1 goroutine and its body buffer. With IdleTimeout unset it also
	// bounds idle keep-alive connections.
	relayReadTimeout = 10 * time.Second
)

// exportClient is the slice of AgentServiceClient the relay needs.
type exportClient interface {
	Export(ctx context.Context, in *agentv1.ExportRequest, opts ...grpc.CallOption) (*agentv1.ExportResult, error)
}

// OTLPRelay is the loopback OTLP/HTTP receiver the agent SDK exports
// to. Each batch is carried unchanged to AgentService.Export over
// clawkerd's mTLS leaf; the CP verifies the peer, stamps identity, and
// forwards to the collector. The relay holds no identity logic: it maps
// the CP verdict onto HTTP so the SDK retries only what can succeed
// later. A nil *OTLPRelay is the degraded daemon (no CP address or
// unusable bootstrap material); its methods are nil-safe.
type OTLPRelay struct {
	srv    *http.Server
	lis    net.Listener
	conn   *grpc.ClientConn
	client exportClient
	log    *logger.Logger

	mu      sync.Mutex
	nextLog time.Time
}

// StartTelemetryRelay dials the CP agent listener with the bootstrap
// leaf (lazily; gRPC reconnects on its own) and serves the relay on
// listenAddr. Every failure degrades to nil with
// event=otlp_relay_unavailable: telemetry is optional and must never
// delay the listener, the Session, or the spawn.
func StartTelemetryRelay(
	ctx context.Context, boot *bootstrap, agentAddr, listenAddr string, log *logger.Logger,
) *OTLPRelay {
	if agentAddr == "" {
		log.Warn().Str("event", "otlp_relay_unavailable").
			Msg("CLAWKER_CP_AGENT_ADDR unset; agent telemetry is dropped")
		return nil
	}
	conn, err := dialAgentService(boot, agentAddr)
	if err != nil {
		log.Error().Err(err).Str("event", "otlp_relay_unavailable").Msg("dial CP agent listener")
		return nil
	}
	relay, err := newOTLPRelay(ctx, listenAddr, agentv1.NewAgentServiceClient(conn), log)
	if err != nil {
		if cerr := conn.Close(); cerr != nil {
			log.Error().Err(cerr).Str("event", "otlp_relay_conn_close_failed").Msg("close")
		}
		log.Error().Err(err).Str("event", "otlp_relay_unavailable").Msg("start OTLP relay")
		return nil
	}
	relay.conn = conn
	log.Info().Str("event", "otlp_relay_ready").
		Str("listen", relay.Addr().String()).
		Str("cp", agentAddr).
		Msg("agent telemetry relay listening")
	return relay
}

// dialAgentService opens the process-lifetime Export connection. Same
// TLS material as Register, no bearer credentials: Export is public
// scope behind mTLS plus the CP's identity gate.
func dialAgentService(boot *bootstrap, addr string) (*grpc.ClientConn, error) {
	dialTLS, err := buildDialTLSConfig(boot.CertPEM, boot.KeyPEM, boot.CACertPEM)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(dialTLS)))
	if err != nil {
		return nil, fmt.Errorf("grpc client %s: %w", addr, err)
	}
	return conn, nil
}

// newOTLPRelay binds listenAddr and serves in a recovered goroutine.
// ctx bounds the bind only; the server outlives it until Shutdown.
func newOTLPRelay(ctx context.Context, listenAddr string, client exportClient, log *logger.Logger) (*OTLPRelay, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	r := &OTLPRelay{
		srv:     nil,
		lis:     lis,
		conn:    nil,
		client:  client,
		log:     log,
		mu:      sync.Mutex{},
		nextLog: time.Time{},
	}
	srv := new(http.Server)
	srv.Handler = r
	srv.ReadHeaderTimeout = relayReadHeaderTimeout
	srv.ReadTimeout = relayReadTimeout
	r.srv = srv
	go func() {
		defer recoverGoroutine(log, "otlp_relay_serve", nil)
		if serr := srv.Serve(lis); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			log.Error().Err(serr).
				Str("event", "otlp_relay_serve_failed").
				Msg("relay listener died; agent telemetry is dropped")
		}
	}()
	return r, nil
}

// Addr is the bound listener address.
func (r *OTLPRelay) Addr() net.Addr {
	return r.lis.Addr()
}

// Shutdown stops the listener, waits up to grace for in-flight
// forwards, and closes the CP connection. Nil-safe. Callers pass a
// context that outlives the daemon's signal context: at exit it is
// already cancelled and would skip the grace.
func (r *OTLPRelay) Shutdown(ctx context.Context, grace time.Duration) {
	if r == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	if err := r.srv.Shutdown(ctx); err != nil {
		r.log.Warn().Err(err).Str("event", "otlp_relay_shutdown_failed").Msg("relay shutdown")
	}
	if r.conn != nil {
		if err := r.conn.Close(); err != nil {
			r.log.Error().Err(err).Str("event", "otlp_relay_conn_close_failed").Msg("close")
		}
	}
}

// ServeHTTP accepts POST /v1/{logs,metrics,traces} with
// application/x-protobuf (optionally gzip) and forwards the body.
func (r *OTLPRelay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	signal := signalForPath(req.URL.Path)
	if signal == agentv1.Signal_SIGNAL_UNSPECIFIED {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if mt, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || mt != otlpContentType {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}
	body, code := readRelayBody(w, req)
	if code != 0 {
		http.Error(w, http.StatusText(code), code)
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), relayExportTimeout)
	defer cancel()
	if _, err := r.client.Export(ctx, &agentv1.ExportRequest{Signal: signal, Body: body}); err != nil {
		r.reject(w, signal, err)
		return
	}
	// An empty Export<Signal>ServiceResponse serializes to zero bytes.
	w.Header().Set("Content-Type", otlpContentType)
	w.WriteHeader(http.StatusOK)
}

func signalForPath(path string) agentv1.Signal {
	switch path {
	case "/v1/logs":
		return agentv1.Signal_SIGNAL_LOGS
	case "/v1/metrics":
		return agentv1.Signal_SIGNAL_METRICS
	case "/v1/traces":
		return agentv1.Signal_SIGNAL_TRACES
	default:
		return agentv1.Signal_SIGNAL_UNSPECIFIED
	}
}

// readRelayBody reads at most ExportMaxBodyBytes, decoding gzip when
// declared. The cap applies to the decoded size so a small compressed
// body cannot expand past what the CP listener accepts. A non-zero
// return code is the HTTP status to answer with.
func readRelayBody(w http.ResponseWriter, req *http.Request) ([]byte, int) {
	var rd io.Reader = http.MaxBytesReader(w, req.Body, agentv1.ExportMaxBodyBytes)
	switch req.Header.Get("Content-Encoding") {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(rd)
		if err != nil {
			return nil, http.StatusBadRequest
		}
		rd = io.LimitReader(gz, agentv1.ExportMaxBodyBytes+1)
	default:
		return nil, http.StatusUnsupportedMediaType
	}
	body, err := io.ReadAll(rd)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge), len(body) > agentv1.ExportMaxBodyBytes:
		return nil, http.StatusRequestEntityTooLarge
	case err != nil:
		return nil, http.StatusBadRequest
	}
	return body, 0
}

// reject maps the CP verdict onto HTTP. Unavailable and a missed
// deadline are retryable (503 + Retry-After); an identity rejection is
// 403; a malformed or oversize batch is the SDK's fault (400/413);
// anything else is a CP-side fault the SDK must not retry (500; the
// Node OTLP exporter retries 429, 502, 503 and 504).
func (r *OTLPRelay) reject(w http.ResponseWriter, signal agentv1.Signal, err error) {
	code := status.Code(err)
	httpCode := relayHTTPStatus(code)
	if httpCode == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", strconv.Itoa(relayRetryAfterSeconds))
	}
	if r.allowLog(time.Now()) {
		event := "otlp_relay_rejected"
		if httpCode == http.StatusServiceUnavailable {
			event = "otlp_relay_cp_unavailable"
		}
		r.log.Warn().Err(err).
			Str("event", event).
			Str("signal", signal.String()).
			Str("grpc_code", code.String()).
			Int("http_status", httpCode).
			Msg("agent telemetry batch not forwarded")
	}
	http.Error(w, code.String(), httpCode)
}

func relayHTTPStatus(code codes.Code) int {
	if code == codes.Unavailable || code == codes.DeadlineExceeded || code == codes.Canceled {
		return http.StatusServiceUnavailable
	}
	if code == codes.PermissionDenied || code == codes.Unauthenticated {
		return http.StatusForbidden
	}
	if code == codes.InvalidArgument {
		return http.StatusBadRequest
	}
	if code == codes.ResourceExhausted {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

// allowLog admits one rejection log line per relayLogInterval.
func (r *OTLPRelay) allowLog(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Before(r.nextLog) {
		return false
	}
	r.nextLog = now.Add(relayLogInterval)
	return true
}
