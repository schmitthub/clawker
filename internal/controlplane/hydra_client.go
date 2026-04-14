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

	"github.com/schmitthub/clawker/internal/consts"
)

// RegisterCLIClient registers the clawker-cli OAuth2 client with Hydra
// via the admin API. The jwkData is the raw JSON of the CLI's public
// JWKS (bind-mounted from the host). Idempotent: returns nil if the
// client already exists (409 Conflict).
func RegisterCLIClient(ctx context.Context, hydraAdminURL string, jwkData []byte, tlsCfg *tls.Config) error {
	// Parse the JWK data to embed as the jwks field.
	var jwks json.RawMessage
	if err := json.Unmarshal(jwkData, &jwks); err != nil {
		return fmt.Errorf("parse CLI JWK data: %w", err)
	}

	// Wrap single JWK in a JWKS if needed.
	jwks, err := ensureJWKS(jwks)
	if err != nil {
		return fmt.Errorf("normalize CLI JWK: %w", err)
	}

	body := map[string]any{
		"client_id":                       consts.ClientIDCLI,
		"grant_types":                     []string{"client_credentials"},
		"token_endpoint_auth_method":      "private_key_jwt",
		"token_endpoint_auth_signing_alg": "ES256",
		"scope":                           consts.ScopeAdmin,
		"jwks":                            jwks,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal client registration: %w", err)
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
		return fmt.Errorf("build registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("hydra admin POST /admin/clients: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read hydra response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusConflict:
		// Client already exists — idempotent success.
		return nil
	default:
		return fmt.Errorf("hydra admin returned %d: %s", resp.StatusCode, string(respBody))
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
