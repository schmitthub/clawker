package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBearerCreds_RequireTransportSecurity pins the security-critical
// invariant that this credential REFUSES to attach over plaintext.
// A regression that flipped this to false would let a misconfigured
// dial leak the bearer token in cleartext over the wire. The agent
// listener is mTLS-only by construction, so this is always satisfied
// in production — but the contract has to be load-bearing-asserted.
func TestBearerCreds_RequireTransportSecurity(t *testing.T) {
	c := newBearerCreds("any-token")
	assert.True(t, c.RequireTransportSecurity(),
		"bearerCreds MUST require transport security to prevent token leak")
}

// TestBearerCreds_GetRequestMetadata pins the Authorization header
// shape: "authorization: Bearer <token>". A regression that dropped
// the "Bearer " prefix or used a different header name would break
// every RPC at the CP's AuthInterceptor boundary.
func TestBearerCreds_GetRequestMetadata(t *testing.T) {
	const tok = "raw-jwt-bytes"
	c := newBearerCreds(tok)

	md, err := c.GetRequestMetadata(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+tok, md["authorization"],
		"bearer creds MUST emit 'authorization: Bearer <token>'")
}

// TestBearerCreds_EmptyToken — defensive: an empty token would attach
// `authorization: Bearer ` (with trailing space + empty token), which
// is technically valid syntax but rejected by every introspector.
// We don't currently filter at the interceptor (the constructor
// doesn't refuse empty); document the current behavior so a future
// caller knows.
func TestBearerCreds_EmptyToken(t *testing.T) {
	md, err := newBearerCreds("").GetRequestMetadata(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer ", md["authorization"],
		"empty token still produces a 'Bearer ' header — caller is responsible for refusing to dial without a token")
}
