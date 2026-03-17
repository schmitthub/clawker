package firewall_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateEnvoyConfig(t *testing.T) {
	tests := []struct {
		name            string
		rules           []config.EgressRule
		goldenFile      string
		wantWarningsSub []string // substrings expected in warnings
	}{
		{
			name: "passthrough only: no path rules",
			rules: []config.EgressRule{
				{Dst: "github.com", Action: "allow"},
				{Dst: "api.github.com", Action: "allow"},
				{Dst: "registry.npmjs.org", Action: "allow"},
			},
			goldenFile: "envoy_passthrough.golden",
		},
		{
			name: "MITM rules: with path rules",
			rules: []config.EgressRule{
				{
					Dst:    "api.openai.com",
					Action: "allow",
					PathRules: []config.PathRule{
						{Path: "/v1/chat", Action: "allow"},
						{Path: "/v1/admin", Action: "deny"},
					},
					PathDefault: "deny",
				},
				{
					Dst:    "storage.googleapis.com",
					Action: "allow",
					PathRules: []config.PathRule{
						{Path: "/download/", Action: "allow"},
					},
					PathDefault: "allow",
				},
			},
			goldenFile: "envoy_mitm.golden",
		},
		{
			name: "mixed: passthrough + MITM + SSH",
			rules: []config.EgressRule{
				{Dst: "github.com", Action: "allow"},
				{
					Dst:    "api.openai.com",
					Action: "allow",
					PathRules: []config.PathRule{
						{Path: "/v1/chat", Action: "allow"},
					},
					PathDefault: "deny",
				},
				{Dst: "github.com", Proto: "ssh", Port: 22, Action: "allow"},
				{Dst: "evil.example.com", Action: "deny"},
				{Dst: "10.0.0.0/8", Action: "allow"},
			},
			goldenFile:      "envoy_mixed.golden",
			wantWarningsSub: []string{"10.0.0.0/8"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warnings, err := firewall.GenerateEnvoyConfig(tt.rules)
			require.NoError(t, err)

			// Check warnings contain expected substrings.
			for _, sub := range tt.wantWarningsSub {
				found := false
				for _, w := range warnings {
					if strings.Contains(w, sub) {
						found = true
						break
					}
				}
				assert.True(t, found, "expected warning containing %q, got %v", sub, warnings)
			}

			goldenPath := filepath.Join("testdata", tt.goldenFile)
			want, err := os.ReadFile(goldenPath)
			require.NoError(t, err, "golden file %s must exist — hand-edit to update", goldenPath)
			assert.Equal(t, string(want), string(got))
		})
	}
}
