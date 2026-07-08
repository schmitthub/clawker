package bundler

import (
	"archive/tar"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"time"

	clawkerdembed "github.com/schmitthub/clawker/clawkerd/embed"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/harness"
	"github.com/schmitthub/clawker/internal/hostproxy/internals"
)

// Embedded assets for Dockerfile generation

// DockerfileBaseTemplate renders the per-project shared base image
// (harness-agnostic layers). Harness images build FROM its output.
//
//go:embed assets/Dockerfile.base.tmpl
var DockerfileBaseTemplate string

// DockerfileHarnessImageTemplate renders the harness image: template
// blocks, volume dirs, seeds, and clawker root assets FROM the shared
// base image. Composed with the harness bundle's block defines.
//
//go:embed assets/Dockerfile.harness-image.tmpl
var DockerfileHarnessImageTemplate string

// Build-context filenames for clawker-owned assets, referenced by COPY
// instructions in the harness-image template.
const (
	ctxFileHostOpen = "host-open.sh"
	// BaseDockerfileName is the reserved name the rendered base Dockerfile
	// is injected under in the legacy tar build context, so a user's own
	// Dockerfile in the project build context is never clobbered.
	BaseDockerfileName   = "Dockerfile.clawker-base"
	ctxFileCallbackFwd   = "callback-forwarder.go"
	ctxFileGitCredential = "git-credential-clawker.sh" //nolint:gosec // filename, not a credential
	ctxFileSocketServer  = "clawker-socket-server.go"
	ctxFileClawkerd      = "clawkerd"
)

// tarFileMode is the mode recorded for files streamed into the build-context
// tar archive.
const tarFileMode = 0o644

// Re-exported from internal/hostproxy/internals so the bundler hashes
// them with its own assets.
var (
	HostOpenScript          = internals.HostOpenScript
	CallbackForwarderSource = internals.CallbackForwarderSource
	GitCredentialScript     = internals.GitCredentialScript
	SocketForwarderSource   = internals.SocketForwarderSource
)

// Default values for container configuration
const (
	DefaultHarnessVersion = "latest"
	DefaultUsername       = "claude"
	DefaultShell          = "/bin/zsh"
	// SubstrateImage is the single base image every generated base Dockerfile
	// builds FROM. Clawker owns the substrate: users extend it with packages,
	// stacks, and run instructions — never by swapping the image. Pinned
	// by digest per the version-pinning policy; the digest MUST reference a
	// multi-arch OCI image index (verify with `docker buildx imagetools
	// inspect`).
	SubstrateImage = "debian:bookworm-slim@sha256:60eac759739651111db372c07be67863818726f754804b8707c90979bda511df"

	// DefaultGoBuilderImage is the Go toolchain image used for builder stages.
	//
	// Pinned to exact patch version + multi-arch manifest-list (OCI image
	// index) SHA digest. The digest MUST be a
	// manifest list so cross-platform builds can select the right per-arch
	// manifest at pull time. Verify via:
	//   docker buildx imagetools inspect golang:<version>-alpine
	// MediaType must be `application/vnd.oci.image.index.v1+json` before
	// updating this constant. Single-platform digests break multi-arch builds.
	DefaultGoBuilderImage = "golang:1.25.10-alpine@sha256:8d22e29d960bc50cd025d93d5b7c7d220b1ee9aa7a239b3c8f55a57e987e8d45"
)

