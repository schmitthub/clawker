// gen-provenance emits an enriched SLSA v1.0 build provenance predicate
// (predicateType: https://slsa.dev/provenance/v1) for the release pipeline.
//
// The release workflow (.github/workflows/release-build.yml) consumes its
// output as the `predicate-path` input to actions/attest@v4 in custom-
// predicate mode, replacing the thin auto-populated predicate that
// actions/attest-build-provenance@v4 emits by default.
//
// Reproducible-build discovery requirement: every input that affects the
// shipped binary bytes must be enumerable from the predicate alone. The
// resolvedDependencies array captures source commit, base-image digests,
// the bpf-builder stage's apt package closure, Go toolchain version, BPF
// source file hashes, every workflow file hash, every pinned GitHub action
// SHA, and the resolved tool versions (goreleaser, bpf2go). buildDefinition.
// externalParameters mirrors what GitHub's default attestation emits (so
// `gh attestation verify` validates cleanly), with build-config knobs
// (CGO_ENABLED, GOFLAGS, ldflags template, clang -cflags, goreleaser args)
// added under externalParameters.buildConfig.
//
// Pure stdlib leaf binary, parallel to cmd/gen-docs. No internal/ imports.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// inputs gathers every flag the generator accepts. Each field maps 1:1 to a
// pflag declaration in run(); keeping them on a struct keeps the wiring
// readable and the test surface flat.
type inputs struct {
	repoURI            string // https://github.com/<owner>/<repo>
	sourceCommit       string // 40-char git SHA
	sourceRef          string // refs/tags/vX.Y.Z
	workflowRef        string // <owner>/<repo>/.github/workflows/<file>.yml@<ref>
	builderID          string // https://github.com/<owner>/<repo>/.github/workflows/<reusable>.yml@<ref>
	repositoryID       string
	repositoryOwnerID  string
	eventName          string
	runnerEnvironment  string
	invocationID       string
	startedOn          string
	finishedOn         string
	callerWorkflowPath string // .github/workflows/release.yml (relative)

	dockerfilePath     string
	makefilePath       string
	goreleaserPath     string
	goModPath          string
	bpfGenPath         string
	bpfSourceDir       string
	workflowCallerPath string // absolute path to release.yml
	workflowReusable   string // absolute path to release-build.yml
	aptPackagesPath    string
	repoRoot           string

	output string
}

