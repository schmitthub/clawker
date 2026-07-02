package consts_test

import (
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
)

// SchemaRefForVersion must pin only exact release versions — every dev-shaped
// version (DEV default, git-describe, dirty, prerelease) falls back to the
// main ref, because only pushed release tags are guaranteed to exist as git
// refs and a dev binary must never stamp a URL that cannot resolve.
func TestSchemaRefForVersion(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{"v0.12.11", "v0.12.11"},
		{"0.12.11", "v0.12.11"}, // GoReleaser {{.Version}} strips the v
		{"DEV", consts.GitHubRefMain},
		{"", consts.GitHubRefMain},
		{"v0.12.10-5-gc97bf1cb", consts.GitHubRefMain}, // git describe between tags
		{"v0.12.10-dirty", consts.GitHubRefMain},
		{"v0.13.0-rc1", consts.GitHubRefMain},                        // prerelease tags are not pinned
		{"v0.0.0-20260702120000-abcdef123456", consts.GitHubRefMain}, // pseudo-version
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := consts.SchemaRefForVersion(tt.version); got != tt.want {
				t.Errorf("SchemaRefForVersion(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

func TestSchemaURL(t *testing.T) {
	tests := []struct {
		filename, ref string
		want          string
	}{
		{
			consts.ProjectSchemaFile, "v0.12.11",
			"https://raw.githubusercontent.com/schmitthub/clawker/v0.12.11/docs/schemas/clawker.schema.json",
		},
		{
			consts.SettingsSchemaFile, consts.GitHubRefMain,
			"https://raw.githubusercontent.com/schmitthub/clawker/main/docs/schemas/settings.schema.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := consts.SchemaURL(tt.filename, tt.ref); got != tt.want {
				t.Errorf("SchemaURL(%q, %q) = %q, want %q", tt.filename, tt.ref, got, tt.want)
			}
		})
	}
}