// DockerfileContext contains the template data for generating a Dockerfile.
// Only structural fields that affect the image filesystem are included here.
// Config-dependent values (env vars, labels, EXPOSE, VOLUME, HEALTHCHECK, SHELL)
// are injected at container creation time or via the Docker build API.
type DockerfileContext struct {
	BaseImage     string
	Packages      []string
	Username      string
	UID           int
	GID           int
	Shell         string
	WorkspacePath string
	// HarnessVersion is the concrete harness tool version rendered into the
	// harness template's version ARG (e.g. ARG CLAUDE_CODE_VERSION=2.1.5).
	HarnessVersion string
	// HarnessBaseImage is the FROM reference of the harness image's final
	// stage — the per-project shared base image (clawker-<project>:base).
	// Only the harness-image template references it.
	HarnessBaseImage string
	// HarnessVolumeDirs are the harness's declared persisted-dir paths
	// (home-relative), pre-created by the harness-image template's
	// runtime-dirs RUN so the volume mounts inherit correct ownership.
	HarnessVolumeDirs []string
	// HarnessSeeds drive the harness-image template's generic seed-staging section:
	// each seed file is COPY'd into the image's seed dir and listed in the
	// baked seed manifest that CP's generic seed-apply step interprets on
	// first boot.
	HarnessSeeds []harness.Seed
	// StackRootSteps / StackUserSteps are pre-rendered stack
	// fragments for THIS render's root/user anchor. The generator computes
	// stage placement (project-declared → base image, harness-declared-only
	// → harness image) and fills them per render; the templates just emit
	// them at static anchors. Nothing declared → empty → zero bytes.
	StackRootSteps []string
	StackUserSteps []string
	// HarnessPackages are extra apt packages for THIS harness image only,
	// from the per-harness build overlay (build.harnesses.<name>.packages).
	// Rendered by the harness-image template's early-root apt slot; never
	// deduped against Packages (apt install is idempotent). Empty for the
	// base render — the field only carries overlay content in GenerateHarness.
	HarnessPackages []string
	BuildKitEnabled bool
	Instructions    *DockerfileInstructions
	Inject          *DockerfileInject

	// OTEL telemetry endpoint — populated from cfg.OtelCollectorURL().
	// Wired into the container as OTEL_EXPORTER_OTLP_ENDPOINT (base URL,
	// no path); the OTel SDK appends /v1/{metrics,logs,traces} per
	// signal. Defaults to the otel-collector OTLP/HTTP receiver so
	// Prometheus retains metric metadata for OpenSearch Dashboards
	// (/api/v1/metadata excludes OTLP-ingested series).
	OtelEndpoint             string // e.g. "http://otel-collector:4318"
	OtelLogsExportInterval   int    // milliseconds, e.g. 5000
	OtelMetricExportInterval int    // milliseconds, e.g. 10000

	// OTEL feature flags — populated from config.TelemetryConfig
	OtelLogToolDetails     bool // OTEL_LOG_TOOL_DETAILS=1
	OtelLogUserPrompts     bool // OTEL_LOG_USER_PROMPTS=1
	OtelIncludeAccountUUID bool // OTEL_METRICS_INCLUDE_ACCOUNT_UUID=true
	OtelIncludeSessionID   bool // OTEL_METRICS_INCLUDE_SESSION_ID=true

	HasFirewallCA  bool   // CA cert exists for MITM inspection
	GoBuilderImage string // Go toolchain image for builder stages (e.g. "golang:1.25.10-alpine@sha256:...")
}

// DockerfileInstructions contains type-safe Dockerfile instructions.
// Only structural instructions that affect the image filesystem are included.
// Non-structural instructions (ENV, Labels, EXPOSE, VOLUME, HEALTHCHECK, SHELL)
// are injected at container creation time or via the Docker build API.
type DockerfileInstructions struct {
	Copy    []CopyInstruction
	Args    []ArgInstruction
	UserRun []string
	RootRun []string
}

// DockerfileInject contains raw instruction injection points.
type DockerfileInject struct {
	AfterFrom           []string
	AfterPackages       []string
	AfterUserSetup      []string
	AfterUserSwitch     []string
	AfterHarnessInstall []string
	BeforeEntrypoint    []string
}

// CopyInstruction represents a COPY instruction.
type CopyInstruction struct {
	Src   string
	Dest  string
	Chown string
	Chmod string
}

// ArgInstruction represents an ARG instruction.
type ArgInstruction struct {
	Name    string
	Default string
}

