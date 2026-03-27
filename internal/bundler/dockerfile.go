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
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/schmitthub/clawker/internal/bundler/registry"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/hostproxy/internals"
)

// Embedded assets for Dockerfile generation
//
// IMPORTANT: All scripts in assets/ are automatically included in image
// content hashing via EmbeddedScripts(). New scripts added to this directory
// will be discovered automatically without manual list maintenance.

//go:embed assets/*
var assetsFS embed.FS

//go:embed assets/Dockerfile.tmpl
var dockerfileFS embed.FS

//go:embed assets/Dockerfile.tmpl
var DockerfileTemplate string

//go:embed assets/entrypoint.sh
var EntrypointScript string

//go:embed assets/firewall.sh
var FirewallScript string

//go:embed assets/statusline.sh
var StatuslineScript string

//go:embed assets/claude-settings.json
var SettingsFile string

//go:embed assets/claude-config.json
var ConfigFile string

//go:embed assets/clawker-agent-prompt.md
var AgentPromptFile string

// Re-export hostproxy container scripts from internal/hostproxy/internals.
// These were previously embedded directly in this package but now live
// alongside the hostproxy code they interact with.
var (
	HostOpenScript          = internals.HostOpenScript
	CallbackForwarderSource = internals.CallbackForwarderSource
	GitCredentialScript     = internals.GitCredentialScript
	SocketForwarderSource   = internals.SocketForwarderSource
)

// EmbeddedScripts returns all embedded script contents for content hashing.
// Scripts are read dynamically from embed.FS to ensure new scripts are
// automatically included without manual list maintenance.
//
// IMPORTANT: This function includes ALL scripts that affect the built image:
//   - Bundler assets (assets/*) are auto-discovered via embed.FS
//   - Hostproxy container scripts are included via internals.AllScripts()
//
// New scripts added to either location will be automatically included.
// Scripts are sorted for deterministic hashing.
func EmbeddedScripts() []string {
	var scripts []string

	// Read bundler assets dynamically from embed.FS
	entries, _ := fs.ReadDir(assetsFS, "assets")
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, _ := fs.ReadFile(assetsFS, "assets/"+entry.Name())
		scripts = append(scripts, string(content))
	}

	// Add hostproxy container scripts via AllScripts()
	scripts = append(scripts, internals.AllScripts()...)

	// Sort for deterministic hashing
	sort.Strings(scripts)
	return scripts
}

// Default values for container configuration
const (
	DefaultClaudeCodeVersion = "latest"
	DefaultUsername          = "claude"
	DefaultShell             = "/bin/zsh"
	// DefaultGoBuilderImage is the Go toolchain image used for builder stages.
	// Pinned to exact patch version + SHA digest matching go.mod (go 1.25.5).
	DefaultGoBuilderImage = "golang:1.25.5-alpine@sha256:ac09a5f469f307e5da71e766b0bd59c9c49ea460a528cc3e6686513d64a6f1fb"
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
	BaseImage       string
	Packages        []string
	Username        string
	UID             int
	GID             int
	Shell           string
	WorkspacePath   string
	ClaudeVersion   string
	IsAlpine        bool
	BuildKitEnabled bool
	Instructions    *DockerfileInstructions
	Inject          *DockerfileInject

	// OTEL telemetry endpoints — populated from config.MonitoringConfig
	OtelMetricsEndpoint      string // e.g. "http://otel-collector:4318/v1/metrics"
	OtelLogsEndpoint         string // e.g. "http://otel-collector:4318/v1/logs"
	OtelLogsExportInterval   int    // milliseconds, e.g. 5000
	OtelMetricExportInterval int    // milliseconds, e.g. 10000

	// OTEL feature flags — populated from config.TelemetryConfig
	OtelLogToolDetails     bool // OTEL_LOG_TOOL_DETAILS=1
	OtelLogUserPrompts     bool // OTEL_LOG_USER_PROMPTS=1
	OtelIncludeAccountUUID bool // OTEL_METRICS_INCLUDE_ACCOUNT_UUID=true
	OtelIncludeSessionID   bool // OTEL_METRICS_INCLUDE_SESSION_ID=true

	HasFirewallCA  bool   // CA cert exists for MITM inspection
	GoBuilderImage string // Go toolchain image for builder stages (e.g. "golang:1.25.5-alpine@sha256:...")
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
	AfterClaudeInstall []string
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

	// Parse the template
	tmplContent, err := dockerfileFS.ReadFile("assets/Dockerfile.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read Dockerfile template: %w", err)
	}

	tmpl, err := template.New("Dockerfile").Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse Dockerfile template: %w", err)
	}

	// Write all required scripts to the dockerfiles directory (only once)
	scripts := []struct {
		name    string
		content string
		mode    os.FileMode
	}{
		{"entrypoint.sh", EntrypointScript, 0755},
		{"firewall.sh", FirewallScript, 0755},
		{"clawker-agent-prompt.md", AgentPromptFile, 0644},
		{"statusline.sh", StatuslineScript, 0755},
		{"claude-settings.json", SettingsFile, 0644},
		{"claude-config.json", ConfigFile, 0644},
		{"host-open.sh", HostOpenScript, 0755},
		{"callback-forwarder.go", CallbackForwarderSource, 0644},
		{"git-credential-clawker.sh", GitCredentialScript, 0755},
		{"clawker-socket-server.go", SocketForwarderSource, 0644},
	}

	for _, script := range scripts {
		scriptPath := filepath.Join(dockerfilesDir, script.name)
		if err := os.WriteFile(scriptPath, []byte(script.content), script.mode); err != nil {
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
			content, err := m.renderDockerfile(tmpl, ctx)
			if err != nil {
				return fmt.Errorf("failed to render Dockerfile for %s-%s: %w", info.FullVersion, variant, err)
			}

			if err := os.WriteFile(path, content, 0644); err != nil {
				return fmt.Errorf("failed to write Dockerfile %s: %w", path, err)
			}
		}
	}

	return nil
}

