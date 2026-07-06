package bundler

import (
	"archive/tar"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	clawkerdembed "github.com/schmitthub/clawker/clawkerd/embed"
	"github.com/schmitthub/clawker/internal/bundler/registry"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/harness"
	"github.com/schmitthub/clawker/internal/hostproxy/internals"
)

// Embedded assets for Dockerfile generation

//go:embed assets/Dockerfile.tmpl
var dockerfileFS embed.FS

//go:embed assets/Dockerfile.tmpl
var DockerfileTemplate string

// Build-context filenames for clawker-owned assets, referenced by COPY
// instructions in the master Dockerfile template.
const (
	ctxFileHostOpen      = "host-open.sh"
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
	DefaultUsername          = "claude"
	DefaultShell             = "/bin/zsh"
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

// DockerfileManager generates and persists Dockerfiles for each version/variant combination.
type DockerfileManager struct {
	cfg             config.Config
	variantConfig   *VariantConfig
	BuildKitEnabled bool // Enables --mount=type=cache directives in generated Dockerfiles
}

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
	// HarnessVolumeDirs are the harness's declared persisted-dir paths
	// (home-relative), pre-created by the master template's runtime-dirs
	// RUN so the volume mounts inherit correct ownership.
	HarnessVolumeDirs []string
	// HarnessSeeds drive the master template's generic seed-staging section:
	// each seed file is COPY'd into the image's seed dir and listed in the
	// baked seed manifest that CP's generic seed-apply step interprets on
	// first boot.
	HarnessSeeds    []harness.Seed
	IsAlpine        bool
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
	AfterFrom          []string
	AfterPackages      []string
	AfterUserSetup     []string
	AfterUserSwitch    []string
	AfterHarnessInstall []string
	BeforeEntrypoint   []string
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

type DockerFileManagerOptions struct {
	VariantCfg *VariantConfig
}

// NewDockerfileManager creates a new DockerfileManager.
func NewDockerfileManager(cfg config.Config, opts *DockerFileManagerOptions) *DockerfileManager {
	if opts.VariantCfg == nil {
		opts.VariantCfg = DefaultVariantConfig()
	}

	return &DockerfileManager{
		cfg:           cfg,
		variantConfig: opts.VariantCfg,
	}
}

// GenerateDockerfiles generates Dockerfiles for all version/variant combinations.
func (m *DockerfileManager) GenerateDockerfiles(versions *registry.VersionsFile) error {
	dockerfilesDir, err := m.cfg.DockerfilesSubdir()
	if err != nil {
		return fmt.Errorf("failed to resolve dockerfiles directory: %w", err)
	}

	// Parse the template, composing the selected harness bundle's blocks
	// onto the master's declared slots.
	tmplContent, err := dockerfileFS.ReadFile("assets/Dockerfile.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read Dockerfile template: %w", err)
	}

	harnessName, err := ResolveHarnessName(m.cfg, "")
	if err != nil {
		return err
	}
	bundle, err := LoadHarness(m.cfg, harnessName)
	if err != nil {
		return err
	}

	tmpl, err := harness.Compose(string(tmplContent), bundle)
	if err != nil {
		return fmt.Errorf("failed to parse Dockerfile template: %w", err)
	}

	// Write the harness bundle's assets/ tree to the dockerfiles directory.
	if assetsErr := writeBundleAssetsToDir(bundle, dockerfilesDir); assetsErr != nil {
		return assetsErr
	}

	// Write all required scripts to the dockerfiles directory (only once).
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

	for _, script := range scripts {
		scriptPath := filepath.Join(dockerfilesDir, script.name)
		if err := os.WriteFile(scriptPath, script.content, script.mode); err != nil {
			return fmt.Errorf("failed to write %s: %w", script.name, err)
		}
	}

	// Generate Dockerfile for each version/variant combination
	for _, key := range versions.SortedKeys() {
		info := (*versions)[key]
		for variant := range info.Variants {
			filename := fmt.Sprintf("%s-%s.dockerfile", info.FullVersion, variant)
			path := filepath.Join(dockerfilesDir, filename)

			ctx, err := m.createContext(info.FullVersion, variant)
			if err != nil {
				return fmt.Errorf("failed to create context for %s-%s: %w", info.FullVersion, variant, err)
			}
			ctx.HarnessVolumeDirs = harnessVolumeDirs(bundle)
			ctx.HarnessSeeds = bundle.Manifest.Seeds
			content, err := m.renderDockerfile(tmpl, ctx)
			if err != nil {
				return fmt.Errorf("failed to render Dockerfile for %s-%s: %w", info.FullVersion, variant, err)
			}

			//nolint:gosec // generated Dockerfile, not a secret
			if writeErr := os.WriteFile(path, content, 0o644); writeErr != nil {
				return fmt.Errorf("failed to write Dockerfile %s: %w", path, writeErr)
			}
		}
	}

	return nil
}