// ProjectGenerator creates Dockerfiles dynamically from project configuration (clawker.yaml).
type ProjectGenerator struct {
	cfg             config.Config
	workDir         string
	BuildKitEnabled bool // Enables --mount=type=cache directives in generated Dockerfiles
	// HarnessVersion is the concrete version string baked into the rendered
	// ARG default (e.g. "2.1.5"). The registry resolution that turns the
	// manifest's version spec into a concrete version happens at the command
	// layer via bundler.ResolveHarnessVersion — bundler itself doesn't fetch.
	// Empty means use DefaultHarnessVersion ("latest") as a literal.
	HarnessVersion string
	// Harness selects the harness bundle whose template blocks and context
	// files compose the rendered Dockerfile. Empty means the built-in
	// DefaultHarnessName — harness selection is explicit; there is no
	// registry default.
	Harness string
	// BaseImageRef is the per-project shared base image reference the
	// harness image builds FROM (clawker-<project>:base). Set by the
	// docker Builder — bundler never derives project names itself.
	// Required by GenerateHarness.
	BaseImageRef string

	bundle *harness.Bundle // lazily loaded via harnessBundle()

	// provenance accumulates build-output lines describing which layer each
	// stack and the harness bundle resolved from when a closer layer shadowed
	// a farther one (and, for the harness, always). Read via Provenance()
	// after GenerateBase/GenerateHarness; the docker Builder forwards it to
	// clawker build stderr.
	provenance []string
}

// Provenance returns the accumulated resolution provenance lines, deduplicated
// and in insertion order. It is populated by GenerateBase/GenerateHarness.
func (g *ProjectGenerator) Provenance() []string {
	seen := make(map[string]struct{}, len(g.provenance))
	out := make([]string, 0, len(g.provenance))
	for _, line := range g.provenance {
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}

// recordStackProvenance appends the build-output lines for a batch of stack
// resolutions that shadowed a farther layer.
func (g *ProjectGenerator) recordStackProvenance(prov []stackProvenance) {
	for _, p := range prov {
		g.provenance = append(g.provenance, p.line())
	}
}

// harnessBundle resolves and caches the selected harness bundle, recording
// its resolution provenance on first load.
func (g *ProjectGenerator) harnessBundle() (*harness.Bundle, error) {
	if g.bundle != nil {
		return g.bundle, nil
	}
	name, err := ResolveHarnessName(g.cfg, g.Harness)
	if err != nil {
		return nil, err
	}
	b, prov, err := loadHarnessResolved(g.cfg, name)
	if err != nil {
		return nil, err
	}
	g.provenance = append(g.provenance, prov.line())
	g.bundle = b
	return b, nil
}

// NewProjectGenerator creates a new project Dockerfile generator.
func NewProjectGenerator(cfg config.Config, workDir string) *ProjectGenerator {
	return &ProjectGenerator{
		cfg:     cfg,
		workDir: workDir,
	}
}

// GenerateBase renders the per-project shared base image Dockerfile
// (harness-agnostic layers: packages, user setup, project instructions).
// No harness bundle is involved — the base template has no block slots.
func (g *ProjectGenerator) GenerateBase() ([]byte, error) {
	tctx, err := g.buildContext()
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}

	// Project-declared stacks render in the base, before the project's
	// own instructions. Resolution deliberately excludes the harness bundle
	// (resolveProjectStacks) — the shared base stays harness-agnostic.
	root, user, prov, err := resolveProjectStacks(g.cfg, g.cfg.Project().Build.Stacks)
	if err != nil {
		return nil, err
	}
	g.recordStackProvenance(prov)
	if tctx.StackRootSteps, err = renderStackSteps(root, tctx); err != nil {
		return nil, err
	}
	if tctx.StackUserSteps, err = renderStackSteps(user, tctx); err != nil {
		return nil, err
	}

	tmpl, err := template.New("Dockerfile.base").Parse(DockerfileBaseTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse base Dockerfile template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, tctx); err != nil {
		return nil, fmt.Errorf("failed to render base Dockerfile template: %w", err)
	}

	return buf.Bytes(), nil
}

