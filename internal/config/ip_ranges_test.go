package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIPRangeSource_IsRequired(t *testing.T) {
	tests := []struct {
		name   string
		source IPRangeSource
		want   bool
	}{
		{
			name:   "github defaults to required",
			source: IPRangeSource{Name: "github"},
			want:   true,
		},
		{
			name:   "google-cloud defaults to optional",
			source: IPRangeSource{Name: "google-cloud"},
			want:   false,
		},
		{
			name:   "custom source defaults to optional",
			source: IPRangeSource{Name: "custom", URL: "https://example.com/ranges.json"},
			want:   false,
		},
		{
			name:   "github with explicit required=true",
			source: IPRangeSource{Name: "github", Required: boolPtr(true)},
			want:   true,
		},
		{
			name:   "github with explicit required=false",
			source: IPRangeSource{Name: "github", Required: boolPtr(false)},
			want:   false,
		},
		{
			name:   "google-cloud with explicit required=true",
			source: IPRangeSource{Name: "google-cloud", Required: boolPtr(true)},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.source.IsRequired()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuiltinIPRangeSources(t *testing.T) {
	// Verify all built-in sources are configured
	expectedSources := []string{"github", "google-cloud", "google", "cloudflare", "aws"}
	for _, name := range expectedSources {
		t.Run(name, func(t *testing.T) {
			cfg, ok := BuiltinIPRangeSources[name]
			require.True(t, ok, "expected %s to be a built-in source", name)
			assert.NotEmpty(t, cfg.URL, "expected %s to have a URL", name)
			assert.NotEmpty(t, cfg.JQFilter, "expected %s to have a jq filter", name)
		})
	}
}

func TestIsKnownIPRangeSource(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"github", true},
		{"google-cloud", true},
		{"google", true},
		{"cloudflare", true},
		{"aws", true},
		{"unknown", false},
		{"GITHUB", false}, // case-sensitive
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsKnownIPRangeSource(tt.name)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFirewallConfig_GetIPRangeSources(t *testing.T) {
	tests := []struct {
		name   string
		config *FirewallConfig
		want   []IPRangeSource
	}{
		{
			name:   "nil config returns defaults",
			config: nil,
			want:   DefaultIPRangeSources(),
		},
		{
			name:   "empty config returns defaults",
			config: &FirewallConfig{},
			want:   DefaultIPRangeSources(),
		},
		{
			name: "explicit sources are used",
			config: &FirewallConfig{
				IPRangeSources: []IPRangeSource{{Name: "github"}},
			},
			want: []IPRangeSource{{Name: "github"}},
		},
		{
			name: "empty sources list is respected (opt-out)",
			config: &FirewallConfig{
				IPRangeSources: []IPRangeSource{},
			},
			want: []IPRangeSource{},
		},
		{
			name: "override mode skips IP range sources",
			config: &FirewallConfig{
				OverrideDomains: []string{"custom.com"},
				IPRangeSources:  []IPRangeSource{{Name: "github"}},
			},
			want: []IPRangeSource{},
		},
		{
			name: "custom source with URL",
			config: &FirewallConfig{
				IPRangeSources: []IPRangeSource{
					{Name: "github"},
					{Name: "custom", URL: "https://example.com/ranges.json", JQFilter: ".cidrs[]"},
				},
			},
			want: []IPRangeSource{
				{Name: "github"},
				{Name: "custom", URL: "https://example.com/ranges.json", JQFilter: ".cidrs[]"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetIPRangeSources()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDefaultIPRangeSources(t *testing.T) {
	defaults := DefaultIPRangeSources()

	// Should include github and google-cloud by default
	require.Len(t, defaults, 2)
	assert.Equal(t, "github", defaults[0].Name)
	assert.Equal(t, "google-cloud", defaults[1].Name)

	// Both should use built-in URLs (empty URL/filter)
	assert.Empty(t, defaults[0].URL)
	assert.Empty(t, defaults[0].JQFilter)
	assert.Empty(t, defaults[1].URL)
	assert.Empty(t, defaults[1].JQFilter)
}

func TestBuiltinIPRangeSources_URLs(t *testing.T) {
	// Verify the URLs are correct
	assert.Equal(t, "https://api.github.com/meta", BuiltinIPRangeSources["github"].URL)
	assert.Equal(t, "https://www.gstatic.com/ipranges/cloud.json", BuiltinIPRangeSources["google-cloud"].URL)
	assert.Equal(t, "https://www.gstatic.com/ipranges/goog.json", BuiltinIPRangeSources["google"].URL)
	assert.Equal(t, "https://api.cloudflare.com/client/v4/ips", BuiltinIPRangeSources["cloudflare"].URL)
	assert.Equal(t, "https://ip-ranges.amazonaws.com/ip-ranges.json", BuiltinIPRangeSources["aws"].URL)
}
