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
// Returns github only by default. The "google" source is NOT included due to security
// concerns (allows traffic to user-generated content on GCS/Firebase - see CLAUDE.md).
func DefaultIPRangeSources() []IPRangeSource {
	return []IPRangeSource{
		{Name: "github"},
	}
}

// IsKnownIPRangeSource returns true if the given name is a known built-in source.
func IsKnownIPRangeSource(name string) bool {
	_, ok := BuiltinIPRangeSources[name]
	return ok
}

// GetIPRangeSources returns the IP range sources to use.
// If IPRangeSources is explicitly set (even to empty slice), returns it.
// Otherwise returns DefaultIPRangeSources().
func (f *FirewallConfig) GetIPRangeSources() []IPRangeSource {
	if f == nil {
		return DefaultIPRangeSources()
	}

	// If ip_range_sources is explicitly configured, use it
	// Note: This includes empty slice (user can opt out with ip_range_sources: [])
	if f.IPRangeSources != nil {
		return f.IPRangeSources
	}

	// Default: github only (google excluded for security reasons - see CLAUDE.md)
	return DefaultIPRangeSources()
}
