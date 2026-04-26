package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
)

func TestRegisterCLIClient_PayloadShape(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/admin/clients" {
			t.Errorf("expected /admin/clients, got %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	jwk := `{"kty":"EC","crv":"P-256","x":"test","y":"test"}`
	err := RegisterCLIClient(context.Background(), srv.URL, []byte(jwk), nil)
	if err != nil {
		t.Fatalf("RegisterCLIClient: %v", err)
	}

	// Assert client_id.
	if id, _ := captured["client_id"].(string); id != consts.ClientIDCLI {
		t.Errorf("client_id = %q, want %q", id, consts.ClientIDCLI)
	}

	// Assert grant_types.
	gts, ok := captured["grant_types"].([]any)
	if !ok || len(gts) != 1 || gts[0] != "client_credentials" {
		t.Errorf("grant_types = %v, want [client_credentials]", captured["grant_types"])
	}

	// Assert auth method + signing alg.
	if v, _ := captured["token_endpoint_auth_method"].(string); v != "private_key_jwt" {
		t.Errorf("token_endpoint_auth_method = %q, want private_key_jwt", v)
	}
	if v, _ := captured["token_endpoint_auth_signing_alg"].(string); v != "ES256" {
		t.Errorf("token_endpoint_auth_signing_alg = %q, want ES256", v)
	}

	// Assert scope.
	if v, _ := captured["scope"].(string); v != consts.ScopeAdmin {
		t.Errorf("scope = %q, want %q", v, consts.ScopeAdmin)
	}

	// Assert jwks is wrapped.
	jwksRaw, ok := captured["jwks"].(map[string]any)
	if !ok {
		t.Fatalf("jwks not an object: %T", captured["jwks"])
	}
	keys, ok := jwksRaw["keys"].([]any)
	if !ok || len(keys) != 1 {
		t.Errorf("jwks.keys = %v, want array with 1 key", jwksRaw["keys"])
	}
}

func TestRegisterCLIClient_ConflictIdempotent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"client already exists"}`))
	}))
	defer srv.Close()

	jwk := `{"kty":"EC","crv":"P-256","x":"test","y":"test"}`
	err := RegisterCLIClient(context.Background(), srv.URL, []byte(jwk), nil)
	if err != nil {
		t.Errorf("expected nil error for 409 Conflict, got: %v", err)
	}
}

func TestRegisterCLIClient_ErrorResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer srv.Close()

	jwk := `{"kty":"EC","crv":"P-256","x":"test","y":"test"}`
	err := RegisterCLIClient(context.Background(), srv.URL, []byte(jwk), nil)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "500") {
		t.Errorf("error should mention status 500: %s", got)
	}
}

// RegisterAgentClient shares the registerHydraClient helper with
// RegisterCLIClient. The CLI-specific tests above cover the transport
// + idempotency contract; here we only assert what's distinctly the
// agent's: the client_id and scope written into the registration body.
func TestRegisterAgentClient_PayloadShape(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	jwk := `{"kty":"EC","crv":"P-256","x":"test","y":"test"}`
	if err := RegisterAgentClient(context.Background(), srv.URL, []byte(jwk), nil); err != nil {
		t.Fatalf("RegisterAgentClient: %v", err)
	}

	if id, _ := captured["client_id"].(string); id != consts.ClientIDAgent {
		t.Errorf("client_id = %q, want %q", id, consts.ClientIDAgent)
	}
	if v, _ := captured["scope"].(string); v != consts.ScopeAgentSelfRegister {
		t.Errorf("scope = %q, want %q", v, consts.ScopeAgentSelfRegister)
	}
}

func TestEnsureJWKS_WrapsBareJWK(t *testing.T) {
	t.Parallel()

	bare := json.RawMessage(`{"kty":"EC","crv":"P-256","x":"a","y":"b"}`)
	result, err := ensureJWKS(bare)
	if err != nil {
		t.Fatalf("ensureJWKS: %v", err)
	}

	var parsed struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(parsed.Keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(parsed.Keys))
	}
	// The original key fields must round-trip into keys[0] — otherwise
	// ensureJWKS produced a JWKS with the right shape but lost the
	// caller's key material. A regression returning {"keys":[{}]} would
	// have passed the prior keys-field-only assertion.
	want := map[string]string{"kty": "EC", "crv": "P-256", "x": "a", "y": "b"}
	for k, v := range want {
		if got := parsed.Keys[0][k]; got != v {
			t.Errorf("keys[0].%s = %q, want %q", k, got, v)
		}
	}
}

func TestEnsureJWKS_PassthroughExisting(t *testing.T) {
	t.Parallel()

	existing := json.RawMessage(`{"keys":[{"kty":"EC","crv":"P-256","x":"a","y":"b"}]}`)
	result, err := ensureJWKS(existing)
	if err != nil {
		t.Fatalf("ensureJWKS: %v", err)
	}
	// Contract: an already-wrapped JWKS round-trips byte-for-byte. A
	// regression that re-wrapped (producing {"keys":[{"keys":[…]}]}) or
	// returned {"keys":null} would have passed the prior keys-field-only
	// assertion; bytes.Equal pins it.
	if !bytes.Equal(existing, result) {
		t.Errorf("passthrough must round-trip byte-for-byte\n  in:  %s\n  out: %s", existing, result)
	}
}