func run(args []string) error {
	flags := pflag.NewFlagSet("gen-provenance", pflag.ContinueOnError)
	in := &inputs{}

	flags.StringVar(&in.repoURI, "repo-uri", "", "Source repository URI (e.g. https://github.com/schmitthub/clawker)")
	flags.StringVar(&in.sourceCommit, "source-commit", "", "Source commit SHA")
	flags.StringVar(&in.sourceRef, "source-ref", "", "Source ref (refs/tags/vX.Y.Z)")
	flags.StringVar(&in.workflowRef, "workflow-ref", "", "GITHUB_WORKFLOW_REF value")
	flags.StringVar(&in.builderID, "builder-id", "", "Builder ID (reusable workflow URL@ref)")
	flags.StringVar(&in.repositoryID, "repository-id", "", "GITHUB_REPOSITORY_ID")
	flags.StringVar(&in.repositoryOwnerID, "repository-owner-id", "", "GITHUB_REPOSITORY_OWNER_ID")
	flags.StringVar(&in.eventName, "event-name", "", "GITHUB_EVENT_NAME")
	flags.StringVar(&in.runnerEnvironment, "runner-environment", "", "RUNNER_ENVIRONMENT")
	flags.StringVar(&in.invocationID, "invocation-id", "", "Actions run URL (predicate runDetails.metadata.invocationId)")
	flags.StringVar(&in.startedOn, "started-on", "", "Build started timestamp (RFC3339, optional)")
	flags.StringVar(&in.finishedOn, "finished-on", "", "Build finished timestamp (RFC3339, optional)")
	flags.StringVar(&in.callerWorkflowPath, "caller-workflow-path", ".github/workflows/release.yml", "Caller workflow path (relative to repo root)")

	flags.StringVar(&in.dockerfilePath, "dockerfile", "Dockerfile.controlplane", "Path to Dockerfile.controlplane")
	flags.StringVar(&in.makefilePath, "makefile", "Makefile", "Path to Makefile")
	flags.StringVar(&in.goreleaserPath, "goreleaser-config", ".goreleaser.yaml", "Path to .goreleaser.yaml")
	flags.StringVar(&in.goModPath, "go-mod", "go.mod", "Path to go.mod")
	flags.StringVar(&in.bpfGenPath, "bpf-gen-go", "internal/controlplane/firewall/ebpf/gen.go", "Path to ebpf gen.go (carries bpf2go pin + clang flags)")
	flags.StringVar(&in.bpfSourceDir, "bpf-source-dir", "internal/controlplane/firewall/ebpf/bpf", "Directory with BPF C sources to hash")
	flags.StringVar(&in.workflowCallerPath, "workflow-caller", ".github/workflows/release.yml", "Path to caller release workflow")
	flags.StringVar(&in.workflowReusable, "workflow-reusable", ".github/workflows/release-build.yml", "Path to reusable build workflow")
	flags.StringVar(&in.aptPackagesPath, "apt-packages", "", "Path to apt package list file (one '<pkg>=<ver>|<arch>' per line) captured from bpf-builder stage; required")
	flags.StringVar(&in.repoRoot, "repo-root", ".", "Repository root (resolves relative paths)")

	flags.StringVar(&in.output, "output", "dist/provenance-predicate.json", "Output path for predicate JSON")

	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	if err := in.validate(); err != nil {
		return err
	}

	predicate, err := build(in)
	if err != nil {
		return fmt.Errorf("build predicate: %w", err)
	}

	body, err := json.MarshalIndent(predicate, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal predicate: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(in.output), 0o755); err != nil {
		return fmt.Errorf("mkdir output: %w", err)
	}
	if err := os.WriteFile(in.output, body, 0o644); err != nil {
		return fmt.Errorf("write predicate: %w", err)
	}

	return nil
}

func (in *inputs) validate() error {
	required := map[string]string{
		"--repo-uri":            in.repoURI,
		"--source-commit":       in.sourceCommit,
		"--source-ref":          in.sourceRef,
		"--workflow-ref":        in.workflowRef,
		"--builder-id":          in.builderID,
		"--repository-id":       in.repositoryID,
		"--repository-owner-id": in.repositoryOwnerID,
		"--event-name":          in.eventName,
		"--runner-environment":  in.runnerEnvironment,
		"--invocation-id":       in.invocationID,
		"--apt-packages":        in.aptPackagesPath,
	}
	missing := []string{}
	for k, v := range required {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required flags: %s", strings.Join(missing, ", "))
	}
	return nil
}

// SLSA v1 predicate types — minimal subset used by this generator. Full
// schema at https://slsa.dev/spec/v1.0/provenance. Omitempty everywhere so
// the emitted JSON stays clean for verifiers that pretty-print it.

type predicate struct {
	BuildDefinition buildDefinition `json:"buildDefinition"`
	RunDetails      runDetails      `json:"runDetails"`
}

type buildDefinition struct {
	BuildType            string               `json:"buildType"`
	ExternalParameters   externalParameters   `json:"externalParameters"`
	InternalParameters   internalParameters   `json:"internalParameters"`
	ResolvedDependencies []resourceDescriptor `json:"resolvedDependencies"`
}

type externalParameters struct {
	Workflow    workflowRef    `json:"workflow"`
	BuildConfig buildConfigExt `json:"buildConfig"`
}

