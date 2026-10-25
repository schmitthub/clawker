package clawkerd

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// fakeExport stands in for the CP's AgentService on the relay's
// outbound side and records every Export call.
type fakeExport struct {
	mu    sync.Mutex
	calls []*agentv1.ExportRequest
	err   error
	// sawDeadline records whether the relay bounded the forward.
	sawDeadline bool
}

func (f *fakeExport) Export(
	ctx context.Context, in *agentv1.ExportRequest, _ ...grpc.CallOption,
) (*agentv1.ExportResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, f.sawDeadline = ctx.Deadline()
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	return new(agentv1.ExportResult), nil
}

func (f *fakeExport) requests() []*agentv1.ExportRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*agentv1.ExportRequest(nil), f.calls...)
}

// newTestRelay starts a relay on an ephemeral loopback port and returns
// its base URL.
func newTestRelay(t *testing.T, fake *fakeExport) string {
	t.Helper()
	relay, err := newOTLPRelay(t.Context(), consts.Localhost+":0", fake, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { relay.Shutdown(context.Background(), time.Second) })
	return "http://" + relay.Addr().String()
}

// send issues one request. hdr replaces the default protobuf
// Content-Type when it carries one.
func send(t *testing.T, method, url string, body []byte, hdr http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", otlpContentType)
	maps.Copy(req.Header, hdr)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() {
		if cerr := res.Body.Close(); cerr != nil {
			t.Logf("close response body: %v", cerr)
		}
	})
	return res
}

func post(t *testing.T, base, path string, body []byte) *http.Response {
	t.Helper()
	return send(t, http.MethodPost, base+path, body, nil)
}

func gzipHeader() http.Header {
	return http.Header{"Content-Encoding": {"gzip"}}
}

func gzipBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(plain)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// TestOTLPRelay_ForwardsEachSignal: the OTLP/HTTP path selects the
// signal enum, the body crosses untouched, and the SDK sees an empty
// Export<Signal>ServiceResponse on 200.
func TestOTLPRelay_ForwardsEachSignal(t *testing.T) {
	cases := []struct {
		path string
		want agentv1.Signal
	}{
		{"/v1/logs", agentv1.Signal_SIGNAL_LOGS},
		{"/v1/metrics", agentv1.Signal_SIGNAL_METRICS},
		{"/v1/traces", agentv1.Signal_SIGNAL_TRACES},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			fake := new(fakeExport)
			base := newTestRelay(t, fake)
			body := []byte("otlp-" + tc.path)

			res := post(t, base, tc.path, body)
			assert.Equal(t, http.StatusOK, res.StatusCode)
			assert.Equal(t, otlpContentType, res.Header.Get("Content-Type"))
			out, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			assert.Empty(t, out, "empty response message serializes to zero bytes")

			reqs := fake.requests()
			require.Len(t, reqs, 1)
			assert.Equal(t, tc.want, reqs[0].GetSignal())
			assert.Equal(t, body, reqs[0].GetBody())
			assert.True(t, fake.sawDeadline, "forward must carry a deadline")
		})
	}
}

// TestOTLPRelay_RejectsBeforeForward: client faults never reach the CP.
func TestOTLPRelay_RejectsBeforeForward(t *testing.T) {
	oversize := make([]byte, agentv1.ExportMaxBodyBytes+1)
	cases := []struct {
		name   string
		method string
		path   string
		body   []byte
		hdr    http.Header
		want   int
	}{
		{"unknown path", http.MethodPost, "/v1/profiles", []byte("x"), nil, http.StatusNotFound},
		{"GET", http.MethodGet, "/v1/logs", nil, nil, http.StatusMethodNotAllowed},
		{
			"json content type", http.MethodPost, "/v1/logs", []byte("{}"),
			http.Header{"Content-Type": {"application/json"}},
			http.StatusUnsupportedMediaType,
		},
		{
			"empty content type", http.MethodPost, "/v1/logs", []byte("x"),
			http.Header{"Content-Type": {""}},
			http.StatusUnsupportedMediaType,
		},
		{
			"unknown encoding", http.MethodPost, "/v1/logs", []byte("x"),
			http.Header{"Content-Encoding": {"br"}},
			http.StatusUnsupportedMediaType,
		},
		{"body over cap", http.MethodPost, "/v1/logs", oversize, nil, http.StatusRequestEntityTooLarge},
		{"corrupt gzip", http.MethodPost, "/v1/logs", []byte("not gzip"), gzipHeader(), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := new(fakeExport)
			base := newTestRelay(t, fake)
			res := send(t, tc.method, base+tc.path, tc.body, tc.hdr)
			assert.Equal(t, tc.want, res.StatusCode)
			assert.Empty(t, fake.requests())
		})
	}
}