// GenerateHarness renders the harness image Dockerfile: the selected
// bundle's template blocks, volume dirs, seeds, and clawker root assets,
// building FROM the shared base image named by BaseImageRef. The
// HarnessVersion baked into the rendered version ARG comes from the
// generator's HarnessVersion field (set by callers from the resolved
// npm version) — empty falls back to DefaultHarnessVersion literal.
func (g *ProjectGenerator) GenerateHarness() ([]byte, error) {
	if g.BaseImageRef == "" {
		return nil, ErrNoBaseImageRef
	}

	tctx, err := g.buildContext()
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}
	tctx.HarnessBaseImage = g.BaseImageRef

	bundle, err := g.harnessBundle()
	if err != nil {
		return nil, err
	}

	// An overlay keyed to a harness that resolves nowhere (typo, or a bundle
	// the user never registered) would otherwise be dead config that silently
	// drops its packages/stacks/inject from every image — surface it loudly.
	if overlayErr := validateOverlayKeys(g.cfg); overlayErr != nil {
		return nil, overlayErr
	}

	// The per-harness build overlay (build.harnesses.<name>) is scoped to this
	// one harness's image, keyed by the resolved harness name the bundle
	// carries.
	overlay := g.cfg.Project().Build.Harnesses[bundle.Name]

	// Harness-declared stacks ALWAYS render here with their lineage-resolved
	// definition — no cross-stratum dedup against project-declared base
	// stacks (design §2). A name the project also declares in build.stacks
	// renders in both images; fragment self-guards own any interaction. The
	// project's per-harness overlay stacks render AFTER the bundle's own
	// installer stacks (installer → overlay), sharing one lineage lookup so
	// a name repeated across the two sources still renders once.
	decls := slices.Concat(bundle.Manifest.Stacks, overlay.Stacks)
	root, user, prov, err := resolveHarnessStacks(g.cfg, bundle, decls)
	if err != nil {
		return nil, err
	}
	g.recordStackProvenance(prov)
	if tctx.StackRootSteps, err = renderStackSteps(root, tctx); err != nil {
		return nil, err
	}
	if tctx.StackUserSteps, err = renderStackSteps(user, tctx); err != nil {
		return nil, err
	}

	applyHarnessOverlay(tctx, overlay)

	tmpl, err := harness.Compose(DockerfileHarnessImageTemplate, bundle)
	if err != nil {
		return nil, fmt.Errorf("failed to parse harness Dockerfile template: %w", err)
	}

	var buf bytes.Buffer
	if execErr := tmpl.Execute(&buf, tctx); execErr != nil {
		return nil, fmt.Errorf("failed to render harness Dockerfile template: %w", execErr)
	}

	return buf.Bytes(), nil
}

// applyHarnessOverlay applies the per-harness build overlay's packages and
// inject points to the harness-image template context. Overlay stacks are
// handled separately — they join the bundle's installer declarations before
// lineage resolution in GenerateHarness.
func applyHarnessOverlay(tctx *DockerfileContext, overlay config.HarnessBuildOverlay) {
	// Overlay apt packages render in this harness image's early-root slot,
	// as declared — no dedupe against the base package list.
	tctx.HarnessPackages = overlay.Packages

	// Overlay inject points render ONLY in this harness's image, appended
	// after any global project inject at the same points (declaration order).
	if overlay.Inject == nil {
		return
	}
	if tctx.Inject == nil {
		tctx.Inject = &DockerfileInject{
			AfterFrom:           nil,
			AfterPackages:       nil,
			AfterUserSetup:      nil,
			AfterUserSwitch:     nil,
			AfterHarnessInstall: nil,
			BeforeEntrypoint:    nil,
		}
	}
	// slices.Concat allocates fresh so the append never writes into a
	// config-owned backing array (the global BeforeEntrypoint slice
	// aliases the store).
	tctx.Inject.AfterHarnessInstall = slices.Concat(tctx.Inject.AfterHarnessInstall, overlay.Inject.AfterHarnessInstall)
	tctx.Inject.BeforeEntrypoint = slices.Concat(tctx.Inject.BeforeEntrypoint, overlay.Inject.BeforeEntrypoint)
}

