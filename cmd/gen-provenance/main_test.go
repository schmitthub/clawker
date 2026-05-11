package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuild_RealRepo exercises the full predicate pipeline against the
// actual repo files so a structural break in any parser (Dockerfile FROM
// regex, workflow `uses:` regex, goreleaser ldflags regex, bpf2go pin
// regex) surfaces here before it lands in CI. The release pipeline is
// produced once-per-tag and rarely exercised — drift here would silently
// produce malformed provenance until the next release.
func TestBuild_RealRepo(t *testing.T) {
	repoRoot := findRepoRoot(t)

	apt := filepath.Join(t.TempDir(), "apt.txt")
	if err := os.WriteFile(apt, []byte(strings.Join([]string{
		"clang=1:14.0-55.7~deb12u1|amd64",
		"libbpf-dev=1:1.1.2-0+deb12u1|amd64",
		"ca-certificates=20230311+deb12u1|all",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write apt: %v", err)
	}

	out := filepath.Join(t.TempDir(), "predicate.json")
	in := &inputs{
		repoURI:            "https://github.com/schmitthub/clawker",
		sourceCommit:       strings.Repeat("a", 40),
		sourceRef:          "refs/tags/v0.0.0-test",
		workflowRef:        "schmitthub/clawker/.github/workflows/release.yml@refs/tags/v0.0.0-test",
		builderID:          "https://github.com/schmitthub/clawker/.github/workflows/release-build.yml@refs/tags/v0.0.0-test",
		repositoryID:       "1",
		repositoryOwnerID:  "1",
		eventName:          "push",
		runnerEnvironment:  "github-hosted",
		invocationID:       "https://github.com/x/y/actions/runs/1/attempts/1",
		callerWorkflowPath: ".github/workflows/release.yml",
		dockerfilePath:     "Dockerfile.controlplane",
		makefilePath:       "Makefile",
		goreleaserPath:     ".goreleaser.yaml",
		goModPath:          "go.mod",
		bpfGenPath:         "internal/controlplane/firewall/ebpf/gen.go",
		bpfSourceDir:       "internal/controlplane/firewall/ebpf/bpf",
		workflowCallerPath: ".github/workflows/release.yml",
		workflowReusable:   ".github/workflows/release-build.yml",
		aptPackagesPath:    apt,
		repoRoot:           repoRoot,
		output:             out,
	}

	if err := in.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	pred, err := build(in)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if pred.BuildDefinition.BuildType != "https://actions.github.io/buildtypes/workflow/v1" {
		t.Errorf("buildType = %q", pred.BuildDefinition.BuildType)
	}

	// Every category we promise the verifier should appear in resolvedDependencies.
	// Looking for at least one of each kind catches regressions in the parsers
	// without hardcoding exact counts (which drift with workflow edits).
	wantNames := map[string]bool{
		"source":                  false,
		"go-toolchain":            false,
		"bpf2go":                  false,
		"dockerfile-controlplane": false,
		"workflow-caller":         false,
		"workflow-reusable":       false,
		"makefile":                false,
		"goreleaser-config":       false,
		"bpf-source-clawker.c":    false,
		"apt-clang":               false,
	}
	var sawBaseImage, sawAction bool
	for _, d := range pred.BuildDefinition.ResolvedDependencies {
		if _, ok := wantNames[d.Name]; ok {
			wantNames[d.Name] = true
		}
		if strings.HasPrefix(d.Name, "base-image-") {
			sawBaseImage = true
		}
		if strings.HasPrefix(d.Name, "action-") {
			sawAction = true
		}
	}
	for name, ok := range wantNames {
		if !ok {
			t.Errorf("resolvedDependencies missing %q entry", name)
		}
	}
	if !sawBaseImage {
		t.Errorf("resolvedDependencies missing base-image-* entry")
	}
	if !sawAction {
		t.Errorf("resolvedDependencies missing action-* entry")
	}

	// Round-trip JSON to ensure the predicate marshals cleanly.
	b, err := json.MarshalIndent(pred, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(b) {
		t.Errorf("predicate JSON invalid")
	}

	if pred.BuildDefinition.ExternalParameters.BuildConfig.Bpf2goTarget != "amd64,arm64" {
		t.Errorf("bpf2goTarget = %q", pred.BuildDefinition.ExternalParameters.BuildConfig.Bpf2goTarget)
	}
	if pred.BuildDefinition.ExternalParameters.BuildConfig.GoreleaserVersion == "" {
		t.Errorf("goreleaserVersion empty")
	}
	if pred.BuildDefinition.ExternalParameters.BuildConfig.Ldflags["version"] != "{{.Version}}" {
		t.Errorf("ldflags.version = %q", pred.BuildDefinition.ExternalParameters.BuildConfig.Ldflags["version"])
	}
}

func TestParseDockerfileImages(t *testing.T) {
	df := `
FROM debian:bookworm-slim@sha256:abc AS bpf-builder
COPY --from=golang:1.25.10-alpine@sha256:def /usr/local/go /usr/local/go
FROM golang:1.25.10-alpine@sha256:def AS ebpf-manager-builder
FROM golang:1.25.10-alpine@sha256:def AS coredns-builder
FROM scratch AS extract
`
	imgs, err := parseDockerfileImages(df)
	if err != nil {
		t.Fatal(err)
	}
	// debian + golang (dedup'd across 3 occurrences) = 2 distinct images.
	if len(imgs) != 2 {
		t.Errorf("got %d images, want 2: %+v", len(imgs), imgs)
	}
}

func TestParseWorkflowActions(t *testing.T) {
	yaml := `
- uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
- uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6.4.0
- uses: anchore/sbom-action/download-syft@e22c389904149dbc22b58101806040fa8d37a610 # v0.24.0
- uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # duplicate, dedup'd
`
	actions := parseWorkflowActions(yaml, "")
	if len(actions) != 3 {
		t.Errorf("got %d actions, want 3 unique: %+v", len(actions), actions)
	}
	// Ordering is alphabetical by repo path.
	want := []string{"actions/checkout", "actions/setup-go", "anchore/sbom-action/download-syft"}
	for i, w := range want {
		if actions[i].repo != w {
			t.Errorf("actions[%d].repo = %q, want %q", i, actions[i].repo, w)
		}
	}
}

func TestParseGoModToolchain(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "toolchain wins", input: "go 1.25.10\ntoolchain go1.25.10-pinned\n", want: "1.25.10-pinned"},
		{name: "go directive fallback", input: "go 1.25.10\n", want: "1.25.10"},
		{name: "neither", input: "module x\n", wantErr: true},
	}
	for _, c := range cases {
		got, err := parseGoModToolchain(c.input)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr=%v", c.name, err, c.wantErr)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLoadAptPackages(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "apt.txt")
	if err := os.WriteFile(tmp, []byte("clang=1:14.0-x|amd64\nlibbpf-dev=1:1.1.2|amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps, err := loadAptPackages(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 {
		t.Fatalf("got %d, want 2", len(deps))
	}
	if deps[0].URI != "pkg:deb/debian/clang@1:14.0-x?arch=amd64" {
		t.Errorf("deps[0].URI = %q", deps[0].URI)
	}
}

func TestLoadAptPackages_MalformedLine(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "apt.txt")
	if err := os.WriteFile(tmp, []byte("not-a-line"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAptPackages(tmp); err == nil {
		t.Fatal("expected error on malformed line")
	}
}

// findRepoRoot walks up from the test's CWD looking for go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for cur := wd; cur != "/"; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
	}
	t.Fatalf("could not find repo root from %s", wd)
	return ""
}