// TestOTLPRelay_BodyAtCapAccepted pins the boundary the handler and
// the CP listener share.
func TestOTLPRelay_BodyAtCapAccepted(t *testing.T) {
	fake := new(fakeExport)
	base := newTestRelay(t, fake)
	body := make([]byte, agentv1.ExportMaxBodyBytes)
	res := post(t, base, "/v1/metrics", body)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	require.Len(t, fake.requests(), 1)
	assert.Len(t, fake.requests()[0].GetBody(), agentv1.ExportMaxBodyBytes)
}

// TestOTLPRelay_Encodings: identity passes through, a gzip body is
// decoded before forwarding, and the decoded size is capped so a small
// compressed body cannot expand past the wire cap.
func TestOTLPRelay_Encodings(t *testing.T) {
	t.Run("identity forwarded", func(t *testing.T) {
		fake := new(fakeExport)
		base := newTestRelay(t, fake)
		plain := []byte("identity-bytes")
		res := send(t, http.MethodPost, base+"/v1/logs", plain, http.Header{"Content-Encoding": {"identity"}})
		assert.Equal(t, http.StatusOK, res.StatusCode)
		require.Len(t, fake.requests(), 1)
		assert.Equal(t, plain, fake.requests()[0].GetBody())
	})
	t.Run("gzip decoded body forwarded", func(t *testing.T) {
		fake := new(fakeExport)
		base := newTestRelay(t, fake)
		plain := []byte("plain-otlp-bytes")
		res := send(t, http.MethodPost, base+"/v1/traces", gzipBytes(t, plain), gzipHeader())
		assert.Equal(t, http.StatusOK, res.StatusCode)
		require.Len(t, fake.requests(), 1)
		assert.Equal(t, plain, fake.requests()[0].GetBody())
	})
	t.Run("decoded size over cap", func(t *testing.T) {
		fake := new(fakeExport)
		base := newTestRelay(t, fake)
		bomb := gzipBytes(t, make([]byte, agentv1.ExportMaxBodyBytes+1))
		require.Less(t, len(bomb), agentv1.ExportMaxBodyBytes, "compressed input must fit the wire cap")
		res := send(t, http.MethodPost, base+"/v1/logs", bomb, gzipHeader())
		assert.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
		assert.Empty(t, fake.requests())
	})
}

// TestOTLPRelay_StatusMapping: the CP verdict maps onto HTTP so the SDK
// retries only what can succeed later.
func TestOTLPRelay_StatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		want       int
		retryAfter bool
	}{
		{"unavailable", status.Error(codes.Unavailable, "cp down"), http.StatusServiceUnavailable, true},
		{"deadline", status.Error(codes.DeadlineExceeded, "slow"), http.StatusServiceUnavailable, true},
		{"permission denied", status.Error(codes.PermissionDenied, "unregistered"), http.StatusForbidden, false},
		{"invalid argument", status.Error(codes.InvalidArgument, "malformed"), http.StatusBadRequest, false},
		{
			"resource exhausted", status.Error(codes.ResourceExhausted, "too big"),
			http.StatusRequestEntityTooLarge, false,
		},
		{"internal", status.Error(codes.Internal, "bug"), http.StatusInternalServerError, false},
		{"canceled", status.Error(codes.Canceled, "caller left"), http.StatusServiceUnavailable, true},
		{"unauthenticated", status.Error(codes.Unauthenticated, "no cert"), http.StatusForbidden, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := new(fakeExport)
			fake.err = tc.err
			base := newTestRelay(t, fake)
			res := post(t, base, "/v1/logs", []byte("x"))
			assert.Equal(t, tc.want, res.StatusCode)
			if tc.retryAfter {
				assert.NotEmpty(t, res.Header.Get("Retry-After"))
			} else {
				assert.Empty(t, res.Header.Get("Retry-After"))
			}
		})
	}
}

