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
	// ExpiresIn is the duration until expiration (typically 30-60s).
	ExpiresInSeconds int
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

	now := time.Now()
	jc := jwtClaims{
		Issuer:   claims.Issuer,
		Subject:  claims.Subject,
		Audience: claims.Audience,
		JWTID:    claims.JWTID,
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