// GenerateHarnessBuildContext builds a tar archive build context for the
// harness image using pre-rendered Dockerfile bytes: bundle assets, the
// firewall CA, and the clawker-owned scripts/binaries. Project files are
// NOT staged here — user copy instructions render into the base image,
// whose context is the project build-context directory.
func (g *ProjectGenerator) GenerateHarnessBuildContext(dockerfile []byte) (io.Reader, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	// Add Dockerfile to archive
	if err := addFileToTar(tw, "Dockerfile", dockerfile); err != nil {
		return nil, err
	}

	// Add the harness bundle's assets/ tree (config seeds, scripts,
	// instruction files — referenced by COPY instructions in the harness
	// template blocks and seeds[].file entries).
	bundle, bundleErr := g.harnessBundle()
	if bundleErr != nil {
		return nil, bundleErr
	}
	if walkErr := bundle.WalkAssets(func(relPath string, content []byte) error {
		return addFileToTar(tw, relPath, content)
	}); walkErr != nil {
		return nil, fmt.Errorf("stage harness assets: %w", walkErr)
	}

	// Conditionally add firewall CA cert for MITM inspection
	if err := g.addFirewallCAToTar(tw); err != nil {
		return nil, err
	}

	// Clawker-owned scripts and binaries: host-open, the two Go sources
	// compiled by the builder stages, the git credential helper, and the
	// pre-compiled clawkerd binary dropped at /usr/local/bin/clawkerd.
	clawkerFiles := []struct {
		name    string
		content []byte
	}{
		{ctxFileHostOpen, []byte(HostOpenScript)},
		{ctxFileCallbackFwd, []byte(CallbackForwarderSource)},
		{ctxFileGitCredential, []byte(GitCredentialScript)},
		{ctxFileSocketServer, []byte(SocketForwarderSource)},
		{ctxFileClawkerd, clawkerdembed.Binary},
	}
	for _, f := range clawkerFiles {
		if err := addFileToTar(tw, f.name, f.content); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close harness build context tar: %w", err)
	}

	return buf, nil
}

// addFirewallCAToTar stages the firewall MITM CA cert into the archive
// when one exists; no CA (probe error or empty path) means the firewall
// is off and staging is skipped — same best-effort semantics as the
// BuildKit context writer.
func (g *ProjectGenerator) addFirewallCAToTar(tw *tar.Writer) error {
	if caCertPath, err := g.firewallCACertPath(); err == nil && caCertPath != "" {
		content, readErr := os.ReadFile(caCertPath)
		if readErr != nil {
			return fmt.Errorf("failed to read firewall CA cert: %w", readErr)
		}
		return addFileToTar(tw, "clawker-ca.crt", content)
	}
	return nil
}

// WriteHarnessBuildContextToDir writes the harness image's Dockerfile and all
// supporting scripts to a directory on disk. BuildKit requires files on the
// filesystem (not a tar stream) because it creates fsutil.FS mounts from
// directory paths.
func (g *ProjectGenerator) WriteHarnessBuildContextToDir(dir string, dockerfile []byte) error {
	// Write Dockerfile
	//nolint:gosec // generated Dockerfile, not a secret
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfile, 0o644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// Write the harness bundle's assets/ tree (mirrors GenerateHarnessBuildContext).
	bundle, err := g.harnessBundle()
	if err != nil {
		return err
	}
	if walkErr := writeBundleAssetsToDir(bundle, dir); walkErr != nil {
		return walkErr
	}

	// Write all supporting scripts (mirrors GenerateHarnessBuildContext).
	// content is []byte so the multi-MB clawkerdembed.Binary passes through
	// without a string<->[]byte round-trip copy at WriteFile.
	scripts := []struct {
		name    string
		content []byte
		mode    os.FileMode
	}{
		{ctxFileHostOpen, []byte(HostOpenScript), 0o755},
		{ctxFileCallbackFwd, []byte(CallbackForwarderSource), 0o644},
		{ctxFileGitCredential, []byte(GitCredentialScript), 0o755},
		{ctxFileSocketServer, []byte(SocketForwarderSource), 0o644},
		{ctxFileClawkerd, clawkerdembed.Binary, 0o755},
	}

	for _, s := range scripts {
		if err := os.WriteFile(filepath.Join(dir, s.name), s.content, s.mode); err != nil {
			return fmt.Errorf("failed to write %s: %w", s.name, err)
		}
	}

	// Conditionally write firewall CA cert for MITM inspection
	if caCertPath, err := g.firewallCACertPath(); err == nil && caCertPath != "" {
		content, err := os.ReadFile(caCertPath)
		if err != nil {
			return fmt.Errorf("failed to read firewall CA cert: %w", err)
		}
		// The CA cert is COPY'd into the image and must stay world-readable
		// there — it is the public half only, not a secret.
		//nolint:gosec // public cert half only — must stay world-readable in-image
		if writeErr := os.WriteFile(filepath.Join(dir, "clawker-ca.crt"), content, 0o644); writeErr != nil {
			return fmt.Errorf("failed to write clawker-ca.crt: %w", writeErr)
		}
	}

	return nil
}

