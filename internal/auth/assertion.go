package auth

import (
	"crypto/ecdsa"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// assertionClockSkewLeeway backdates the assertion's iat as a
// defense-in-depth floor. fosite — which Hydra uses to validate
// private_key_jwt client_assertions — enforces iat with ZERO tolerance
// (requires now >= iat with no clock-skew accounting) and exposes no
// server-side leeway knob, so a minting clock even marginally ahead of
// Hydra's clock yields HTTP 500 "Token used before issued". The primary
// defense is clock alignment: callers exposed to host↔CP drift (the CLI on
// Docker Desktop, whose LinuxKit VM clock lags after the host sleeps) set
// AssertionClaims.Now to the CP's own clock via GetSystemTime, eliminating
// the bulk of the skew. This floor only has to absorb the residual —
// measurement RTT plus the gap before Hydra validates — so relative to that
// sub-second residual it is deliberately generous, leaving headroom for an
// imperfect skew measurement (or none at all, when the probe degraded). It
// is applied unconditionally, including for in-container minters (clawkerd)
// that already share Hydra's kernel clock, where it is a harmless backdate.
// nbf is left unset (a future nbf would trip the same zero-leeway check).
// Backdating is safe: the client-auth path applies no iat-too-old check.
const assertionClockSkewLeeway = 15 * time.Second

// AssertionClaims holds the claims for a client assertion JWT per RFC 7523.
type AssertionClaims struct {
	// Issuer (iss) — must be the client_id.
	Issuer string
	// Subject (sub) — must be the client_id.
	Subject string
	// Audience (aud) — must be the Hydra token endpoint URL.
	Audience string
	// JWTID (jti) — cryptographically random unique ID.
	JWTID string
	// ExpiresInSeconds is the duration until expiration. The CLI assertion is
	// short-lived (~30s); the agent assertion uses AgentAssertionTTL (24h), so
	// callers set this per use site — do not assume a short value here.
	ExpiresInSeconds int
	// Now is the reference clock for iat/exp. Zero → time.Now(). Both
	// host-minted assertions set it to CP-aligned time (local now + skew
	// measured via adminclient.ProbeClockSkew / GetSystemTime) so iat lands
	// in the CP clock domain Hydra/fosite validates against with zero
	// leeway: the CLI's own `clawker-cli` assertion (adminclient.Dial) and
	// the `clawker-agent` assertion the CLI bakes into a container's
	// bootstrap material (BuildAgentAssertion). clawkerd does not mint — it
	// only exchanges the pre-minted agent assertion at Hydra. Zero is the
	// fallback when skew is unmeasured (degrades to host-clock iat, which
	// the residual leeway floor absorbs for small drift).
	Now time.Time
}

// jwtClaims is the serialized form of AssertionClaims for JWT encoding.
type jwtClaims struct {
	Issuer   string           `json:"iss"`
	Subject  string           `json:"sub"`
	Audience string           `json:"aud"`
	JWTID    string           `json:"jti"`
	Expiry   *jwt.NumericDate `json:"exp"`
	IssuedAt *jwt.NumericDate `json:"iat"`
}

// BuildSignedAssertion creates a signed JWT assertion per RFC 7523 for
// use in private_key_jwt client authentication with Hydra.
// The assertion is signed with ES256 (ECDSA P-256).
// Returns the signed JWT string.
func BuildSignedAssertion(claims AssertionClaims, signingKey *ecdsa.PrivateKey) (string, error) {
	if err := ValidateAssertionClaims(claims); err != nil {
		return "", fmt.Errorf("invalid claims: %w", err)
	}

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: signingKey},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		return "", fmt.Errorf("create signer: %w", err)
	}

	// Reference clock: caller-supplied (CP-aligned) when set, else local.
	now := claims.Now
	if now.IsZero() {
		now = time.Now()
	}
	jc := jwtClaims{
		Issuer:   claims.Issuer,
		Subject:  claims.Subject,
		Audience: claims.Audience,
		JWTID:    claims.JWTID,
		// exp is a forward window from the reference clock; iat is backdated
		// by the residual leeway floor (nbf left unset — a future nbf trips
		// the same zero-leeway check). See assertionClockSkewLeeway.
		Expiry:   jwt.NewNumericDate(now.Add(time.Duration(claims.ExpiresInSeconds) * time.Second)),
		IssuedAt: jwt.NewNumericDate(now.Add(-assertionClockSkewLeeway)),
	}

	signed, err := jwt.Signed(signer).Claims(jc).Serialize()
	if err != nil {
		return "", fmt.Errorf("sign assertion: %w", err)
	}

	return signed, nil
}

// ValidateAssertionClaims checks that all required RFC 7523 claims are present.
// Returns an error describing the first missing or invalid claim.
func ValidateAssertionClaims(claims AssertionClaims) error {
	if claims.Issuer == "" {
		return fmt.Errorf("iss (issuer) is required")
	}
	if claims.Subject == "" {
		return fmt.Errorf("sub (subject) is required")
	}
	if claims.Audience == "" {
		return fmt.Errorf("aud (audience) is required")
	}
	if claims.JWTID == "" {
		return fmt.Errorf("jti (JWT ID) is required")
	}
	if claims.ExpiresInSeconds <= 0 {
		return fmt.Errorf("exp (expiration) must be positive")
	}
	return nil
}
