package clawkerd

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/logger"
)

// fakeExportClient answers every ExportTelemetry with err, or blocks on
// release when it is non-nil.
type fakeExportClient struct {
	err     error
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (f *fakeExportClient) ExportTelemetry(
	ctx context.Context, _ *agentv1.ExportTelemetryRequest, _ ...grpc.CallOption,
) (*agentv1.ExportTelemetryResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &agentv1.ExportTelemetryResult{}, nil
}

func newFakeClient(err error, release chan struct{}) *fakeExportClient {
	return &fakeExportClient{err: err, release: release, mu: sync.Mutex{}, calls: 0}
}

func newTestRelay(t *testing.T, client exportClient) *OTLPRelay {
	t.Helper()
	r, err := newOTLPRelay(t.Context(), "127.0.0.1:0", client, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { r.Shutdown(context.Background(), time.Second) })
	return r
}

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write(b)
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func postMetrics(r *OTLPRelay, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", otlpContentType)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestReadRelayBody(t *testing.T) {
	small := bytes.Repeat([]byte{7}, 64)
	// Incompressible data at exactly the cap: gzip framing pushes the
	// compressed size over the cap, which must not be a 413.
	atCap := make([]byte, agentv1.ExportTelemetryMaxBodyBytes)
	for i := range atCap {
		atCap[i] = byte(i*131 + i>>8)
	}
	overCap := bytes.Repeat([]byte{1}, agentv1.ExportTelemetryMaxBodyBytes+1)
	cases := []struct {
		name     string
		body     []byte
		encoding string
		wantCode int
		wantBody []byte
	}{
		{"identity ok", small, "", 0, small},
		{"identity over cap", overCap, "identity", http.StatusRequestEntityTooLarge, nil},
		{"gzip ok", gzipBytes(t, small), "gzip", 0, small},
		{"gzip decoded at cap", gzipBytes(t, atCap), "gzip", 0, atCap},
		{"gzip decoded over cap", gzipBytes(t, overCap), "gzip", http.StatusRequestEntityTooLarge, nil},
		{"malformed gzip", []byte("not gzip"), "gzip", http.StatusBadRequest, nil},
		{"unknown encoding", small, "br", http.StatusUnsupportedMediaType, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(tc.body))
			if tc.encoding != "" {
				req.Header.Set("Content-Encoding", tc.encoding)
			}
			body, code := readRelayBody(httptest.NewRecorder(), req)
			assert.Equal(t, tc.wantCode, code)
			if tc.wantCode == 0 {
				assert.True(t, bytes.Equal(tc.wantBody, body), "decoded body differs")
			}
		})
	}
}

func TestOTLPRelay_ServeHTTP_RejectsBeforeForward(t *testing.T) {
	client := newFakeClient(nil, nil)
	r := newTestRelay(t, client)
	cases := []struct {
		name        string
		method      string
		path        string
		contentType string
		wantCode    int
	}{
		{"unknown path", http.MethodPost, "/v1/profiles", otlpContentType, http.StatusNotFound},
		{"GET", http.MethodGet, "/v1/logs", otlpContentType, http.StatusMethodNotAllowed},
		{"json content type", http.MethodPost, "/v1/traces", "application/json", http.StatusUnsupportedMediaType},
		{"no content type", http.MethodPost, "/v1/traces", "", http.StatusUnsupportedMediaType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte{1}))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			assert.Equal(t, tc.wantCode, rec.Code)
		})
	}
	assert.Equal(t, 0, client.calls, "a rejected request must not reach the CP")
}