// GetBuildContext returns the build context path (the project root — the
// base image's build context, home of copy-instruction srcs).
func (g *ProjectGenerator) GetBuildContext() string {
	return g.workDir
}

// isBasePackage reports whether the base template already installs pkg; user
// build.packages entries duplicating the floor are filtered out.
func isBasePackage(pkg string) bool {
	switch pkg {
	case "less", "git", "procps", "sudo", "fzf",
		"zsh", "man-db", "unzip", "gnupg2", "openssh-client",
		"gcc", "libc6-dev", "make",
		"iproute2", "dnsutils",
		"aggregate", "jq", "nano", "vim", "wget",
		"curl", "gh", "locales", "locales-all",
		"ca-certificates", "xz-utils":
		return true
	}
	return false
}

// filterBasePackages removes packages that are already in the template.
func filterBasePackages(packages []string) []string {
	var filtered []string
	for _, pkg := range packages {
		if !isBasePackage(pkg) {
			filtered = append(filtered, pkg)
		}
	}
	return filtered
}

// buildContext creates the template context from config.
func (g *ProjectGenerator) buildContext() (*DockerfileContext, error) {
	p := g.cfg.Project()

	// OTEL telemetry from monitoring config
	mon := g.cfg.MonitoringConfig()

	// Check if firewall CA cert exists for MITM inspection
	hasFirewallCA := false
	if caCertPath, err := g.firewallCACertPath(); err == nil && caCertPath != "" {
		hasFirewallCA = true
	}

	harnessVersion := g.HarnessVersion
	if harnessVersion == "" {
		harnessVersion = DefaultHarnessVersion
	}

	bundle, err := g.harnessBundle()
	if err != nil {
		return nil, err
	}

	tctx := &DockerfileContext{
		BaseImage:                SubstrateImage,
		Packages:                 filterBasePackages(p.Build.Packages),
		Username:                 DefaultUsername,
		UID:                      g.cfg.ContainerUID(),
		GID:                      g.cfg.ContainerGID(),
		Shell:                    DefaultShell,
		WorkspacePath:            "/workspace",
		HarnessVersion:           harnessVersion,
		HarnessVolumeDirs:        harnessVolumeDirs(bundle),
		HarnessSeeds:             bundle.Manifest.Seeds,
		BuildKitEnabled:          g.BuildKitEnabled,
		HasFirewallCA:            hasFirewallCA,
		OtelEndpoint:             g.cfg.OtelCollectorURL(),
		OtelLogsExportInterval:   mon.Telemetry.LogsExportIntervalMs,
		OtelMetricExportInterval: mon.Telemetry.MetricExportIntervalMs,
		OtelLogToolDetails:       *mon.Telemetry.LogToolDetails,
		OtelLogUserPrompts:       *mon.Telemetry.LogUserPrompts,
		OtelIncludeAccountUUID:   *mon.Telemetry.IncludeAccountUUID,
		OtelIncludeSessionID:     *mon.Telemetry.IncludeSessionID,
		GoBuilderImage:           DefaultGoBuilderImage,
	}

	// Populate Instructions if present (structural only — Copy, Args, RUN)
	if p.Build.Instructions != nil {
		inst := p.Build.Instructions
		tctx.Instructions = &DockerfileInstructions{
			Copy:    convertCopyInstructions(inst.Copy),
			Args:    convertArgInstructions(inst.Args),
			UserRun: inst.UserRun,
			RootRun: inst.RootRun,
		}
	}

	// Populate Inject if present
	if p.Build.Inject != nil {
		inj := p.Build.Inject
		tctx.Inject = &DockerfileInject{
			AfterFrom:       inj.AfterFrom,
			AfterPackages:   inj.AfterPackages,
			AfterUserSetup:  inj.AfterUserSetup,
			AfterUserSwitch: inj.AfterUserSwitch,
			// Legacy after_claude_install entries render first, at the same
			// position — the key is deprecated, not moved.
			AfterHarnessInstall: append(append([]string{}, inj.AfterClaudeInstall...), inj.AfterHarnessInstall...),
			BeforeEntrypoint:    inj.BeforeEntrypoint,
		}
	}

	return tctx, nil
}

