package hostgateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/controlplane/hostgateway"
	"github.com/schmitthub/clawker/internal/logger"
)

func agentA() hostgateway.Identity {
	return hostgateway.Identity{Project: "proj", Agent: "a", ContainerID: "ctr-a"}
}

func agentB() hostgateway.Identity {
	return hostgateway.Identity{Project: "proj", Agent: "b", ContainerID: "ctr-b"}
}

// hostProxy is a stand-in host proxy: it records each request and answers
// with the status and JSON body the test sets per path.
type hostProxy struct {
	mu       sync.Mutex
	requests []recorded
	replies  map[string]reply
}

type recorded struct {
	Method, Path string
	Body         map[string]any
}

type reply struct {
	status int
	body   any
}

func newHostProxy(t *testing.T, replies map[string]reply) (*hostProxy, *hostgateway.Gateway) {
	t.Helper()
	hp := &hostProxy{mu: sync.Mutex{}, requests: nil, replies: replies}
	srv := httptest.NewTLSServer(http.HandlerFunc(hp.serve))
	t.Cleanup(srv.Close)
	gw, err := hostgateway.New(srv.URL, srv.Client(), logger.Nop())
	require.NoError(t, err)
	return hp, gw
}

func (hp *hostProxy) serve(w http.ResponseWriter, r *http.Request) {
	rec := recorded{Method: r.Method, Path: r.URL.Path, Body: nil}
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &rec.Body)
	}
	hp.mu.Lock()
	hp.requests = append(hp.requests, rec)
	rep, ok := hp.replies[r.Method+" "+r.URL.Path]
	hp.mu.Unlock()
	if !ok {
		rep = reply{http.StatusOK, map[string]any{"success": true}}
	}
	w.Header().Set("Content-Type", "application/json")
	raw, err := json.Marshal(rep.body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(rep.status)
	_, _ = w.Write(raw)
}

func (hp *hostProxy) calls() []recorded {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	return append([]recorded(nil), hp.requests...)
}

func TestGateway_OpenURL_Forwards(t *testing.T) {
	hp, gw := newHostProxy(t, nil)

	require.NoError(t, gw.OpenURL(context.Background(), agentA(), "https://example.com/x"))

	calls := hp.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, http.MethodPost, calls[0].Method)
	assert.Equal(t, "/open/url", calls[0].Path)
	assert.Equal(t, "https://example.com/x", calls[0].Body["url"])
}

// TestGateway_StatusMapping pins how a host-proxy verdict reaches the
// handler: an egress deny is ErrDenied, a malformed request is
// ErrInvalidRequest, and a host-side fault is ErrUnavailable.
func TestGateway_StatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"egress deny", http.StatusForbidden, hostgateway.ErrDenied},
		{"bad request", http.StatusBadRequest, hostgateway.ErrInvalidRequest},
		{"host fault", http.StatusInternalServerError, hostgateway.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, gw := newHostProxy(t, map[string]reply{
				"POST /open/url": {tc.status, map[string]any{"success": false, "error": "no"}},
			})
			err := gw.OpenURL(context.Background(), agentA(), "https://example.com")
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestGateway_Unreachable(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	client := srv.Client()
	srv.Close()
	gw, err := hostgateway.New(srv.URL, client, logger.Nop())
	require.NoError(t, err)

	err = gw.OpenURL(context.Background(), agentA(), "https://example.com")
	require.ErrorIs(t, err, hostgateway.ErrUnavailable)
}

func TestGateway_GitCredential(t *testing.T) {
	hp, gw := newHostProxy(t, map[string]reply{
		"POST /git/credential": {http.StatusOK, map[string]any{
			"success": true, "protocol": "https", "host": "github.com",
			"username": "u", "password": "p",
		}},
	})
	req := gitGet()

	res, err := gw.GitCredential(context.Background(), agentA(), req)
	require.NoError(t, err)
	assert.Equal(t, "u", res.GetUsername())
	assert.Equal(t, "p", res.GetPassword())

	calls := hp.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "get", calls[0].Body["action"])
	assert.Equal(t, "github.com", calls[0].Body["host"])
}