func TestOTLPRelay_ServeHTTP_MapsCPVerdict(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantCode   int
		retryAfter bool
	}{
		{"ok", nil, http.StatusOK, false},
		{"unavailable", status.Error(codes.Unavailable, "cp away"), http.StatusServiceUnavailable, true},
		{"deadline", status.Error(codes.DeadlineExceeded, "slow"), http.StatusServiceUnavailable, true},
		{"denied", status.Error(codes.PermissionDenied, "who"), http.StatusForbidden, false},
		{"unauthenticated", status.Error(codes.Unauthenticated, "who"), http.StatusForbidden, false},
		{"invalid", status.Error(codes.InvalidArgument, "bad"), http.StatusBadRequest, false},
		{"exhausted", status.Error(codes.ResourceExhausted, "big"), http.StatusRequestEntityTooLarge, false},
		{"internal", status.Error(codes.Internal, "boom"), http.StatusInternalServerError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRelay(t, newFakeClient(tc.err, nil))
			rec := postMetrics(r, []byte{1, 2, 3}, nil)
			assert.Equal(t, tc.wantCode, rec.Code)
			if tc.retryAfter {
				assert.Equal(t, strconv.Itoa(relayRetryAfterSeconds), rec.Header().Get("Retry-After"))
			} else {
				assert.Empty(t, rec.Header().Get("Retry-After"))
			}
			if tc.wantCode == http.StatusOK {
				assert.Equal(t, otlpContentType, rec.Header().Get("Content-Type"))
				assert.Empty(t, rec.Body.Bytes())
			}
		})
	}
}

func TestOTLPRelay_ServeHTTP_GzipForwardsDecoded(t *testing.T) {
	client := new(captureClient)
	r := newTestRelay(t, client)
	rec := postMetrics(r, gzipBytes(t, []byte("batch")), map[string]string{"Content-Encoding": "gzip"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []byte("batch"), client.body)
	assert.Equal(t, agentv1.Signal_SIGNAL_METRICS, client.signal)
}

// captureClient records the last forwarded request.
type captureClient struct {
	body   []byte
	signal agentv1.Signal
}

func (c *captureClient) ExportTelemetry(
	_ context.Context, in *agentv1.ExportTelemetryRequest, _ ...grpc.CallOption,
) (*agentv1.ExportTelemetryResult, error) {
	c.body = in.GetBody()
	c.signal = in.GetSignal()
	return &agentv1.ExportTelemetryResult{}, nil
}

func TestOTLPRelay_ServeHTTP_BusyOverInflightCap(t *testing.T) {
	client := newFakeClient(nil, make(chan struct{}))
	r := newTestRelay(t, client)

	var wg sync.WaitGroup
	results := make(chan int, relayMaxInflight)
	for range relayMaxInflight {
		wg.Go(func() { results <- postMetrics(r, []byte{1}, nil).Code })
	}
	// Wait until every slot holder is parked in the fake client.
	require.Eventually(t, func() bool {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.calls == relayMaxInflight
	}, 5*time.Second, 5*time.Millisecond)

	rec := postMetrics(r, []byte{1}, nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, strconv.Itoa(relayRetryAfterSeconds), rec.Header().Get("Retry-After"))
	assert.Equal(t, relayMaxInflight, client.calls, "the busy request must not reach the CP")

	close(client.release)
	wg.Wait()
	close(results)
	for code := range results {
		assert.Equal(t, http.StatusOK, code)
	}
	// Slots are released: the next request is forwarded.
	assert.Equal(t, http.StatusOK, postMetrics(r, []byte{1}, nil).Code)
}

func TestOTLPRelay_Shutdown_StopsListener(t *testing.T) {
	r, err := newOTLPRelay(t.Context(), "127.0.0.1:0", newFakeClient(nil, nil), logger.Nop())
	require.NoError(t, err)
	addr := r.Addr().String()

	resp, err := http.Get("http://" + addr + "/v1/logs")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	done := make(chan struct{})
	go func() {
		r.Shutdown(context.Background(), time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return")
	}
	_, err = http.Get("http://" + addr + "/v1/logs")
	require.Error(t, err, "listener must be closed after Shutdown")

	var nilRelay *OTLPRelay
	nilRelay.Shutdown(context.Background(), time.Second) // nil-safe
}