// createContext creates a DockerfileContext for a given version and variant.
func (m *DockerfileManager) createContext(version, variant string) (*DockerfileContext, error) {
	isAlpine := m.variantConfig.IsAlpine(variant)
	baseImage := m.variantToBaseImage(variant)

	// OTEL telemetry from monitoring config
	mon := m.cfg.MonitoringConfig()

	return &DockerfileContext{
		BaseImage:                baseImage,
		Packages:                 []string{}, // Base packages are in template
		Username:                 DefaultUsername,
		UID:                      m.cfg.ContainerUID(),
		GID:                      m.cfg.ContainerGID(),
		Shell:                    "/bin/zsh",
		WorkspacePath:            "/workspace",
		HarnessVersion:           version,
		IsAlpine:                 isAlpine,
		BuildKitEnabled:          m.BuildKitEnabled,
		Instructions:             nil,
		Inject:                   nil,
		OtelEndpoint:             m.cfg.OtelCollectorURL(),
		OtelLogsExportInterval:   mon.Telemetry.LogsExportIntervalMs,
		OtelMetricExportInterval: mon.Telemetry.MetricExportIntervalMs,
		OtelLogToolDetails:       *mon.Telemetry.LogToolDetails,
		OtelLogUserPrompts:       *mon.Telemetry.LogUserPrompts,
		OtelIncludeAccountUUID:   *mon.Telemetry.IncludeAccountUUID,
		OtelIncludeSessionID:     *mon.Telemetry.IncludeSessionID,
		GoBuilderImage:           DefaultGoBuilderImage,
	}, nil
}

// variantToBaseImage converts a variant name to a Docker base image.
func (m *DockerfileManager) variantToBaseImage(variant string) string {
	if m.variantConfig.IsAlpine(variant) {
		// Convert "alpine3.23" to "alpine:3.23"
		alpineVersion := strings.TrimPrefix(variant, "alpine")
		return fmt.Sprintf("alpine:%s", alpineVersion)
	}
	// Debian variants use buildpack-deps
	return fmt.Sprintf("buildpack-deps:%s-scm", variant)
}

// renderDockerfile renders the Dockerfile template with the given context.
func (m *DockerfileManager) renderDockerfile(tmpl *template.Template, ctx *DockerfileContext) ([]byte, error) {
	var buf strings.Builder
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// DockerfilesDir returns the path to the dockerfiles directory.
// Delegates to cfg.DockerfilesSubdir() as the single source of truth.
func (m *DockerfileManager) DockerfilesDir() (string, error) {
	return m.cfg.DockerfilesSubdir()
}

// ProjectGenerator creates Dockerfiles dynamically from project configuration (clawker.yaml).
type ProjectGenerator struct {
	cfg             config.Config
	workDir         string
	BuildKitEnabled bool // Enables --mount=type=cache directives in generated Dockerfiles
	// HarnessVersion is the concrete version string baked into the rendered
	// ARG default (e.g. "2.1.5"). The npm-registry resolution that turns the
	// "latest" dist-tag into a concrete version happens at the command layer
	// via bundler.ResolveLatestHarnessVersion — bundler itself doesn't fetch.
	// Empty means use DefaultHarnessVersion ("latest") as a literal.
	HarnessVersion string
	// Harness selects the harness bundle whose template blocks and context
	// files compose the rendered Dockerfile. Empty means the registry entry
	// marked default (falling back to DefaultHarnessName).
	Harness string

	bundle *harness.Bundle // lazily loaded via harnessBundle()
}

// harnessBundle resolves and caches the selected harness bundle.
func (g *ProjectGenerator) harnessBundle() (*harness.Bundle, error) {
	if g.bundle != nil {
		return g.bundle, nil
	}
	name, err := ResolveHarnessName(g.cfg, g.Harness)
	if err != nil {
		return nil, err
	}
	b, err := LoadHarness(g.cfg, name)
	if err != nil {
		return nil, err
	}
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

// Generate creates a Dockerfile based on the project configuration. The
// HarnessVersion baked into the rendered ARG default comes from the
// generator's HarnessVersion field (set by callers from the resolved
// npm version) — empty falls back to DefaultHarnessVersion literal.
func (g *ProjectGenerator) Generate() ([]byte, error) {
	tctx, err := g.buildContext()
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}

	bundle, err := g.harnessBundle()
	if err != nil {
		return nil, err
	}

	tmpl, err := harness.Compose(DockerfileTemplate, bundle)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dockerfile template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, tctx); err != nil {
		return nil, fmt.Errorf("failed to render Dockerfile template: %w", err)
	}

	return buf.Bytes(), nil
}