// TestGateway_GitCredential_HelperFailure: the host helper failed (the
// host proxy answers 200 with success false). The agent sees a failed
// action, not a credential.
func TestGateway_GitCredential_HelperFailure(t *testing.T) {
	_, gw := newHostProxy(t, map[string]reply{
		"POST /git/credential": {http.StatusOK, map[string]any{"success": false, "error": "helper failed"}},
	})
	req := gitGet()
	_, err := gw.GitCredential(context.Background(), agentA(), req)
	require.ErrorIs(t, err, hostgateway.ErrActionFailed)
}

func gitGet() *agentv1.GitCredentialRequest {
	return &agentv1.GitCredentialRequest{
		Action:   agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET,
		Protocol: "https",
		Host:     "github.com",
		Path:     "",
		Username: "",
		Password: "",
	}
}

func registerReply(sessionID string, expires time.Time) reply {
	return reply{http.StatusOK, map[string]any{
		"success": true, "sessionId": sessionID, "port": 54545,
		"expiresAt": expires.Format(time.RFC3339),
	}}
}

// TestGateway_CallbackOwnership: a callback session belongs to the
// container that registered it. Another container cannot read or delete
// it, and its requests never reach the host proxy.
func TestGateway_CallbackOwnership(t *testing.T) {
	hp, gw := newHostProxy(t, map[string]reply{
		"POST /callback/register": registerReply("s1", time.Now().Add(5*time.Minute)),
		"GET /callback/s1/data": {http.StatusOK, map[string]any{
			"received": true,
			"callback": map[string]any{
				"method": "GET", "path": "/cb", "query": "code=x",
				"receivedAt": time.Unix(1700000000, 0).UTC().Format(time.RFC3339),
			},
		}},
	})
	ctx := context.Background()

	reg, err := gw.RegisterCallback(ctx, agentA(), &agentv1.RegisterCallbackRequest{
		AuthUrl: "https://auth.example.com/?redirect_uri=http://localhost:54545/cb", TtlSeconds: 60,
	})
	require.NoError(t, err)
	assert.Equal(t, "s1", reg.GetSessionId())
	assert.Equal(t, int32(54545), reg.GetPort())
	body := hp.calls()[0].Body
	assert.Equal(t, "https://auth.example.com/?redirect_uri=http://localhost:54545/cb", body["authUrl"])
	assert.EqualValues(t, 60, body["ttlSeconds"])

	_, err = gw.GetCallbackData(ctx, agentB(), "s1")
	require.ErrorIs(t, err, hostgateway.ErrNotOwner)
	require.ErrorIs(t, gw.DeleteCallback(ctx, agentB(), "s1"), hostgateway.ErrNotOwner)
	require.Len(t, hp.calls(), 1, "a non-owner request must not reach the host proxy")

	data, err := gw.GetCallbackData(ctx, agentA(), "s1")
	require.NoError(t, err)
	require.True(t, data.GetReceived())
	assert.Equal(t, "code=x", data.GetCallback().GetQuery())
	assert.Equal(t, int64(1700000000), data.GetCallback().GetReceivedAtUnix())

	require.NoError(t, gw.DeleteCallback(ctx, agentA(), "s1"))
	_, err = gw.GetCallbackData(ctx, agentA(), "s1")
	require.ErrorIs(t, err, hostgateway.ErrNotOwner, "a deleted session has no owner")
}

func TestGateway_CallbackUnknownSession(t *testing.T) {
	hp, gw := newHostProxy(t, nil)
	_, err := gw.GetCallbackData(context.Background(), agentA(), "never-registered")
	require.ErrorIs(t, err, hostgateway.ErrNotOwner)
	assert.Empty(t, hp.calls())
}

// TestGateway_CallbackOwnerExpires: the ownership record ends with the
// session lifetime the host proxy reported.
func TestGateway_CallbackOwnerExpires(t *testing.T) {
	expires := time.Unix(1700000300, 0)
	_, gw := newHostProxy(t, map[string]reply{
		"POST /callback/register": registerReply("s1", expires),
	})
	now := time.Unix(1700000000, 0)
	gw.Clock = func() time.Time { return now }
	ctx := context.Background()

	req := &agentv1.RegisterCallbackRequest{AuthUrl: "https://a.example.com", TtlSeconds: 0}
	_, err := gw.RegisterCallback(ctx, agentA(), req)
	require.NoError(t, err)

	now = expires.Add(time.Second)
	_, err = gw.GetCallbackData(ctx, agentA(), "s1")
	require.ErrorIs(t, err, hostgateway.ErrNotOwner)
}
