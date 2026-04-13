package controlplane

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
)

const oryConfigDir = "/etc/clawker"

// buildHydraConfig generates the Hydra v26.2.0 config YAML for the CP's
// local auth stack. Ports come from ControlPlaneSettings (settings.yaml).
//
// Validated against the spec/config.json schema and smoke-tested with:
//
//	hydra serve all --config /etc/clawker/hydra.yaml --dev --sqa-opt-out
//
// Key config decisions:
//   - In-memory DSN: no external DB needed, state is ephemeral per CP restart.
//   - JWT access tokens: the CP's AuthInterceptor verifies tokens via Hydra's
//     /admin/oauth2/introspect endpoint (RFC 7662).
//   - System secret: ≥16 chars, encryption of auth codes/refresh tokens.
//     Safe as a static value because the in-memory store is ephemeral.
//   - Admin on 127.0.0.1: only reachable from within the CP container.
//   - Public on 0.0.0.0: published to host for CLI token requests.
//   - TTLs: 1h access token (matches tokenRefreshMargin=30s in cp_dial.go).
//   - expose_internal_errors: true for --dev mode debugging.
//   - TLS: uses the same self-signed cert as the gRPC admin API.
//
// Log level enum (Hydra v26.2.0): panic, fatal, error, warn, info, debug, trace.
func buildHydraConfig(cp config.ControlPlaneSettings, hydraSecret string) string {
	return fmt.Sprintf(`dsn: memory
serve:
  public:
    host: 0.0.0.0
    port: %d
  admin:
    host: 127.0.0.1
    port: %d
  tls:
    enabled: true
    cert:
      path: /etc/clawker/tls/server.pem
    key:
      path: /etc/clawker/tls/server.key
strategies:
  access_token: jwt
secrets:
  system:
    - %s
urls:
  self:
    issuer: https://127.0.0.1:%d/
oauth2:
  expose_internal_errors: true
ttl:
  access_token: 1h
  auth_code: 10m
log:
  level: warn
  format: json
`, cp.HydraPublicPort, cp.HydraAdminPort, hydraSecret, cp.HydraPublicPort)
}

// buildKratosConfig generates the Kratos v26.2.0 config YAML.
//
// Validated against the embedx/config.schema.json schema and smoke-tested with:
//
//	kratos serve --config /etc/clawker/kratos.yaml --dev --sqa-opt-out
//
// Kratos is not actively used in v1 (no user registration/login flows) but must
// be running for the Ory stack to be fully operational. It will be used when the
// webui is added (user accounts, session management).
//
// Required fields per schema: identity (with schema that has traits with ≥1 prop),
// dsn, selfservice (with default_browser_return_url).
//
// Log level enum (Kratos v26.2.0): trace, debug, info, warning, error, fatal, panic.
// Note: Kratos uses "warning" NOT "warn" — different from Hydra/Oathkeeper.
func buildKratosConfig(cp config.ControlPlaneSettings) string {
	return fmt.Sprintf(`version: v26.2.0
dsn: memory
serve:
  public:
    host: 127.0.0.1
    port: %d
    tls:
      cert:
        path: /etc/clawker/tls/server.pem
      key:
        path: /etc/clawker/tls/server.key
  admin:
    host: 127.0.0.1
    port: %d
    tls:
      cert:
        path: /etc/clawker/tls/server.pem
      key:
        path: /etc/clawker/tls/server.key
selfservice:
  default_browser_return_url: https://127.0.0.1:4455/
identity:
  default_schema_id: default
  schemas:
    - id: default
      url: base64://eyJ0eXBlIjoib2JqZWN0IiwicHJvcGVydGllcyI6eyJ0cmFpdHMiOnsidHlwZSI6Im9iamVjdCIsInByb3BlcnRpZXMiOnsiZW1haWwiOnsidHlwZSI6InN0cmluZyIsImZvcm1hdCI6ImVtYWlsIiwidGl0bGUiOiJFbWFpbCJ9fX19fQ==
log:
  level: warning
  format: json
`, cp.KratosPublicPort, cp.KratosAdminPort)
}

// buildOathkeeperConfig generates the Oathkeeper v26.2.0 config YAML.
//
// Validated against the internal/config/.oathkeeper.yaml reference and smoke-tested with:
//
//	oathkeeper serve --config /etc/clawker/oathkeeper.yaml
//
// Oathkeeper serves as the HTTP reverse proxy for future webui auth. gRPC auth
// (CLI + agents) bypasses Oathkeeper entirely — it uses direct Hydra token
// introspection via AuthInterceptor. Not actively routing traffic in v1.
//
// The error handler config is required in v26.2.0 — without it Oathkeeper has
// no way to render errors. JSON fallback ensures API-style error responses.
//
// Log level enum (Oathkeeper v26.2.0): panic, fatal, error, warn, info, debug, trace.
// Note: Oathkeeper uses "warn" like Hydra, NOT "warning" like Kratos.
func buildOathkeeperConfig(cp config.ControlPlaneSettings) string {
	return fmt.Sprintf(`serve:
  proxy:
    host: 0.0.0.0
    port: %d
    tls:
      cert:
        path: /etc/clawker/tls/server.pem
      key:
        path: /etc/clawker/tls/server.key
  api:
    host: 127.0.0.1
    port: %d
    tls:
      cert:
        path: /etc/clawker/tls/server.pem
      key:
        path: /etc/clawker/tls/server.key
access_rules:
  matching_strategy: regexp
  repositories:
    - inline://W10=
authenticators:
  noop:
    enabled: true
authorizers:
  allow:
    enabled: true
mutators:
  noop:
    enabled: true
errors:
  fallback:
    - json
  handlers:
    json:
      enabled: true
      config:
        verbose: true
log:
  level: warn
  format: json
`, cp.OathkeeperPort, cp.OathkeeperAPIPort)
}

// WriteOryConfigs writes config files for Hydra, Kratos, and Oathkeeper to
// the config directory. Ports are read from ControlPlaneSettings. Called by
// the CP binary at startup before launching subprocesses. Idempotent —
// overwrites on every start so configs stay in sync with the binary version.
func WriteOryConfigs(cp config.ControlPlaneSettings, hydraSecret string) error {
	if err := os.MkdirAll(oryConfigDir, 0o755); err != nil {
		return fmt.Errorf("create ory config dir: %w", err)
	}

	configs := map[string]string{
		"hydra.yaml":      buildHydraConfig(cp, hydraSecret),
		"kratos.yaml":     buildKratosConfig(cp),
		"oathkeeper.yaml": buildOathkeeperConfig(cp),
	}

	for name, content := range configs {
		path := filepath.Join(oryConfigDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}
