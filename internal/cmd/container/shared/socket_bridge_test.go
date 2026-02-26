package shared

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestNeedsSocketBridge(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name   string
		cfg    *config.Project
		expect bool
	}{
		{
			name:   "nil config returns false",
			cfg:    nil,
			expect: false,
		},
		{
			name:   "nil git_credentials defaults to SSH enabled",
			cfg:    &config.Project{},
			expect: true,
		},
		{
			name: "ssh enabled only",
			cfg: &config.Project{
				Security: config.SecurityConfig{
					GitCredentials: &config.GitCredentialsConfig{
						ForwardSSH: boolPtr(true),
						ForwardGPG: boolPtr(false),
					},
				},
			},
			expect: true,
		},
		{
			name: "gpg enabled only",
			cfg: &config.Project{
				Security: config.SecurityConfig{
					GitCredentials: &config.GitCredentialsConfig{
						ForwardSSH: boolPtr(false),
						ForwardGPG: boolPtr(true),
					},
				},
			},
			expect: true,
		},
		{
			name: "both enabled",
			cfg: &config.Project{
				Security: config.SecurityConfig{
					GitCredentials: &config.GitCredentialsConfig{
						ForwardSSH: boolPtr(true),
						ForwardGPG: boolPtr(true),
					},
				},
			},
			expect: true,
		},
		{
			name: "both disabled",
			cfg: &config.Project{
				Security: config.SecurityConfig{
					GitCredentials: &config.GitCredentialsConfig{
						ForwardSSH: boolPtr(false),
						ForwardGPG: boolPtr(false),
					},
				},
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsSocketBridge(tt.cfg)
			assert.Equal(t, tt.expect, got)
		})
	}
}
