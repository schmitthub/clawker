# Credentials Package

Environment variable management, `.env` file parsing, and OTEL credential forwarding for containers.

## Key Files

| File | Purpose |
|------|---------|
| `env.go` | `EnvBuilder` — allowlist/denylist env var builder |
| `dotenv.go` | `.env` file parsing, sensitive key detection |
| `otel.go` | OTEL environment variable forwarding |

## EnvBuilder (`env.go`)

Builder pattern for constructing container environment variables with security filtering.

```go
b := credentials.NewEnvBuilder()
b.Set("KEY", "value")              // Set explicit var
b.SetAll(map[string]string{...})   // Set multiple vars
b.SetFromHost("PATH")              // Copy single var from host
b.SetFromHostAll([]string{...})    // Copy multiple from host
b.LoadDotEnv(path)                 // Load from .env file
b.AllowFromHost("CUSTOM_*")       // Allow pattern from host
b.Deny("SECRET_KEY")              // Block specific var
b.PassthroughFromHost()            // Pass allowed host vars
env := b.Build()                   // Returns []string{"KEY=value", ...}
envMap := b.BuildMap()             // Returns map[string]string
```

Default deny list blocks sensitive keys (AWS, GitHub tokens, etc.).

## Dotenv Parsing (`dotenv.go`)

```go
func LoadDotEnv(path string) (map[string]string, error)  // Parse .env file
func FindDotEnvFiles(dir string) ([]string, error)        // Find .env files in directory
```

Supports quoted values, comments, and `export` prefix. `isSensitiveKey()` detects credential-like keys.

## OTEL Forwarding (`otel.go`)

```go
func OtelEnvVars() map[string]string              // OTEL env vars from host
func OtelEnvVarsWithPrompts() map[string]string    // OTEL vars including prompt config
```
