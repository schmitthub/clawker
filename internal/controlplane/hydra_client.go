package controlplane

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/schmitthub/clawker/internal/consts"
)

// HydraClientRegistration represents the configuration for registering
// the CLI client with Hydra. This corresponds to the Hydra client model
// from github.com/ory/hydra-client-go.
type HydraClientRegistration struct {
	ClientID                    string
	GrantTypes                  []string
	TokenEndpointAuthMethod     string
	TokenEndpointAuthSigningAlg string
	Scope                       string
	// JWKS is the JSON Web Key Set containing the client's public key(s)
	// for verifying private_key_jwt assertions.
	JWKS json.RawMessage
}

// CLIClientRegistration returns the Hydra client registration config
// for the CLI client. This is used during CP startup to register
// the CLI with Hydra via the Go SDK.
func CLIClientRegistration() *HydraClientRegistration {
	// Generate a temporary ES256 key pair for the JWK. In production,
	// this would use the persisted signing key; for registration config
	// validation, we need a structurally valid JWKS.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil
	}

	jwks, err := buildJWKS(&key.PublicKey)
	if err != nil {
		return nil
	}

	return &HydraClientRegistration{
		ClientID:                    consts.ClientIDCLI,
		GrantTypes:                  []string{"client_credentials"},
		TokenEndpointAuthMethod:     "private_key_jwt",
		TokenEndpointAuthSigningAlg: "ES256",
		Scope:                       consts.ScopeAdmin,
		JWKS:                        jwks,
	}
}

// buildJWKS exports an ECDSA P-256 public key as a JWKS JSON document.
func buildJWKS(pub *ecdsa.PublicKey) (json.RawMessage, error) {
	if pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("unsupported curve: expected P-256")
	}

	// Encode x and y coordinates as base64url (no padding), 32 bytes each for P-256.
	xBytes := pub.X.Bytes()
	yBytes := pub.Y.Bytes()

	// Pad to 32 bytes (P-256 coordinate size).
	xPadded := make([]byte, 32)
	yPadded := make([]byte, 32)
	copy(xPadded[32-len(xBytes):], xBytes)
	copy(yPadded[32-len(yBytes):], yBytes)

	jwk := map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(xPadded),
		"y":   base64.RawURLEncoding.EncodeToString(yPadded),
	}

	jwks := map[string]any{
		"keys": []any{jwk},
	}

	return json.Marshal(jwks)
}

// AdminMethodScopes returns the method→scope map for the AdminService.
// Used by NewAuthInterceptor when wiring the CP gRPC server.
func AdminMethodScopes() map[string]string {
	const svc = "/clawker.admin.v1.AdminService/"
	return map[string]string{
		svc + "Install":         consts.ScopeAdmin,
		svc + "Remove":          consts.ScopeAdmin,
		svc + "Enable":          consts.ScopeAdmin,
		svc + "Disable":         consts.ScopeAdmin,
		svc + "SyncRoutes":      consts.ScopeAdmin,
		svc + "ResolveHostname": consts.ScopeAdmin,
	}
}
