# Plan: Configurable IP Range Sources for Firewall

## Problem Statement

The current firewall implementation resolves domains to IPs via DNS at container startup. This works for simple domains but fails for cloud providers like Google that use hundreds of IPs across load balancers. A `dig` lookup only returns a subset of IPs.

**Example failure:**
```
storage.googleapis.com/proxy-golang-org-prod/... dial tcp 142.251.34.91:443: no route to host
```

The domain `storage.googleapis.com` IS configured in `add_domains`, but DNS resolution didn't capture all Google IPs.

## Current Architecture

### Config Schema (`internal/config/schema.go:107-163`)
```go
type FirewallConfig struct {
    Enable          bool     `yaml:"enable"`
    AddDomains      []string `yaml:"add_domains,omitempty"`
    RemoveDomains   []string `yaml:"remove_domains,omitempty"`
    OverrideDomains []string `yaml:"override_domains,omitempty"`
}
```

### Data Flow
```
clawker.yaml → config.FirewallConfig → RuntimeEnvOpts → docker.RuntimeEnv()
    → CLAWKER_FIREWALL_DOMAINS env var → init-firewall.sh
```

### Current IP Range Handling (`init-firewall.sh:50-85`)
- GitHub: Fetches CIDR ranges from `api.github.com/meta` (hardcoded, comprehensive)
- Other domains: DNS resolution only (incomplete for cloud providers)

## Key Learnings

### 1. Cloud Provider IP Range APIs

| Provider | URL | Format | Notes |
|----------|-----|--------|-------|
| GitHub | `https://api.github.com/meta` | JSON with `.web`, `.api`, `.git`, etc. arrays | Already implemented |
| Google Cloud | `https://www.gstatic.com/ipranges/cloud.json` | JSON `{prefixes: [{ipv4Prefix, service, scope}]}` | GCS, GCE, etc. |
| Google (all) | `https://www.gstatic.com/ipranges/goog.json` | JSON `{prefixes: [{ipv4Prefix}]}` | All Google services |
| Cloudflare | `https://api.cloudflare.com/client/v4/ips` | JSON `{result: {ipv4_cidrs, ipv6_cidrs}}` | CDN IPs |
| AWS | `https://ip-ranges.amazonaws.com/ip-ranges.json` | JSON `{prefixes: [{ip_prefix, service, region}]}` | Can filter by service |
| Azure | `https://www.microsoft.com/en-us/download/details.aspx?id=56519` | Weekly JSON download | More complex |

### 2. Why DNS Resolution Fails for Cloud Providers
- Google, AWS, Cloudflare use anycast and geographic load balancing
- A single DNS query returns only 2-4 IPs from hundreds available
- IPs rotate frequently based on load and geography
- CIDR range APIs provide the complete, authoritative list

### 3. Design Principles
- **Backward compatible**: Default behavior unchanged (`["github"]` implicit)
- **Explicit over implicit**: Users opt-in to additional sources
- **Fail-safe**: Missing source = warning, not hard failure (except GitHub which is critical)
- **Extensible**: Easy to add new providers without schema changes

## Proposed Solution

### 1. Config Schema Changes (`internal/config/schema.go`)

```go
type FirewallConfig struct {
    Enable          bool              `yaml:"enable"`
    AddDomains      []string          `yaml:"add_domains,omitempty"`
    RemoveDomains   []string          `yaml:"remove_domains,omitempty"`
    OverrideDomains []string          `yaml:"override_domains,omitempty"`
    IPRangeSources  []IPRangeSource   `yaml:"ip_range_sources,omitempty"`  // NEW
}

// IPRangeSource defines a source of IP CIDR ranges
type IPRangeSource struct {
    // Name is the identifier (e.g., "github", "google-cloud", "cloudflare")
    Name string `yaml:"name"`
    // URL is optional custom URL (uses built-in URL if empty)
    URL string `yaml:"url,omitempty"`
    // JQFilter extracts CIDR arrays from JSON response (optional, uses built-in if empty)
    JQFilter string `yaml:"jq_filter,omitempty"`
    // Required determines if failure to fetch is fatal (default: false for custom, true for github)
    Required bool `yaml:"required,omitempty"`
}
```

### 2. Built-in Source Registry

