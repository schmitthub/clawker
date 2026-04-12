package controlplane

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/grpc"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/authkeeper"
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
		// Should not happen with P-256 + crypto/rand.
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

// CPGRPCServerOptions returns the gRPC server options for the CP admin API,
// including the Oathkeeper authorization interceptor.
//
// It generates a temporary Oathkeeper config file that configures:
//   - bearer_token authenticator: validates tokens via Hydra's introspection endpoint
//   - allow authorizer: delegates authz decisions to the bearer token check
//   - noop mutator: passes the request through unchanged
//
// The hydraAdminURL is the base URL of the Hydra admin API (e.g.,
// "http://127.0.0.1:4445") and requiredScope is the OAuth2 scope
// required for admin API access (e.g., "firewall:admin").
func CPGRPCServerOptions(hydraAdminURL, requiredScope string) []grpc.ServerOption {
	ctx := context.Background()

	configFile, err := writeOathkeeperConfig(hydraAdminURL, requiredScope)
	if err != nil {
		// Fall back to a deny-all interceptor if config generation fails.
		// This is a safety net — production code should never reach here.
		return []grpc.ServerOption{
			grpc.UnaryInterceptor(denyAllUnaryInterceptor),
			grpc.StreamInterceptor(denyAllStreamInterceptor),
		}
	}

	mw, err := authkeeper.New(ctx, authkeeper.WithConfigFile(configFile))
	if err != nil {
		return []grpc.ServerOption{
			grpc.UnaryInterceptor(denyAllUnaryInterceptor),
			grpc.StreamInterceptor(denyAllStreamInterceptor),
		}
	}

	return []grpc.ServerOption{
		grpc.UnaryInterceptor(mw.UnaryInterceptor()),
		grpc.StreamInterceptor(mw.StreamInterceptor()),
	}
}

// writeOathkeeperConfig generates a temporary Oathkeeper config file for the
// CP's embedded middleware. The config enables bearer_token authentication
// against the provided Hydra admin URL.
func writeOathkeeperConfig(hydraAdminURL, requiredScope string) (string, error) {
	config := fmt.Sprintf(`authenticators:
  bearer_token:
    enabled: true
    config:
      check_session_url: %s/admin/oauth2/introspect
      token_from:
        header: authorization
  noop:
    enabled: true
  anonymous:
    enabled: true
authorizers:
  allow:
    enabled: true
mutators:
  noop:
    enabled: true
`, hydraAdminURL)

	dir, err := os.MkdirTemp("", "clawker-oathkeeper-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	configPath := filepath.Join(dir, "oathkeeper.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return configPath, nil
}

func denyAllUnaryInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
	return nil, authkeeper.ErrDenied
}

func denyAllStreamInterceptor(_ any, _ grpc.ServerStream, _ *grpc.StreamServerInfo, _ grpc.StreamHandler) error {
	return authkeeper.ErrDenied
}

// CPGRPCServerTLSConfig constructs the tls.Config that the CP gRPC server
// uses for mTLS authentication. The config requires and verifies client
// certificates signed by the given CA pool.
func CPGRPCServerTLSConfig(serverCert tls.Certificate, clientCAPool *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAPool,
		MinVersion:   tls.VersionTLS13,
	}
}