// TestOTLPRelay_RejectionLog: a CP outage must not turn every SDK batch
// into a warn line, and the event name tells an operator whether the CP
// was away or rejected the batch.
func TestOTLPRelay_RejectionLog(t *testing.T) {
	t.Run("cp unavailable logs once per interval", func(t *testing.T) {
		var buf bytes.Buffer
		fake := new(fakeExport)
		fake.err = status.Error(codes.Unavailable, "cp down")
		relay, err := newOTLPRelay(t.Context(), consts.Localhost+":0", fake, logger.NewWriter(&buf))
		require.NoError(t, err)
		t.Cleanup(func() { relay.Shutdown(context.Background(), time.Second) })
		base := "http://" + relay.Addr().String()
		post(t, base, "/v1/logs", []byte("x"))
		post(t, base, "/v1/logs", []byte("x"))
		assert.Len(t, fake.requests(), 2)
		assert.Equal(t, 1, strings.Count(buf.String(), `"event":"otlp_relay_cp_unavailable"`), buf.String())
	})
	t.Run("cp verdict logs rejected", func(t *testing.T) {
		var buf bytes.Buffer
		fake := new(fakeExport)
		fake.err = status.Error(codes.PermissionDenied, "unregistered")
		relay, err := newOTLPRelay(t.Context(), consts.Localhost+":0", fake, logger.NewWriter(&buf))
		require.NoError(t, err)
		t.Cleanup(func() { relay.Shutdown(context.Background(), time.Second) })
		post(t, "http://"+relay.Addr().String(), "/v1/logs", []byte("x"))
		assert.Contains(t, buf.String(), `"event":"otlp_relay_rejected"`)
		assert.NotContains(t, buf.String(), "otlp_relay_cp_unavailable")
	})
	t.Run("interval boundary", func(t *testing.T) {
		var r OTLPRelay
		now := time.Now()
		assert.True(t, r.allowLog(now))
		assert.False(t, r.allowLog(now.Add(relayLogInterval/2)))
		assert.True(t, r.allowLog(now.Add(relayLogInterval)))
	})
}

// TestStartTelemetryRelay covers the orchestrator: degrade to nil when
// the CP address, the bootstrap material, or the bind is unusable, and
// with a CP that refuses the connection the SDK gets a retryable 503
// through the real gRPC client.
func TestStartTelemetryRelay(t *testing.T) {
	caPEM, certPEM, keyPEM := validPEMs(t)
	boot := &bootstrap{CertPEM: certPEM, KeyPEM: keyPEM, CACertPEM: caPEM, Assertion: "jwt"}
	ephemeral := consts.Localhost + ":0"
	// Port 1 on loopback has no listener: the gRPC client fails fast
	// with Unavailable.
	deadCP := consts.Localhost + ":1"

	t.Run("no CP address degrades to nil", func(t *testing.T) {
		relay := StartTelemetryRelay(t.Context(), boot, "", ephemeral, logger.Nop())
		assert.Nil(t, relay)
		relay.Shutdown(t.Context(), time.Second) // nil-safe
	})
	t.Run("bad bootstrap material degrades to nil", func(t *testing.T) {
		bad := &bootstrap{CertPEM: []byte("junk"), KeyPEM: keyPEM, CACertPEM: caPEM, Assertion: "jwt"}
		assert.Nil(t, StartTelemetryRelay(t.Context(), bad, deadCP, ephemeral, logger.Nop()))
	})
	t.Run("listen failure degrades to nil", func(t *testing.T) {
		var lc net.ListenConfig
		taken, err := lc.Listen(t.Context(), "tcp", ephemeral)
		require.NoError(t, err)
		t.Cleanup(func() {
			if cerr := taken.Close(); cerr != nil {
				t.Logf("close listener: %v", cerr)
			}
		})
		assert.Nil(t, StartTelemetryRelay(t.Context(), boot, deadCP, taken.Addr().String(), logger.Nop()))
	})
	t.Run("CP refusing connections yields 503", func(t *testing.T) {
		relay := StartTelemetryRelay(t.Context(), boot, deadCP, ephemeral, logger.Nop())
		require.NotNil(t, relay)
		t.Cleanup(func() { relay.Shutdown(context.Background(), time.Second) })
		host, _, err := net.SplitHostPort(relay.Addr().String())
		require.NoError(t, err)
		assert.True(t, net.ParseIP(host).IsLoopback())

		res := post(t, "http://"+relay.Addr().String(), "/v1/logs", []byte("x"))
		assert.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
		assert.NotEmpty(t, res.Header.Get("Retry-After"))
	})
}