// Conversion helpers from config types to build types

func convertCopyInstructions(src []config.CopyInstruction) []CopyInstruction {
	if src == nil {
		return nil
	}
	result := make([]CopyInstruction, len(src))
	for i, c := range src {
		result[i] = CopyInstruction{
			Src:   c.Src,
			Dest:  c.Dest,
			Chown: c.Chown,
			Chmod: c.Chmod,
		}
	}
	return result
}

func convertArgInstructions(src []config.ArgDefinition) []ArgInstruction {
	if src == nil {
		return nil
	}
	result := make([]ArgInstruction, len(src))
	for i, a := range src {
		result[i] = ArgInstruction{
			Name:    a.Name,
			Default: a.Default,
		}
	}
	return result
}

// firewallCACertPath ensures the firewall CA certificate exists and returns its path.
// If the CA cert doesn't exist yet, it generates a new self-signed CA keypair.
// This guarantees the CA is always available for baking into agent container images,
// regardless of whether the firewall stack has been started before the build.
func (g *ProjectGenerator) firewallCACertPath() (string, error) {
	certDir, err := g.cfg.FirewallCertSubdir()
	if err != nil {
		return "", err
	}

	caCertPath := filepath.Join(certDir, "ca-cert.pem")
	caKeyPath := filepath.Join(certDir, "ca-key.pem")

	// If both files exist, return the cert path.
	if _, err := os.Stat(caCertPath); err == nil {
		if _, err := os.Stat(caKeyPath); err == nil {
			return caCertPath, nil
		}
	}

	// Generate a new CA keypair — the firewall hasn't created one yet.
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return "", fmt.Errorf("creating firewall cert dir: %w", err)
	}

	if err := generateCA(caCertPath, caKeyPath); err != nil {
		return "", fmt.Errorf("generating firewall CA: %w", err)
	}

	return caCertPath, nil
}

// generateCA creates a self-signed CA keypair for Envoy MITM inspection.
// The firewall package has its own EnsureCA that loads/creates the same files;
// this is a standalone copy so the bundler stays a leaf package.
func generateCA(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generating serial: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Clawker Firewall CA"},
		NotBefore:             now,
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("creating CA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return fmt.Errorf("writing CA cert: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshalling CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("writing CA key: %w", err)
	}

	return nil
}

// addFileToTar adds a file to a tar archive.
func addFileToTar(tw *tar.Writer, name string, content []byte) error {
	header := &tar.Header{
		Name: name,
		Mode: tarFileMode,
		Size: int64(len(content)),
	}

	// Make scripts executable
	if strings.HasSuffix(name, ".sh") {
		header.Mode = 0o755
	}

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header for %s: %w", name, err)
	}

	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("failed to write tar content for %s: %w", name, err)
	}

	return nil
}

