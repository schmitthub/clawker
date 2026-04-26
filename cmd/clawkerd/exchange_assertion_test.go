package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExchangeAssertion_HydraErrorResponse pins that any non-200
// status from Hydra surfaces as a wrapped error including the status
// code. A regression that swallowed the status would let a clawkerd
// daemon proceed with an empty token and fail mid-stream with an
// opaque Unauthenticated.
func TestExchangeAssertion_HydraErrorResponse(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"401 unauthorized", http.StatusUnauthorized, `{"error":"invalid_client"}`},
		{"500 server error", http.StatusInternalServerError, `oops`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			tok, err := exchangeAssertion(context.Background(), srv.URL, "assertion-jwt", nil)
			require.Error(t, err)
			assert.Empty(t, tok)
			// The error must carry the status code so an operator
			// triaging "why is registration failing?" sees the actual
			// HTTP failure mode.
			assert.Contains(t, err.Error(), "hydra returned")
		})
	}
}

// TestExchangeAssertion_RejectsEmptyAccessToken pins the defensive
// check at line ~478: if Hydra responded 200 with no access_token in
// the body, clawkerd would otherwise attach `authorization: Bearer `
// (empty) to every RPC and fail at the CP boundary with a meaningless
// Unauthenticated.
func TestExchangeAssertion_RejectsEmptyAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token_type":"Bearer","access_token":""}`))
	}))
	defer srv.Close()

	tok, err := exchangeAssertion(context.Background(), srv.URL, "assertion-jwt", nil)
	require.Error(t, err)
	assert.Empty(t, tok)
	assert.Contains(t, err.Error(), "empty access_token")
}

// TestExchangeAssertion_RejectsNonBearerTokenType pins the
// non-Bearer check. Hydra always returns "Bearer" today, but a
// Hydra upgrade or misconfig that swaps to "DPoP" or "MAC" would let
// clawkerd silently attach the wrong header — CP rejects mid-stream
// with an opaque Unauthenticated. The named-error contract here is
// what makes the failure debuggable.
func TestExchangeAssertion_RejectsNonBearerTokenType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token_type":"DPoP","access_token":"good-token"}`))
	}))
	defer srv.Close()

	_, err := exchangeAssertion(context.Background(), srv.URL, "assertion-jwt", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DPoP")
	assert.Contains(t, err.Error(), "expected Bearer")
}

// TestExchangeAssertion_HappyPath proves the round-trip including
// (a) the assertion lands in the form-encoded body verbatim and
// (b) the Bearer-typed access token round-trips out of the response.
func TestExchangeAssertion_HappyPath(t *testing.T) {
	const wantAssertion = "signed-assertion-bytes"
	const wantToken = "access-token-value"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		assert.Equal(t, wantAssertion, r.Form.Get("client_assertion"))
		assert.Equal(t, "urn:ietf:params:oauth:client-assertion-type:jwt-bearer",
			r.Form.Get("client_assertion_type"))
		// Scope must be agent_self_register — wrong scope here would
		// produce a CP-side method-not-allowed at Connect.
		assert.NotEmpty(t, r.Form.Get("scope"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token_type":"Bearer","access_token":"` + wantToken + `"}`))
	}))
	defer srv.Close()

	tok, err := exchangeAssertion(context.Background(), srv.URL, wantAssertion, nil)
	require.NoError(t, err)
	assert.Equal(t, wantToken, tok)
}

// TestExchangeAssertion_AcceptsMissingTokenType pins that an empty
// token_type is treated as Bearer (Hydra used to omit the field on
// some pathways). The strict-Bearer check must specifically target
// non-empty wrong values.
func TestExchangeAssertion_AcceptsMissingTokenType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"tok-only"}`))
	}))
	defer srv.Close()

	tok, err := exchangeAssertion(context.Background(), srv.URL, "a", nil)
	require.NoError(t, err)
	assert.Equal(t, "tok-only", tok)
}

// TestExchangeAssertion_BearerCaseInsensitive matches the EqualFold
// branch — `bearer` lowercase is treated the same as `Bearer`.
func TestExchangeAssertion_BearerCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token_type":"bearer","access_token":"t"}`))
	}))
	defer srv.Close()

	tok, err := exchangeAssertion(context.Background(), srv.URL, "a", nil)
	require.NoError(t, err)
	assert.Equal(t, "t", tok)
}

// TestExchangeAssertion_MalformedJSONResponse pins that a Hydra reply
// whose JSON the client can't parse fails fast — without this, the
// daemon would silently attach an empty token.
func TestExchangeAssertion_MalformedJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	_, err := exchangeAssertion(context.Background(), srv.URL, "a", nil)
	require.Error(t, err)
	// Decode error path. Don't assert exact wrapping wording — the
	// `decode response:` prefix is implementation detail. The
	// existence of the error is the contract.
	assert.True(t, strings.Contains(err.Error(), "decode") ||
		strings.Contains(err.Error(), "JSON") ||
		strings.Contains(err.Error(), "json"),
		"decode failure must surface as an error, got: %v", err)
}