// otelBaseEndpoint returns the OTEL collector base URL.
// Uses OtelCollectorEndpoint if set, otherwise constructs from internal host + port.
func otelBaseEndpoint(mon config.MonitoringConfig) string {
	if mon.OtelCollectorEndpoint != "" {
		return mon.OtelCollectorEndpoint
	}
	return "http://" + net.JoinHostPort(mon.OtelCollectorInternal, strconv.Itoa(mon.OtelCollectorPort))
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
		ClaudeVersion:            version,
		IsAlpine:                 isAlpine,
		BuildKitEnabled:          m.BuildKitEnabled,
		Instructions:             nil,
		Inject:                   nil,
		OtelMetricsEndpoint:      otelBaseEndpoint(mon) + mon.Telemetry.MetricsPath,
		OtelLogsEndpoint:         otelBaseEndpoint(mon) + mon.Telemetry.LogsPath,
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
}

// NewProjectGenerator creates a new project Dockerfile generator.
func NewProjectGenerator(cfg config.Config, workDir string) *ProjectGenerator {
	return &ProjectGenerator{
		cfg:     cfg,
		workDir: workDir,
	}
}

// Generate creates a Dockerfile based on the project configuration.
func (g *ProjectGenerator) Generate() ([]byte, error) {
	ctx, err := g.buildContext()
	if err != nil {
		return nil, fmt.Errorf("failed to build context: %w", err)
	}

	tmpl, err := template.New("Dockerfile").Parse(DockerfileTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dockerfile template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
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
// the caller (e.g. EnsureImage) has already rendered it for content hashing.
func (g *ProjectGenerator) GenerateBuildContextFromDockerfile(dockerfile []byte) (io.Reader, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	// Add Dockerfile to archive
	if err := addFileToTar(tw, "Dockerfile", dockerfile); err != nil {
		return nil, err
	}

	// Add entrypoint script
	if err := addFileToTar(tw, "entrypoint.sh", []byte(EntrypointScript)); err != nil {
		return nil, err
	}

	// Add statusline script
	if err := addFileToTar(tw, "statusline.sh", []byte(StatuslineScript)); err != nil {
		return nil, err
	}

	// Add settings file json
	if err := addFileToTar(tw, "claude-settings.json", []byte(SettingsFile)); err != nil {
		return nil, err
	}

	// Add claude config seed (onboarding bypass + session pointer persistence)
	if err := addFileToTar(tw, "claude-config.json", []byte(ConfigFile)); err != nil {
		return nil, err
	}

	// Add firewall script (always included; execution gated at runtime)
	if err := addFileToTar(tw, "firewall.sh", []byte(FirewallScript)); err != nil {
		return nil, err
	}

	// Add agent prompt file for in-container agent awareness
	if err := addFileToTar(tw, "clawker-agent-prompt.md", []byte(AgentPromptFile)); err != nil {
		return nil, err
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
	if err := addFileToTar(tw, "host-open.sh", []byte(HostOpenScript)); err != nil {
		return nil, err
	}

	// Add callback-forwarder Go source for compilation in multi-stage build
	if err := addFileToTar(tw, "callback-forwarder.go", []byte(CallbackForwarderSource)); err != nil {
		return nil, err
	}

	// Add git-credential-clawker script for git credential forwarding
	if err := addFileToTar(tw, "git-credential-clawker.sh", []byte(GitCredentialScript)); err != nil {
		return nil, err
	}

	// Add clawker-socket-server source for compilation in multi-stage build
	if err := addFileToTar(tw, "clawker-socket-server.go", []byte(SocketForwarderSource)); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), dockerfile, 0644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// Write all supporting scripts (mirrors GenerateBuildContextFromDockerfile)
	scripts := []struct {
		name    string
		content string
		mode    os.FileMode
	}{
		{"entrypoint.sh", EntrypointScript, 0755},
		{"firewall.sh", FirewallScript, 0755},
		{"clawker-agent-prompt.md", AgentPromptFile, 0644},
		{"statusline.sh", StatuslineScript, 0755},
		{"claude-settings.json", SettingsFile, 0644},
		{"claude-config.json", ConfigFile, 0644},
		{"host-open.sh", HostOpenScript, 0755},
		{"callback-forwarder.go", CallbackForwarderSource, 0644},
		{"git-credential-clawker.sh", GitCredentialScript, 0755},
		{"clawker-socket-server.go", SocketForwarderSource, 0644},
	}

	for _, s := range scripts {
		if err := os.WriteFile(filepath.Join(dir, s.name), []byte(s.content), s.mode); err != nil {
			return fmt.Errorf("failed to write %s: %w", s.name, err)
		}
	}

	// Conditionally write firewall CA cert for MITM inspection
	if caCertPath, err := g.firewallCACertPath(); err == nil && caCertPath != "" {
		content, err := os.ReadFile(caCertPath)
		if err != nil {
			return fmt.Errorf("failed to read firewall CA cert: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "clawker-ca.crt"), content, 0644); err != nil {
			return fmt.Errorf("failed to write clawker-ca.crt: %w", err)
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
	"iptables": true, "ipset": true, "iproute2": true, "bind-tools": true,
	"jq": true, "nano": true, "vim": true, "wget": true, "curl": true,
	"github-cli": true, "musl-locales": true, "musl-locales-lang": true,
}

// basePackagesDebian are packages already included in the template for Debian.
var basePackagesDebian = map[string]bool{
	"less": true, "git": true, "procps": true, "sudo": true, "fzf": true,
	"zsh": true, "man-db": true, "unzip": true, "gnupg2": true,
	"iptables": true, "ipset": true, "iproute2": true, "dnsutils": true,
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

	ctx := &DockerfileContext{
		BaseImage:                baseImage,
		Packages:                 filterBasePackages(p.Build.Packages, isAlpine),
		Username:                 DefaultUsername,
		UID:                      g.cfg.ContainerUID(),
		GID:                      g.cfg.ContainerGID(),
		Shell:                    DefaultShell,
		WorkspacePath:            "/workspace",
		ClaudeVersion:            DefaultClaudeCodeVersion,
		IsAlpine:                 isAlpine,
		BuildKitEnabled:          g.BuildKitEnabled,
		HasFirewallCA:            hasFirewallCA,
		OtelMetricsEndpoint:      otelBaseEndpoint(mon) + mon.Telemetry.MetricsPath,
		OtelLogsEndpoint:         otelBaseEndpoint(mon) + mon.Telemetry.LogsPath,
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
		ctx.Instructions = &DockerfileInstructions{
			Copy:    convertCopyInstructions(inst.Copy),
			Args:    convertArgInstructions(inst.Args),
			UserRun: inst.UserRun,
			RootRun: inst.RootRun,
		}
	}

	// Populate Inject if present
	if p.Build.Inject != nil {
		inj := p.Build.Inject
		ctx.Inject = &DockerfileInject{
			AfterFrom:          inj.AfterFrom,
			AfterPackages:      inj.AfterPackages,
			AfterUserSetup:     inj.AfterUserSetup,
			AfterUserSwitch:    inj.AfterUserSwitch,
			AfterClaudeInstall: inj.AfterClaudeInstall,
			BeforeEntrypoint:   inj.BeforeEntrypoint,
		}
	}

	return ctx, nil
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
		Mode: 0644,
		Size: int64(len(content)),
	}

	// Make scripts executable
	if strings.HasSuffix(name, ".sh") {
		header.Mode = 0755
	}

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header for %s: %w", name, err)
	}

	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("failed to write tar content for %s: %w", name, err)
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
