package clawkerd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	agentmocks "github.com/schmitthub/clawker/api/agent/v1/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// relayCall sends one request to a host relay backed by client and
// returns the status and the decoded JSON body.
func relayCall(
	t *testing.T, client agentv1.AgentServiceClient, method, path, body string,
) (int, map[string]any) {
	t.Helper()
	relay := newHostRelayHandler(client, logger.Nop())
	req := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	relay.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Header().Get("Content-Type") == "application/json" {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	}
	return rec.Code, out
}

func TestHostRelay_OpenURL(t *testing.T) {
	client := new(agentmocks.AgentServiceClientMock)
	client.OpenURLFunc = func(
		_ context.Context, in *agentv1.OpenURLRequest, _ ...grpc.CallOption,
	) (*agentv1.OpenURLResult, error) {
		return new(agentv1.OpenURLResult), nil
	}

	code, body := relayCall(t, client, http.MethodPost, "/open/url", `{"url":"https://example.com/x"}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, body["success"])
	require.Len(t, client.OpenURLCalls(), 1)
	assert.Equal(t, "https://example.com/x", client.OpenURLCalls()[0].In.GetUrl())
}

// TestHostRelay_StatusMapping pins how a CP verdict reaches the container
// scripts: they read the HTTP status and the error field.
func TestHostRelay_StatusMapping(t *testing.T) {
	cases := []struct {
		code codes.Code
		want int
	}{
		{codes.PermissionDenied, http.StatusForbidden},
		{codes.InvalidArgument, http.StatusBadRequest},
		{codes.NotFound, http.StatusNotFound},
		{codes.Unavailable, http.StatusServiceUnavailable},
		{codes.Internal, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.code.String(), func(t *testing.T) {
			client := new(agentmocks.AgentServiceClientMock)
			client.OpenURLFunc = func(
				context.Context, *agentv1.OpenURLRequest, ...grpc.CallOption,
			) (*agentv1.OpenURLResult, error) {
				return nil, status.Error(tc.code, "nope")
			}
			code, body := relayCall(t, client, http.MethodPost, "/open/url", `{"url":"https://example.com"}`)
			assert.Equal(t, tc.want, code)
			assert.Equal(t, false, body["success"])
			assert.NotEmpty(t, body["error"])
		})
	}
}

func TestHostRelay_GitCredential(t *testing.T) {
	client := new(agentmocks.AgentServiceClientMock)
	client.GitCredentialFunc = func(
		_ context.Context, in *agentv1.GitCredentialRequest, _ ...grpc.CallOption,
	) (*agentv1.GitCredentialResult, error) {
		return &agentv1.GitCredentialResult{Protocol: "https", Host: "github.com", Username: "u", Password: "p"}, nil
	}

	code, body := relayCall(t, client, http.MethodPost, "/git/credential",
		`{"action":"get","protocol":"https","host":"github.com","path":"","username":"","password":""}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, map[string]any{
		"success": true, "protocol": "https", "host": "github.com", "username": "u", "password": "p",
	}, body, "git-credential-clawker prints every non-empty key but success and error")
	require.Len(t, client.GitCredentialCalls(), 1)
	assert.Equal(t, agentv1.GitCredentialAction_GIT_CREDENTIAL_ACTION_GET,
		client.GitCredentialCalls()[0].In.GetAction())
}

// TestHostRelay_GitCredential_HelperFailed: a host helper failure is a
// failed lookup for git, not a transport error.
func TestHostRelay_GitCredential_HelperFailed(t *testing.T) {
	client := new(agentmocks.AgentServiceClientMock)
	client.GitCredentialFunc = func(
		context.Context, *agentv1.GitCredentialRequest, ...grpc.CallOption,
	) (*agentv1.GitCredentialResult, error) {
		return nil, status.Error(codes.FailedPrecondition, "helper failed")
	}
	code, body := relayCall(t, client, http.MethodPost, "/git/credential", `{"action":"get","host":"github.com"}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, false, body["success"])
}

func TestHostRelay_GitCredential_UnknownAction(t *testing.T) {
	client := new(agentmocks.AgentServiceClientMock)
	code, _ := relayCall(t, client, http.MethodPost, "/git/credential", `{"action":"approve","host":"github.com"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Empty(t, client.GitCredentialCalls())
}

func TestHostRelay_Callbacks(t *testing.T) {
	client := new(agentmocks.AgentServiceClientMock)
	client.RegisterCallbackFunc = func(
		context.Context, *agentv1.RegisterCallbackRequest, ...grpc.CallOption,
	) (*agentv1.RegisterCallbackResult, error) {
		return &agentv1.RegisterCallbackResult{SessionId: "s1", Port: 54545, ExpiresAtUnix: 1700000300}, nil
	}
	client.GetCallbackDataFunc = func(
		context.Context, *agentv1.GetCallbackDataRequest, ...grpc.CallOption,
	) (*agentv1.GetCallbackDataResult, error) {
		return &agentv1.GetCallbackDataResult{Received: true, Callback: &agentv1.CallbackData{
			Method: "GET", Path: "/cb", Query: "code=x", Headers: map[string]string{"Accept": "*/*"},
			Body: "", ReceivedAtUnix: 1700000000,
		}}, nil
	}
	client.DeleteCallbackFunc = func(
		context.Context, *agentv1.DeleteCallbackRequest, ...grpc.CallOption,
	) (*agentv1.DeleteCallbackResult, error) {
		return new(agentv1.DeleteCallbackResult), nil
	}

	code, reg := relayCall(t, client, http.MethodPost, "/callback/register",
		`{"authUrl":"https://auth.example.com/?redirect_uri=http%3A%2F%2Flocalhost%3A54545%2Fcb","ttlSeconds":300}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "s1", reg["sessionId"])
	assert.EqualValues(t, 54545, reg["port"])
	assert.Equal(t, "2023-11-14T22:18:20Z", reg["expiresAt"])
	in := client.RegisterCallbackCalls()[0].In
	assert.Equal(t, int32(300), in.GetTtlSeconds())

	code, data := relayCall(t, client, http.MethodGet, "/callback/s1/data", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, data["received"])
	cb, ok := data["callback"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "code=x", cb["query"])
	assert.Equal(t, "/cb", cb["path"])
	assert.Equal(t, "s1", client.GetCallbackDataCalls()[0].In.GetSessionId())

	code, _ = relayCall(t, client, http.MethodDelete, "/callback/s1", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "s1", client.DeleteCallbackCalls()[0].In.GetSessionId())
}

func TestHostRelay_UnknownRoute(t *testing.T) {
	code, _ := relayCall(t, new(agentmocks.AgentServiceClientMock), http.MethodGet, "/health", "")
	assert.Equal(t, http.StatusNotFound, code)
}
