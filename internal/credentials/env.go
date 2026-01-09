package credentials

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schmitthub/claucker/pkg/logger"
)

// EnvBuilder builds environment variables for container execution
type EnvBuilder struct {
	vars      map[string]string
	allowList []string
	denyList  []string
}

// NewEnvBuilder creates a new environment builder
func NewEnvBuilder() *EnvBuilder {
	return &EnvBuilder{
		vars:      make(map[string]string),
		allowList: []string{},
		denyList:  defaultDenyList(),
	}
}

// defaultDenyList returns environment variables that should never be passed
func defaultDenyList() []string {
	return []string{
		// Shell/terminal variables
		"PS1", "PS2", "PS3", "PS4",
		"PROMPT_COMMAND",
		"BASH_ENV",
		"ENV",
		"SHELLOPTS",

		// SSH/GPG
		"SSH_AUTH_SOCK",
		"SSH_AGENT_PID",
		"GPG_AGENT_INFO",
		"GPG_TTY",

		// Display/desktop
		"DISPLAY",
		"XAUTHORITY",
		"XDG_SESSION_ID",
		"XDG_RUNTIME_DIR",

		// System paths (will be set by container)
		"HOME",
		"USER",
		"LOGNAME",
		"PWD",
		"OLDPWD",

		// Claucker internal
		"CLAUCKER_",
	}
}

// Set sets an environment variable
func (b *EnvBuilder) Set(key, value string) *EnvBuilder {
	b.vars[key] = value
	return b
}

// SetAll sets multiple environment variables from a map
func (b *EnvBuilder) SetAll(vars map[string]string) *EnvBuilder {
	for k, v := range vars {
		b.vars[k] = v
	}
	return b
}

// SetFromHost copies an environment variable from the host if it exists
func (b *EnvBuilder) SetFromHost(key string) *EnvBuilder {
	if value, exists := os.LookupEnv(key); exists {
		b.vars[key] = value
	}
	return b
}

// SetFromHostAll copies multiple environment variables from the host
func (b *EnvBuilder) SetFromHostAll(keys []string) *EnvBuilder {
	for _, key := range keys {
		b.SetFromHost(key)
	}
	return b
}

// LoadDotEnv loads variables from a .env file
func (b *EnvBuilder) LoadDotEnv(path string) error {
	vars, err := LoadDotEnv(path)
	if err != nil {
		return err
	}
	b.SetAll(vars)
	return nil
}

// AllowFromHost adds keys to the allowlist for host passthrough
func (b *EnvBuilder) AllowFromHost(keys ...string) *EnvBuilder {
	b.allowList = append(b.allowList, keys...)
	return b
}

// Deny adds keys to the denylist
func (b *EnvBuilder) Deny(keys ...string) *EnvBuilder {
	b.denyList = append(b.denyList, keys...)
	return b
}

// PassthroughFromHost copies allowed environment variables from the host
func (b *EnvBuilder) PassthroughFromHost() *EnvBuilder {
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]

		// Check denylist
		if b.isDenied(key) {
			continue
		}

		// Check allowlist (if specified) or allow all
		if len(b.allowList) > 0 && !b.isAllowed(key) {
			continue
		}

		// Don't overwrite existing vars
		if _, exists := b.vars[key]; !exists {
			b.vars[key] = parts[1]
		}
	}
	return b
}

// isAllowed checks if a key is in the allowlist
func (b *EnvBuilder) isAllowed(key string) bool {
	for _, allowed := range b.allowList {
		if strings.HasPrefix(key, allowed) || key == allowed {
			return true
		}
	}
	return false
}

// isDenied checks if a key is in the denylist
func (b *EnvBuilder) isDenied(key string) bool {
	for _, denied := range b.denyList {
		if strings.HasPrefix(key, denied) || key == denied {
			return true
		}
	}
	return false
}

// Build returns the environment variables as a slice of KEY=value strings
func (b *EnvBuilder) Build() []string {
	// Sort keys for deterministic output
	keys := make([]string, 0, len(b.vars))
	for k := range b.vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, k := range keys {
		// Skip denied keys
		if b.isDenied(k) {
			logger.Debug().
				Str("key", k).
				Msg("skipping denied environment variable")
			continue
		}

		result = append(result, fmt.Sprintf("%s=%s", k, b.vars[k]))
	}

	return result
}

// BuildMap returns the environment variables as a map
func (b *EnvBuilder) BuildMap() map[string]string {
	result := make(map[string]string)
	for k, v := range b.vars {
		if b.isDenied(k) {
			continue
		}
		result[k] = v
	}
	return result
}

// Count returns the number of environment variables
func (b *EnvBuilder) Count() int {
	return len(b.vars)
}

// DefaultPassthrough returns environment variables that should be passed to containers
func DefaultPassthrough() []string {
	return []string{
		// Useful development variables
		"TZ",
		"LANG",
		"LANGUAGE",
		"LC_ALL",
		"TERM",
		"COLORTERM",

		// Editor preferences
		"EDITOR",
		"VISUAL",

		// Git configuration (non-sensitive)
		"GIT_AUTHOR_NAME",
		"GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME",
		"GIT_COMMITTER_EMAIL",

		// Node.js
		"NODE_ENV",
		"NODE_OPTIONS",
		"NPM_CONFIG_REGISTRY",

		// Python
		"PYTHONPATH",
		"PYTHONDONTWRITEBYTECODE",
		"PYTHONUNBUFFERED",

		// Go
		"GOPROXY",
		"GOFLAGS",

		// HTTP proxies
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"http_proxy",
		"https_proxy",
		"no_proxy",

		// Claude/Anthropic authentication
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_CUSTOM_HEADERS",
	}
}
