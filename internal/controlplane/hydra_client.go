package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/consts"
)

// RegisterCLIClient registers the clawker-cli OAuth2 client with Hydra
// via the admin API. The jwkData is the raw JSON of the CLI's public
// JWKS (bind-mounted from the host). Idempotent: returns nil if the
// client already exists (409 Conflict).
func RegisterCLIClient(ctx context.Context, hydraAdminURL string, jwkData []byte, tlsCfg *tls.Config) error {
	return registerHydraClient(ctx, hydraAdminURL, consts.ClientIDCLI, string(adminv1.ScopeAdmin), jwkData, tlsCfg)
}

// RegisterAgentClient registers the clawker-agent OAuth2 client with
// Hydra via the admin API. Both clawker-cli and clawker-agent use the
// same public JWK (the CLI's signing key) — distinct client IDs keep
// the scope surface clean even though the signing key is shared.
// Idempotent: returns nil on 409 Conflict.
func RegisterAgentClient(ctx context.Context, hydraAdminURL string, jwkData []byte, tlsCfg *tls.Config) error {
	return registerHydraClient(ctx, hydraAdminURL, consts.ClientIDAgent, string(agentv1.ScopeSelfRegister), jwkData, tlsCfg)
}

// registerHydraClient is the shared implementation; public callers
// differ only in client_id and scope.
func registerHydraClient(ctx context.Context, hydraAdminURL, clientID, scope string, jwkData []byte, tlsCfg *tls.Config) error {
	var jwks json.RawMessage
	if err := json.Unmarshal(jwkData, &jwks); err != nil {
		return fmt.Errorf("parse %s JWK data: %w", clientID, err)
	}
	jwks, err := ensureJWKS(jwks)
	if err != nil {
		return fmt.Errorf("normalize %s JWK: %w", clientID, err)
	}

	body := map[string]any{
		"client_id":                       clientID,
		"grant_types":                     []string{consts.GrantTypeClientCredentials},
		"token_endpoint_auth_method":      "private_key_jwt",
		"token_endpoint_auth_signing_alg": "ES256",
		"scope":                           scope,
		"jwks":                            jwks,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal %s registration: %w", clientID, err)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   tlsCfg,
			ForceAttemptHTTP2: true,
		},
	}

	url := hydraAdminURL + "/admin/clients"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build %s registration request: %w", clientID, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("hydra admin POST /admin/clients (%s): %w", clientID, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read hydra response (%s): %w", clientID, err)
	}

	switch resp.StatusCode {
	case http.StatusCreated:
		// Hydra v2 documents 201 only. 200 is intentionally not accepted
		// — a misconfigured proxy or future Hydra change returning 200
		// with an empty body would otherwise mark registration successful
		// while the client never lands.
		return nil
	case http.StatusConflict:
		return nil
	default:
		return fmt.Errorf("hydra admin returned %d for %s: %s", resp.StatusCode, clientID, string(respBody))
	}
}

// ensureJWKS wraps a bare JWK object in a JWKS envelope if needed.
// If the input already has a "keys" field, it's returned as-is.
func ensureJWKS(data json.RawMessage) (json.RawMessage, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	if _, ok := probe["keys"]; ok {
		return data, nil // Already a JWKS.
	}
	// Bare JWK — wrap it.
	wrapped := map[string]any{"keys": []json.RawMessage{data}}
	return json.Marshal(wrapped)
}