```go
var BuiltinIPRangeSources = map[string]IPRangeSourceConfig{
    "github": {
        URL:      "https://api.github.com/meta",
        JQFilter: "(.web + .api + .git + .copilot + .packages + .pages + .importer + .actions)[]",
        Required: true,
    },
    "google-cloud": {
        URL:      "https://www.gstatic.com/ipranges/cloud.json",
        JQFilter: ".prefixes[].ipv4Prefix // empty",
        Required: false,
    },
    "google": {
        URL:      "https://www.gstatic.com/ipranges/goog.json",
        JQFilter: ".prefixes[].ipv4Prefix // empty",
        Required: false,
    },
    "cloudflare": {
        URL:      "https://api.cloudflare.com/client/v4/ips",
        JQFilter: ".result.ipv4_cidrs[]",
        Required: false,
    },
    "aws": {
        URL:      "https://ip-ranges.amazonaws.com/ip-ranges.json",
        JQFilter: ".prefixes[].ip_prefix",
        Required: false,
    },
}
```

### 3. Default Behavior

```go
func (c *FirewallConfig) GetIPRangeSources() []IPRangeSource {
    if len(c.IPRangeSources) == 0 {
        // Backward compatible: default to GitHub only
        return []IPRangeSource{{Name: "github"}}
    }
    return c.IPRangeSources
}
```

### 4. YAML Examples

**Default (unchanged behavior):**
```yaml
security:
  firewall:
    enable: true
    # ip_range_sources defaults to ["github"]
```

**Add Google Cloud for Go proxy:**
```yaml
security:
  firewall:
    enable: true
    ip_range_sources:
      - name: github
      - name: google-cloud  # Adds GCS, proxy.golang.org backend
```

**Custom source:**
```yaml
security:
  firewall:
    enable: true
    ip_range_sources:
      - name: github
      - name: internal-registry
        url: "https://internal.example.com/ip-ranges.json"
        jq_filter: ".allowed_cidrs[]"
        required: true
```

**Disable all IP range sources (domains only):**
```yaml
security:
  firewall:
    enable: true
    ip_range_sources: []  # Empty list = no IP range fetching
```

### 5. Environment Variable Changes

```go
// RuntimeEnvOpts (internal/docker/env.go)
type RuntimeEnvOpts struct {
    // ... existing fields ...
    FirewallIPRangeSources []IPRangeSource  // NEW: serialized as JSON
}
```

Produces: `CLAWKER_FIREWALL_IP_RANGE_SOURCES='[{"name":"github"},{"name":"google-cloud"}]'`

### 6. init-firewall.sh Changes

Replace hardcoded GitHub block (lines 50-85) with generic loop:

```bash
# Read IP range sources from environment (JSON array)
IP_RANGE_SOURCES="${CLAWKER_FIREWALL_IP_RANGE_SOURCES:-}"

# Built-in source configurations
declare -A BUILTIN_URLS=(
    ["github"]="https://api.github.com/meta"
    ["google-cloud"]="https://www.gstatic.com/ipranges/cloud.json"
    ["google"]="https://www.gstatic.com/ipranges/goog.json"
    ["cloudflare"]="https://api.cloudflare.com/client/v4/ips"
    ["aws"]="https://ip-ranges.amazonaws.com/ip-ranges.json"
)

declare -A BUILTIN_FILTERS=(
    ["github"]='(.web + .api + .git + .copilot + .packages + .pages + .importer + .actions)[]'
    ["google-cloud"]='.prefixes[].ipv4Prefix // empty'
    ["google"]='.prefixes[].ipv4Prefix // empty'
    ["cloudflare"]='.result.ipv4_cidrs[]'
    ["aws"]='.prefixes[].ip_prefix'
)

if [ -n "$IP_RANGE_SOURCES" ] && [ "$IP_RANGE_SOURCES" != "[]" ]; then
    echo "$IP_RANGE_SOURCES" | jq -c '.[]' | while read -r source; do
        name=$(echo "$source" | jq -r '.name')
        url=$(echo "$source" | jq -r '.url // empty')
        jq_filter=$(echo "$source" | jq -r '.jq_filter // empty')
        required=$(echo "$source" | jq -r '.required // false')
        
        # Use built-in URL/filter if not specified
        [ -z "$url" ] && url="${BUILTIN_URLS[$name]:-}"
        [ -z "$jq_filter" ] && jq_filter="${BUILTIN_FILTERS[$name]:-}"
        
        if [ -z "$url" ]; then
            echo "WARNING: Unknown IP range source '$name' with no URL"
            continue
        fi
        
        echo "Fetching IP ranges from $name ($url)..."
        response=$(curl -s --connect-timeout 10 "$url")
        
        if [ -z "$response" ]; then
            if [ "$required" = "true" ]; then
                echo "ERROR: Failed to fetch required IP ranges from $name"
                exit 1
            else
                echo "WARNING: Failed to fetch IP ranges from $name, skipping"
                continue
            fi
        fi
        
        # Extract and add CIDRs
        echo "$response" | jq -r "$jq_filter" | while read -r cidr; do
            # Skip IPv6
            [[ "$cidr" =~ : ]] && continue
            # Skip empty
            [ -z "$cidr" ] && continue
            # Validate and add
            if [[ "$cidr" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+(/[0-9]+)?$ ]]; then
                echo "Adding $name range: $cidr"
                ipset add allowed-domains "$cidr" -exist 2>/dev/null || true
            fi
        done
    done
fi
```