// GenerateBuildContext creates a tar archive containing the Dockerfile and scripts.
func (g *ProjectGenerator) GenerateBuildContext() (io.Reader, error) {
	dockerfile, err := g.Generate()
	if err != nil {
		return nil, err
	}
	return g.GenerateBuildContextFromDockerfile(dockerfile)
}

// GenerateBuildContextFromDockerfile builds a tar archive build context using
// pre-rendered Dockerfile bytes. This avoids re-generating the Dockerfile when
// the caller already has it.
func (g *ProjectGenerator) GenerateBuildContextFromDockerfile(dockerfile []byte) (io.Reader, error) {
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
	if caCertPath, err := g.firewallCACertPath(); err == nil && caCertPath != "" {
		content, err := os.ReadFile(caCertPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read firewall CA cert: %w", err)
		}
		if err := addFileToTar(tw, "clawker-ca.crt", content); err != nil {
			return nil, err
		}
	}

	// Add host-open script for opening URLs on host machine
	if err := addFileToTar(tw, ctxFileHostOpen, []byte(HostOpenScript)); err != nil {
		return nil, err
	}

	// Add callback-forwarder Go source for compilation in multi-stage build
	if err := addFileToTar(tw, ctxFileCallbackFwd, []byte(CallbackForwarderSource)); err != nil {
		return nil, err
	}

	// Add git-credential-clawker script for git credential forwarding
	if err := addFileToTar(tw, ctxFileGitCredential, []byte(GitCredentialScript)); err != nil {
		return nil, err
	}

	// Add clawker-socket-server source for compilation in multi-stage build
	if err := addFileToTar(tw, ctxFileSocketServer, []byte(SocketForwarderSource)); err != nil {
		return nil, err
	}

	// Add the clawkerd binary itself — it's pre-compiled in the
	// clawker CLI release and dropped into every per-project image
	// at /usr/local/bin/clawkerd by the Dockerfile.
	if err := addFileToTar(tw, ctxFileClawkerd, clawkerdembed.Binary); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf, nil
}

// WriteBuildContextToDir writes the Dockerfile and all supporting scripts to a
// directory on disk. BuildKit requires files on the filesystem (not a tar stream)
// because it creates fsutil.FS mounts from directory paths.
func (g *ProjectGenerator) WriteBuildContextToDir(dir string, dockerfile []byte) error {
	// Write Dockerfile
	//nolint:gosec // generated Dockerfile, not a secret
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfile, 0o644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// Write the harness bundle's assets/ tree (mirrors GenerateBuildContextFromDockerfile).
	bundle, err := g.harnessBundle()
	if err != nil {
		return err
	}
	if walkErr := writeBundleAssetsToDir(bundle, dir); walkErr != nil {
		return walkErr
	}

	// Write all supporting scripts (mirrors GenerateBuildContextFromDockerfile).
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

// UseCustomDockerfile checks if a custom Dockerfile should be used.
func (g *ProjectGenerator) UseCustomDockerfile() bool {
	return g.cfg.Project().Build.Dockerfile != ""
}

// GetCustomDockerfilePath returns the path to the custom Dockerfile.
func (g *ProjectGenerator) GetCustomDockerfilePath() string {
	path := g.cfg.Project().Build.Dockerfile
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.workDir, path)
	}
	return path
}