type workflowRef struct {
	Ref        string `json:"ref"`
	Repository string `json:"repository"`
	Path       string `json:"path"`
}

type buildConfigExt struct {
	GoreleaserArgs    string            `json:"goreleaserArgs"`
	GoreleaserVersion string            `json:"goreleaserVersion"`
	GoEnv             map[string]string `json:"goEnv"`
	Ldflags           map[string]string `json:"ldflags"`
	ClangCflags       string            `json:"clangCflags"`
	Bpf2goTarget      string            `json:"bpf2goTarget"`
}

type internalParameters struct {
	GitHub map[string]string `json:"github"`
}

type resourceDescriptor struct {
	URI         string            `json:"uri"`
	Digest      map[string]string `json:"digest,omitempty"`
	Name        string            `json:"name,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type runDetails struct {
	Builder  builder       `json:"builder"`
	Metadata buildMetadata `json:"metadata"`
}

type builder struct {
	ID      string            `json:"id"`
	Version map[string]string `json:"version,omitempty"`
}

type buildMetadata struct {
	InvocationID string `json:"invocationId"`
	StartedOn    string `json:"startedOn,omitempty"`
	FinishedOn   string `json:"finishedOn,omitempty"`
}

func build(in *inputs) (*predicate, error) {
	root := in.repoRoot
	path := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(root, p)
	}

	dockerfileBytes, err := os.ReadFile(path(in.dockerfilePath))
	if err != nil {
		return nil, fmt.Errorf("read dockerfile: %w", err)
	}
	makefileBytes, err := os.ReadFile(path(in.makefilePath))
	if err != nil {
		return nil, fmt.Errorf("read makefile: %w", err)
	}
	goreleaserBytes, err := os.ReadFile(path(in.goreleaserPath))
	if err != nil {
		return nil, fmt.Errorf("read goreleaser config: %w", err)
	}
	goModBytes, err := os.ReadFile(path(in.goModPath))
	if err != nil {
		return nil, fmt.Errorf("read go.mod: %w", err)
	}
	bpfGenBytes, err := os.ReadFile(path(in.bpfGenPath))
	if err != nil {
		return nil, fmt.Errorf("read bpf gen.go: %w", err)
	}
	callerBytes, err := os.ReadFile(path(in.workflowCallerPath))
	if err != nil {
		return nil, fmt.Errorf("read caller workflow: %w", err)
	}
	reusableBytes, err := os.ReadFile(path(in.workflowReusable))
	if err != nil {
		return nil, fmt.Errorf("read reusable workflow: %w", err)
	}

	dockerfileText := string(dockerfileBytes)
	goreleaserText := string(goreleaserBytes)
	bpfGenText := string(bpfGenBytes)

	// resolvedDependencies — order: source → images → apt → toolchain →
	// BPF source → actions → workflow/Makefile/Dockerfile/goreleaser
	// files. Stable ordering helps diff-based audit and verifier output.
	deps := []resourceDescriptor{}

	// 1. Source commit.
	deps = append(deps, resourceDescriptor{
		URI:    fmt.Sprintf("git+%s@%s", in.repoURI, in.sourceRef),
		Digest: map[string]string{"gitCommit": in.sourceCommit},
		Name:   "source",
	})

	// 2. Base image digests from Dockerfile.controlplane FROM lines.
	images, err := parseDockerfileImages(dockerfileText)
	if err != nil {
		return nil, fmt.Errorf("parse Dockerfile FROM lines: %w", err)
	}
	for _, img := range images {
		deps = append(deps, resourceDescriptor{
			URI:    fmt.Sprintf("pkg:docker/%s@%s", img.repoTag, img.digest),
			Digest: map[string]string{"sha256": strings.TrimPrefix(img.digest, "sha256:")},
			Name:   "base-image-" + img.stage,
		})
	}

	// 3. apt package closure from bpf-builder stage.
	aptDeps, err := loadAptPackages(path(in.aptPackagesPath))
	if err != nil {
		return nil, fmt.Errorf("load apt packages: %w", err)
	}
	deps = append(deps, aptDeps...)

	// 4. Go toolchain.
	goVersion, err := parseGoModToolchain(string(goModBytes))
	if err != nil {
		return nil, fmt.Errorf("parse go.mod toolchain: %w", err)
	}
	deps = append(deps, resourceDescriptor{
		URI:         fmt.Sprintf("pkg:golang/go@%s", goVersion),
		Name:        "go-toolchain",
		Annotations: map[string]string{"source": "go.mod go directive"},
	})

	// 5. bpf2go tool version (pinned in gen.go's //go:generate line).
	bpf2goVersion := parseBpf2goVersion(bpfGenText)
	if bpf2goVersion != "" {
		deps = append(deps, resourceDescriptor{
			URI:         fmt.Sprintf("pkg:golang/github.com/cilium/ebpf/cmd/bpf2go@%s", bpf2goVersion),
			Name:        "bpf2go",
			Annotations: map[string]string{"source": "//go:generate directive in gen.go"},
		})
	}

	// 6. BPF C source + headers.
	bpfFiles, err := hashDir(path(in.bpfSourceDir))
	if err != nil {
		return nil, fmt.Errorf("hash bpf sources: %w", err)
	}
	bpfRel := strings.TrimPrefix(in.bpfSourceDir, "./")
	for _, f := range bpfFiles {
		deps = append(deps, resourceDescriptor{
			URI:    fmt.Sprintf("git+%s@%s#%s/%s", in.repoURI, in.sourceRef, bpfRel, f.name),
			Digest: map[string]string{"sha256": f.sha256},
			Name:   "bpf-source-" + f.name,
		})
	}
	// gen.go itself drives the bpf2go invocation.
	deps = append(deps, resourceDescriptor{
		URI:    fmt.Sprintf("git+%s@%s#%s", in.repoURI, in.sourceRef, in.bpfGenPath),
		Digest: map[string]string{"sha256": sha256Hex(bpfGenBytes)},
		Name:   "bpf-source-gen.go",
	})

	// 7. Every pinned GitHub Action from both workflow files.
	actions := parseWorkflowActions(string(callerBytes), string(reusableBytes))
	for _, a := range actions {
		deps = append(deps, resourceDescriptor{
			URI:    fmt.Sprintf("git+https://github.com/%s@%s", a.repo, a.sha),
			Digest: map[string]string{"gitCommit": a.sha},
			Name:   "action-" + actionShortName(a.repo),
		})
	}

	// 8. Workflow + build recipe file hashes. Even though the source commit
	// pins their content via gitCommit, an explicit file-content digest is
	// cheaper for an offline verifier and makes drift visible without
	// resolving the git tree.
	for _, f := range []struct {
		path  string
		bytes []byte
		name  string
	}{
		{in.workflowCallerPath, callerBytes, "workflow-caller"},
		{in.workflowReusable, reusableBytes, "workflow-reusable"},
		{in.dockerfilePath, dockerfileBytes, "dockerfile-controlplane"},
		{in.makefilePath, makefileBytes, "makefile"},
		{in.goreleaserPath, goreleaserBytes, "goreleaser-config"},
	} {
		deps = append(deps, resourceDescriptor{
			URI:    fmt.Sprintf("git+%s@%s#%s", in.repoURI, in.sourceRef, f.path),
			Digest: map[string]string{"sha256": sha256Hex(f.bytes)},
			Name:   f.name,
		})
	}

	// externalParameters.buildConfig — knobs that affect output bytes.
	goreleaserVersion := parseGoreleaserVersion(string(reusableBytes))
	goreleaserArgs := parseGoreleaserArgs(string(reusableBytes))
	bpf2goTarget := parseBpf2goTarget(bpfGenText)
	clangCflags := parseClangCflags(bpfGenText)

	pred := &predicate{
		BuildDefinition: buildDefinition{
			BuildType: "https://actions.github.io/buildtypes/workflow/v1",
			ExternalParameters: externalParameters{
				Workflow: workflowRef{
					Ref:        in.sourceRef,
					Repository: in.repoURI,
					Path:       in.callerWorkflowPath,
				},
				BuildConfig: buildConfigExt{
					GoreleaserArgs:    goreleaserArgs,
					GoreleaserVersion: goreleaserVersion,
					GoEnv: map[string]string{
						"CGO_ENABLED": "0",
						"GOFLAGS":     "-trimpath",
					},
					Ldflags: map[string]string{
						"version": parseLdflagTemplate(goreleaserText, "Version"),
						"date":    parseLdflagTemplate(goreleaserText, "Date"),
						"strip":   "-s -w",
					},
					ClangCflags:  clangCflags,
					Bpf2goTarget: bpf2goTarget,
				},
			},
			InternalParameters: internalParameters{
				GitHub: map[string]string{
					"event_name":          in.eventName,
					"repository_id":       in.repositoryID,
					"repository_owner_id": in.repositoryOwnerID,
					"runner_environment":  in.runnerEnvironment,
				},
			},
			ResolvedDependencies: deps,
		},
		RunDetails: runDetails{
			Builder: builder{ID: in.builderID},
			Metadata: buildMetadata{
				InvocationID: in.invocationID,
				StartedOn:    in.startedOn,
				FinishedOn:   in.finishedOn,
			},
		},
	}

	return pred, nil
}

// ----------------------------------------------------------------------------
// Parsers
// ----------------------------------------------------------------------------

type dockerImage struct {
	repoTag string // debian:bookworm-slim, golang:1.25.10-alpine
	digest  string // sha256:...
	stage   string // bpf-builder, golang-shared, ebpf-manager-builder, ...
}

// FROM <image>[:tag]@sha256:<digest> [AS <stage>]
var fromLine = regexp.MustCompile(
	`(?m)^FROM\s+([^\s@]+)@(sha256:[0-9a-f]+)(?:\s+AS\s+(\S+))?`)

// COPY --from=<image>[:tag]@sha256:<digest> ... (we only care about the
// inline image, used to copy the Go toolchain across stages)
var copyFromLine = regexp.MustCompile(
	`(?m)^COPY\s+--from=([^\s:@]+(?::[^\s@]+)?)@(sha256:[0-9a-f]+)\s`)

func parseDockerfileImages(text string) ([]dockerImage, error) {
	seen := map[string]bool{}
	out := []dockerImage{}
	for _, m := range fromLine.FindAllStringSubmatch(text, -1) {
		key := m[1] + "@" + m[2]
		stage := m[3]
		if stage == "" {
			stage = "anonymous"
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, dockerImage{repoTag: m[1], digest: m[2], stage: stage})
	}
	for _, m := range copyFromLine.FindAllStringSubmatch(text, -1) {
		key := m[1] + "@" + m[2]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, dockerImage{repoTag: m[1], digest: m[2], stage: "copy-from"})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no FROM lines with @sha256 digests found")
	}
	return out, nil
}

// apt-packages.txt format: one `<pkg>=<version>|<arch>` per line, output of
//
//	dpkg-query -W -f '${Package}=${Version}|${Architecture}\n'
//
// inside the built bpf-builder image. Generator emits one
// pkg:deb/debian/<pkg>@<version>?arch=<arch> ResourceDescriptor per line.
func loadAptPackages(path string) ([]resourceDescriptor, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	out := make([]resourceDescriptor, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		eq := strings.Index(ln, "=")
		bar := strings.LastIndex(ln, "|")
		if eq < 0 || bar < 0 || bar < eq {
			return nil, fmt.Errorf("apt-packages line malformed: %q (expected <pkg>=<ver>|<arch>)", ln)
		}
		pkg := ln[:eq]
		ver := ln[eq+1 : bar]
		arch := ln[bar+1:]
		out = append(out, resourceDescriptor{
			URI:  fmt.Sprintf("pkg:deb/debian/%s@%s?arch=%s", pkg, ver, arch),
			Name: "apt-" + pkg,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("apt-packages file %s yielded no entries", path)
	}
	return out, nil
}

var toolchainLine = regexp.MustCompile(`(?m)^toolchain\s+go(\S+)`)
var goLine = regexp.MustCompile(`(?m)^go\s+(\S+)`)

// parseGoModToolchain prefers an explicit `toolchain` directive (since that
// is what's actually used by the build) and falls back to the `go` directive
// when no toolchain pin is set.
func parseGoModToolchain(text string) (string, error) {
	if m := toolchainLine.FindStringSubmatch(text); m != nil {
		return m[1], nil
	}
	if m := goLine.FindStringSubmatch(text); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("neither toolchain nor go directive found")
}

var bpf2goVersionRE = regexp.MustCompile(`github\.com/cilium/ebpf/cmd/bpf2go@(v[0-9][^\s]+)`)

func parseBpf2goVersion(text string) string {
	if m := bpf2goVersionRE.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

var bpf2goTargetRE = regexp.MustCompile(`-target\s+(\S+)`)

func parseBpf2goTarget(text string) string {
	if m := bpf2goTargetRE.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

var clangCflagsRE = regexp.MustCompile(`-cflags\s+"([^"]+)"`)

