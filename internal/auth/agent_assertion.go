package auth

import (
	"crypto/ecdsa"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/schmitthub/clawker/internal/consts"
)

// AgentAssertionTTL bounds how long a CLI-signed agent assertion stays
// valid at Hydra. Sized for typical container session length: a single
// assertion can refresh access tokens for a full working day before
// clawkerd's parent container would be expected to be torn down.
const AgentAssertionTTL = 24 * time.Hour

// BuildAgentAssertion signs an RFC 7523 client_assertion identifying
// the calling clawkerd as the clawker-agent OAuth2 client at Hydra. The
// assertion is consumed by clawkerd at boot to obtain the access token
// it needs for AgentService.Register; it is NOT used for per-agent
// identity (that comes from the mTLS cert thumbprint at Register).
//
// Same private key as the CLI client (`clawker-cli`) — distinct
// client_id + scope keeps the AuthZ surface clean even though the
// signing key is shared. See `RegisterAgentClient` for the Hydra-side
// counterpart.
//
// iat is minted in the host clock. The host clock is the source of truth:
// Docker forces the CP/VM clock to track the host, so a host-minted iat is
// already in the domain Hydra validates against. The only divergence is the
// transient window where a just-woken VM clock still lags; that is handled
// by *waiting* until the CP clock has caught up to the host before the
// assertion is exchanged (the pre-start CP-ensure for this baked agent
// assertion, the dial-time wait for the CLI's own) — never by shifting iat.
func BuildAgentAssertion(audience string, signingKey *ecdsa.PrivateKey) (string, error) {
	if audience == "" {
		return "", fmt.Errorf("agent assertion: audience required")
	}
	if signingKey == nil {
		return "", fmt.Errorf("agent assertion: signing key required")
	}
	claims := AssertionClaims{
		Issuer:           consts.ClientIDAgent,
		Subject:          consts.ClientIDAgent,
		Audience:         audience,
		JWTID:            uuid.NewString(),
		ExpiresInSeconds: int(AgentAssertionTTL / time.Second),
	}
	return BuildSignedAssertion(claims, signingKey)
}