// GetBuildContext returns the build context path.
func (g *ProjectGenerator) GetBuildContext() string {
	if g.cfg.Project().Build.Context != "" {
		path := g.cfg.Project().Build.Context
		if !filepath.IsAbs(path) {
			path = filepath.Join(g.workDir, path)
		}
		return path
	}
	return g.workDir
}

// basePackagesAlpine are packages already included in the template for Alpine.
var basePackagesAlpine = map[string]bool{
	"bash": true, "less": true, "git": true, "procps": true, "sudo": true,
	"fzf": true, "zsh": true, "man-db": true, "unzip": true, "gnupg": true,
	"iproute2": true, "bind-tools": true,
	"jq": true, "nano": true, "vim": true, "wget": true, "curl": true,
	"github-cli": true, "musl-locales": true, "musl-locales-lang": true,
}

// basePackagesDebian are packages already included in the template for Debian.
var basePackagesDebian = map[string]bool{
	"less": true, "git": true, "procps": true, "sudo": true, "fzf": true,
	"zsh": true, "man-db": true, "unzip": true, "gnupg2": true,
	"iproute2": true, "dnsutils": true,
	"aggregate": true, "jq": true, "nano": true, "vim": true, "wget": true,
	"curl": true, "gh": true, "locales": true, "locales-all": true,
}

// filterBasePackages removes packages that are already in the template.
func filterBasePackages(packages []string, isAlpine bool) []string {
	basePackages := basePackagesDebian
	if isAlpine {
		basePackages = basePackagesAlpine
	}

	var filtered []string
	for _, pkg := range packages {
		if !basePackages[pkg] {
			filtered = append(filtered, pkg)
		}
	}
	return filtered
}

// buildContext creates the template context from config.
func (g *ProjectGenerator) buildContext() (*DockerfileContext, error) {
	p := g.cfg.Project()
	baseImage := p.Build.Image
	if baseImage == "" {
		return nil, ErrNoBuildImage
	}

	isAlpine := strings.Contains(strings.ToLower(baseImage), "alpine")

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
		BaseImage:                baseImage,
		Packages:                 filterBasePackages(p.Build.Packages, isAlpine),
		Username:                 DefaultUsername,
		UID:                      g.cfg.ContainerUID(),
		GID:                      g.cfg.ContainerGID(),
		Shell:                    DefaultShell,
		WorkspacePath:            "/workspace",
		HarnessVersion:           harnessVersion,
		HarnessVolumeDirs:        harnessVolumeDirs(bundle),
		HarnessSeeds:             bundle.Manifest.Seeds,
		IsAlpine:                 isAlpine,
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
			AfterFrom:          inj.AfterFrom,
			AfterPackages:      inj.AfterPackages,
			AfterUserSetup:     inj.AfterUserSetup,
			AfterUserSwitch:    inj.AfterUserSwitch,
			// Legacy after_claude_install entries render first, at the same
			// position — the key is deprecated, not moved.
			AfterHarnessInstall: append(append([]string{}, inj.AfterClaudeInstall...), inj.AfterHarnessInstall...),
			BeforeEntrypoint:   inj.BeforeEntrypoint,
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
// normalized home-relative, for the master template's runtime-dirs RUN.
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

// CreateBuildContextFromDir creates a tar archive from a directory for custom Dockerfiles.
func CreateBuildContextFromDir(dir string, dockerfilePath string) (io.Reader, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	// Walk the directory using WalkDir (does not follow symlinks)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		// Skip root directory
		if relPath == "." {
			return nil
		}

		// Skip .git directory
		if relPath == ".git" || strings.HasPrefix(relPath, ".git/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks — they produce broken entries in tar archives
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Write file content if it's a regular file
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf, nil
}