func parseClangCflags(text string) string {
	if m := clangCflagsRE.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

type actionRef struct {
	repo string // <owner>/<repo>[/path]
	sha  string
}

var actionLineRE = regexp.MustCompile(`uses:\s*([^\s@]+)@([0-9a-f]{40})`)

func parseWorkflowActions(workflows ...string) []actionRef {
	seen := map[string]bool{}
	out := []actionRef{}
	for _, text := range workflows {
		for _, m := range actionLineRE.FindAllStringSubmatch(text, -1) {
			key := m[1] + "@" + m[2]
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, actionRef{repo: m[1], sha: m[2]})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].repo < out[j].repo })
	return out
}

// actionShortName returns the trailing path segment of an action ref, useful
// as a human-readable name (`actions/checkout` → `checkout`,
// `anchore/sbom-action/download-syft` → `sbom-action-download-syft`).
func actionShortName(repo string) string {
	parts := strings.Split(repo, "/")
	if len(parts) <= 1 {
		return repo
	}
	return strings.Join(parts[1:], "-")
}

var goreleaserVersionRE = regexp.MustCompile(`(?m)^\s*version:\s*"?(v[0-9][^"\s]*)"?`)

func parseGoreleaserVersion(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		// `version: "vX.Y.Z"` lines under goreleaser-action's `with:` block.
		// The regex's `v[0-9]` anchor excludes the unrelated top-level
		// `version: 2` of goreleaser's own config schema.
		if m := goreleaserVersionRE.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

var goreleaserArgsRE = regexp.MustCompile(`(?m)^\s*args:\s*(.+?)\s*$`)

func parseGoreleaserArgs(text string) string {
	if m := goreleaserArgsRE.FindStringSubmatch(text); m != nil {
		return strings.Trim(m[1], `"`)
	}
	return ""
}

var ldflagTemplateRE = regexp.MustCompile(`internal/build\.([A-Za-z]+)=({{[^}]+}})`)

func parseLdflagTemplate(text, field string) string {
	for _, m := range ldflagTemplateRE.FindAllStringSubmatch(text, -1) {
		if m[1] == field {
			return m[2]
		}
	}
	return ""
}

type fileHash struct {
	name   string
	sha256 string
}

func hashDir(dir string) ([]fileHash, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []fileHash{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, fileHash{name: e.Name(), sha256: sha256Hex(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
