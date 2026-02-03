package config

// BuiltinIPRangeConfig holds the configuration for a built-in IP range source.
type BuiltinIPRangeConfig struct {
	URL      string
	JQFilter string
}

// BuiltinIPRangeSources maps known source names to their API URLs and jq filters.
// These sources provide authoritative IP CIDR ranges for cloud providers.
var BuiltinIPRangeSources = map[string]BuiltinIPRangeConfig{
	"github": {
		URL:      "https://api.github.com/meta",
		JQFilter: `(.web + .api + .git + .copilot + .packages + .pages + .importer + .actions)[]`,
	},
	"google-cloud": {
		URL:      "https://www.gstatic.com/ipranges/cloud.json",
		JQFilter: `.prefixes[].ipv4Prefix // empty`,
	},
	"google": {
		URL:      "https://www.gstatic.com/ipranges/goog.json",
		JQFilter: `.prefixes[].ipv4Prefix // empty`,
	},
	"cloudflare": {
		URL:      "https://api.cloudflare.com/client/v4/ips",
		JQFilter: `.result.ipv4_cidrs[]`,
	},
	"aws": {
		URL:      "https://ip-ranges.amazonaws.com/ip-ranges.json",
		JQFilter: `.prefixes[].ip_prefix`,
	},
}

// DefaultIPRangeSources returns the default IP range sources when none are configured.
// Returns [github, google] to support Go proxy and Google services.
// Note: "google" (goog.json) includes broader ranges than "google-cloud" (cloud.json),
// covering CDN/edge infrastructure used by storage.googleapis.com, www.gstatic.com, etc.
func DefaultIPRangeSources() []IPRangeSource {
	return []IPRangeSource{
		{Name: "github"},
		{Name: "google"},
	}
}

// IsKnownIPRangeSource returns true if the given name is a known built-in source.
func IsKnownIPRangeSource(name string) bool {
	_, ok := BuiltinIPRangeSources[name]
	return ok
}

// GetIPRangeSources returns the IP range sources to use.
// If IPRangeSources is explicitly set (even to empty slice), returns it.
// If OverrideDomains is set (override mode), returns empty slice (user controls everything).
// Otherwise returns DefaultIPRangeSources().
func (f *FirewallConfig) GetIPRangeSources() []IPRangeSource {
	if f == nil {
		return DefaultIPRangeSources()
	}

	// If override_domains is set, skip IP range sources entirely
	// (user is in full control mode)
	if len(f.OverrideDomains) > 0 {
		return []IPRangeSource{}
	}

	// If ip_range_sources is explicitly configured, use it
	// Note: This includes empty slice (user can opt out with ip_range_sources: [])
	if f.IPRangeSources != nil {
		return f.IPRangeSources
	}

	// Default: github + google for Go proxy and Google services support
	return DefaultIPRangeSources()
}
