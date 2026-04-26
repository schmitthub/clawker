package main

import (
	"context"

	"google.golang.org/grpc/credentials"
)

// bearerCreds implements credentials.PerRPCCredentials so the
// "authorization: Bearer <token>" header is attached to every
// outgoing RPC — unary AND streaming. A unary-only interceptor would
// silently skip the Connect server-stream and Events client-stream,
// causing the CP's AuthInterceptor to reject those RPCs with
// codes.Unauthenticated before clawkerd ever sees Welcome.
//
// clawkerd holds one token for the process lifetime; if the token
// expires the stream simply errors out and the daemon exits, leaving
// the container's restart policy to decide whether to retry. Token
// refresh lands with the cp-restart-resilience initiative alongside
// reconnect-with-backoff.
type bearerCreds struct {
	token string
}

func (c bearerCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + c.token}, nil
}

// RequireTransportSecurity returns true so an accidental swap to
// plaintext credentials drops the bearer rather than leaks it. The
// agent listener is mTLS-only by construction, so this is always
// satisfied in production.
func (bearerCreds) RequireTransportSecurity() bool { return true }

// newBearerCreds is the constructor used by main; the type itself is
// unexported so no external caller can forge a credential.
func newBearerCreds(token string) credentials.PerRPCCredentials {
	return bearerCreds{token: token}
}