// harnessVolumeDirs returns the bundle's declared persisted-dir paths,
// normalized home-relative, for the harness-image template's runtime-dirs RUN.
func harnessVolumeDirs(bundle *harness.Bundle) []string {
	dirs := make([]string, 0, len(bundle.Manifest.Volumes))
	for _, v := range bundle.Manifest.Volumes {
		dirs = append(dirs, harness.NormalizeContainerPath(v.Path))
	}
	return dirs
}

// writeBundleAssetsToDir writes a harness bundle's assets/ tree into dir,
// preserving the assets/-prefixed layout the template's COPY instructions
// reference.
func writeBundleAssetsToDir(bundle *harness.Bundle, dir string) error {
	err := bundle.WalkAssets(func(relPath string, content []byte) error {
		dest := filepath.Join(dir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return fmt.Errorf("create asset dir for %s: %w", relPath, err)
		}
		if err := os.WriteFile(dest, content, harness.FileMode(relPath)); err != nil {
			return fmt.Errorf("failed to write %s: %w", relPath, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("stage harness assets: %w", err)
	}
	return nil
}

// GenerateBaseBuildContext builds a tar archive build context for the base
// image (legacy, non-BuildKit builder): the project build-context directory
// plus the rendered base Dockerfile under BaseDockerfileName. The reserved
// name keeps a user's own Dockerfile in the context untouched.
func (g *ProjectGenerator) GenerateBaseBuildContext(dockerfile []byte) (io.Reader, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	if err := tarDirInto(tw, g.GetBuildContext()); err != nil {
		return nil, err
	}

	// Added after the directory walk so this entry wins should the project
	// context improbably contain a file by the same name.
	if err := addFileToTar(tw, BaseDockerfileName, dockerfile); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close base build context tar: %w", err)
	}

	return buf, nil
}

// tarDirInto streams dir's contents into tw, skipping .git and symlinks.
// The caller owns Close on the writer.
func tarDirInto(tw *tar.Writer, dir string) error {
	// Walk the directory using WalkDir (does not follow symlinks)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return fmt.Errorf("relativize %s: %w", path, relErr)
		}

		switch tarEntryAction(relPath, d) {
		case tarEntrySkip:
			return nil
		case tarEntrySkipDir:
			return filepath.SkipDir
		default:
			return writeTarEntry(tw, relPath, path, d)
		}
	})
	if err != nil {
		return fmt.Errorf("walk build context dir: %w", err)
	}
	return nil
}

// tarEntryAction verdicts for a walked build-context entry.
const (
	tarEntryWrite = iota
	tarEntrySkip
	tarEntrySkipDir
)

// tarEntryAction decides how a walked entry is treated: the root and
// symlinks are skipped (symlinks produce broken tar entries), .git is
// pruned entirely, everything else is written.
func tarEntryAction(relPath string, d fs.DirEntry) int {
	if relPath == "." {
		return tarEntrySkip
	}
	if relPath == ".git" || strings.HasPrefix(relPath, ".git/") {
		if d.IsDir() {
			return tarEntrySkipDir
		}
		return tarEntrySkip
	}
	if d.Type()&fs.ModeSymlink != 0 {
		return tarEntrySkip
	}
	return tarEntryWrite
}

// writeTarEntry writes one walked entry (header + content for regular
// files) into the archive under relPath.
func writeTarEntry(tw *tar.Writer, relPath, path string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return fmt.Errorf("stat %s: %w", relPath, err)
	}

	header, headerErr := tar.FileInfoHeader(info, "")
	if headerErr != nil {
		return fmt.Errorf("tar header for %s: %w", relPath, headerErr)
	}
	header.Name = relPath

	if writeErr := tw.WriteHeader(header); writeErr != nil {
		return fmt.Errorf("write tar header for %s: %w", relPath, writeErr)
	}

	if info.Mode().IsRegular() {
		file, openErr := os.Open(path)
		if openErr != nil {
			return fmt.Errorf("open %s: %w", relPath, openErr)
		}
		defer file.Close()

		if _, copyErr := io.Copy(tw, file); copyErr != nil {
			return fmt.Errorf("copy %s into tar: %w", relPath, copyErr)
		}
	}

	return nil
}
