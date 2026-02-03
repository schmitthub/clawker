# Credentials Package

Environment variable management, `.env` file parsing, and OTEL credential forwarding for containers.

## Files

| File | Purpose |
|------|---------|
| `env.go` | `EnvBuilder` — allowlist/denylist env var builder |
| `dotenv.go` | `.env` file parsing, sensitive key detection |
| `otel.go` | OTEL environment variable forwarding |

## EnvBuilder

Builder pattern for constructing container environment variables with security filtering.

```go
type EnvBuilder struct {
    vars      map[string]string
    allowList []string  // glob patterns for host passthrough
    denyList  []string  // glob patterns to block
}

func NewEnvBuilder() *EnvBuilder
```

### Builder Methods

```go
(*EnvBuilder).Set(key, value string)              // Set explicit var
(*EnvBuilder).SetAll(vars map[string]string)      // Set multiple vars
(*EnvBuilder).SetFromHost(key string)             // Copy single var from host env
(*EnvBuilder).SetFromHostAll(keys []string)       // Copy multiple vars from host env
(*EnvBuilder).LoadDotEnv(path string) error       // Load from .env file
(*EnvBuilder).AllowFromHost(pattern string)       // Allow glob pattern from host
(*EnvBuilder).Deny(pattern string)                // Block specific var pattern
(*EnvBuilder).PassthroughFromHost()               // Pass all allowed host vars (filtered by deny list)
```

### Output Methods

```go
(*EnvBuilder).Build() []string                    // Returns []string{"KEY=value", ...}
(*EnvBuilder).BuildMap() map[string]string        // Returns map[string]string
(*EnvBuilder).Count() int                         // Number of vars set
```

### Security Filtering

Default deny list (`defaultDenyList()`) blocks sensitive keys: AWS credentials, GitHub tokens, SSH keys, Docker auth, cloud provider secrets, etc. `isAllowed()`/`isDenied()` use glob matching against the allow/deny lists.

### DefaultPassthrough

```go
func DefaultPassthrough() []string  // Returns env var names that should be passed to containers
```

Returns a curated list of safe-to-forward environment variables (e.g., `TERM`, `LANG`, `TZ`, etc.).

## Dotenv Parsing

```go
func LoadDotEnv(path string) (map[string]string, error)  // Parse .env file
func FindDotEnvFiles(dir string) []string                  // Find .env files in directory
```

Supports quoted values, comments, `export` prefix. Internal `isSensitiveKey()` detects credential-like keys. `parseEnvLine()` and `unquote()` handle line-level parsing.

## OTEL Forwarding

```go
func OtelEnvVars(containerName string) map[string]string           // OTEL env vars from host
func OtelEnvVarsWithPrompts(containerName string) map[string]string // OTEL vars including prompt config
```

Forwards OpenTelemetry configuration to containers. `WithPrompts` variant includes additional prompt-related OTEL vars.