## Implementation Plan

### Phase 1: Config Schema (internal/config/)
1. Add `IPRangeSource` struct to `schema.go`
2. Add `IPRangeSources` field to `FirewallConfig`
3. Add `GetIPRangeSources()` method with backward-compatible default
4. Add `BuiltinIPRangeSources` registry to new file `ip_ranges.go`
5. Update `defaults.go` with example YAML
6. Add validation in `validation.go`

### Phase 2: Environment Wiring (internal/docker/)
1. Add `FirewallIPRangeSources` to `RuntimeEnvOpts`
2. Update `RuntimeEnv()` to serialize sources as JSON env var
3. Update tests in `env_test.go`

### Phase 3: Container Script (internal/bundler/assets/)
1. Refactor `init-firewall.sh` to use source loop
2. Add built-in source registry (URLs + jq filters)
3. Handle required vs optional sources
4. Update `firewall_test.go`

### Phase 4: Command Wiring (internal/cmd/container/)
1. Update `create.go` to pass IP range sources to RuntimeEnvOpts
2. Update `run.go` similarly
3. Test end-to-end

### Phase 5: Documentation
1. Update `CLAUDE.md` with new config options
2. Update `internal/config/CLAUDE.md`
3. Add example to templates/

## Files to Modify

| File | Changes |
|------|---------|
| `internal/config/schema.go` | Add `IPRangeSource`, update `FirewallConfig` |
| `internal/config/ip_ranges.go` | NEW: Built-in source registry |
| `internal/config/defaults.go` | Update example YAML |
| `internal/config/validation.go` | Add source validation |
| `internal/docker/env.go` | Add to `RuntimeEnvOpts`, serialize |
| `internal/docker/env_test.go` | Add tests |
| `internal/bundler/assets/init-firewall.sh` | Refactor to use source loop |
| `internal/bundler/firewall_test.go` | Update tests |
| `internal/cmd/container/create/create.go` | Wire sources to opts |
| `internal/cmd/container/run/run.go` | Wire sources to opts |
| `CLAUDE.md` | Document new feature |

## Testing Strategy

1. **Unit tests**: Config parsing, serialization, defaults
2. **Script tests**: Bash syntax validation, jq filter correctness
3. **Integration tests** (`test/cli/`): Full flow with mock HTTP server
4. **Manual testing**: Build container with `google-cloud` source, verify Go proxy works

## Verification

After implementation:
```bash
# Build with google-cloud source
clawker build

# Run and test Go proxy access
clawker run -- go mod download github.com/klauspost/compress@latest

# Should succeed (storage.googleapis.com now allowed via CIDR ranges)
```

## Open Questions

1. **Caching**: Should we cache IP ranges to avoid fetching on every container start?
   - Pro: Faster startup, works offline after first fetch
   - Con: Stale ranges if provider updates IPs
   - Recommendation: No caching initially, consider later as optimization

2. **Aggregation**: Should we use `aggregate` tool on fetched CIDRs?
   - Currently used for GitHub ranges
   - Reduces ipset size but adds dependency
   - Recommendation: Yes, for consistency with current approach

3. **IPv6**: Currently skipped entirely
   - Google/AWS provide IPv6 ranges
   - ip6tables support exists but is secondary
   - Recommendation: Keep IPv4-only for v1, add IPv6 later
