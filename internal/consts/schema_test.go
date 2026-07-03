package consts_test

import (
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
)

// SchemaRef must always resolve to a git ref that pins the schema as close
// to the binary's structs as possible — a version tag when one is derivable,
// a commit SHA otherwise. The main branch ref is a dead-last resort reserved
// for builds carrying zero VCS metadata (e.g. a tarball build), because a
// branch ref drifts under an installed binary.
func TestSchemaRef(t *testing.T) {
	const fullSHA = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name     string
		version  string
		revision string
		want     string
	}{
		{"release tag", "v0.12.11", "", "v0.12.11"},
		{"goreleaser strips v", "0.12.11", "", "v0.12.11"}, // GoReleaser {{.Version}}
		{"prerelease tag pins", "v0.13.0-rc1", "", "v0.13.0-rc1"},
		{"goreleaser prerelease", "0.13.0-rc1", "", "v0.13.0-rc1"},
		{"describe between tags", "v0.12.10-5-gc97bf1cb", "", "v0.12.10"},
		{"describe dirty", "v0.12.10-dirty", "", "v0.12.10"},
		{"describe between tags dirty", "v0.12.10-5-gc97bf1cb-dirty", "", "v0.12.10"},
		{"describe prerelease between tags", "v0.13.0-rc1-3-gabc1234", "", "v0.13.0-rc1"},
		{"pseudo-version extracts commit", "v0.0.0-20260702120000-abcdef123456", "", "abcdef123456"},
		{"bare short sha", "abc1234", "", "abc1234"}, // git describe --always, tagless repo
		{"bare sha dirty", "abc1234-dirty", "", "abc1234"},
		{"DEV falls back to revision", "DEV", fullSHA, fullSHA},
		{"dev falls back to revision", "dev", fullSHA, fullSHA},
		{"DEV no revision", "DEV", "unknown", consts.GitHubRefMain},
		{"empty everything", "", "", consts.GitHubRefMain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := consts.SchemaRef(tt.version, tt.revision); got != tt.want {
				t.Errorf("SchemaRef(%q, %q) = %q, want %q", tt.version, tt.revision, got, tt.want)
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
