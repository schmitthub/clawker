package auth

import (
	"crypto/ecdsa"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

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
	// Now is the reference clock for iat/exp. Zero → time.Now(), which is
	// what production always uses: the host clock is the source of truth
	// (the CP/VM clock is Docker-forced to track it), so no per-mint clock
	// override is applied. Host↔CP drift in the transient post-sleep window
	// is handled by *waiting* for reconvergence before minting/exchanging,
	// not by shifting this reference. Exists as an explicit seam so tests
	// can pin iat/exp deterministically.
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

	// Reference clock: caller-supplied (test override) when set, else local.
	now := claims.Now
	if now.IsZero() {
		now = time.Now()
	}
	jc := jwtClaims{
		Issuer:   claims.Issuer,
		Subject:  claims.Subject,
		Audience: claims.Audience,
		JWTID:    claims.JWTID,
		// exp is a forward window from the reference clock; iat is the
		// reference clock itself (no backdate). nbf is left unset — a future
		// nbf would trip fosite's zero-leeway check. Callers that exchange an
		// assertion wait until the CP clock has caught up to the host first,
		// so a host-clock iat is always in the CP's past.
		Expiry:   jwt.NewNumericDate(now.Add(time.Duration(claims.ExpiresInSeconds) * time.Second)),
		IssuedAt: jwt.NewNumericDate(now),
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
