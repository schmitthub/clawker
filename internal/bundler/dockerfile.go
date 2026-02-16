package bundler

import (
	"archive/tar"
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

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

//go:embed assets/init-firewall.sh
var FirewallScript string

//go:embed assets/statusline.sh
var StatuslineScript string

//go:embed assets/claude-settings.json
var SettingsFile string

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
	DefaultUID               = config.ContainerUID
	DefaultGID               = config.ContainerGID
	DefaultShell             = "/bin/zsh"
)

// DockerfileManager generates and persists Dockerfiles for each version/variant combination.
type DockerfileManager struct {
	outputDir       string
	config          *VariantConfig
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
	EnableFirewall  bool
	BuildKitEnabled bool
	Instructions    *DockerfileInstructions
	Inject          *DockerfileInject

	// OTEL telemetry endpoints — populated from config.MonitoringConfig
	OtelMetricsEndpoint      string // e.g. "http://otel-collector:4318/v1/metrics"
	OtelLogsEndpoint         string // e.g. "http://otel-collector:4318/v1/logs"
	OtelLogsExportInterval   int    // milliseconds, e.g. 5000
	OtelMetricExportInterval int    // milliseconds, e.g. 10000
}

// DockerfileInstructions contains type-safe Dockerfile instructions.
// Only structural instructions that affect the image filesystem are included.
// Non-structural instructions (ENV, Labels, EXPOSE, VOLUME, HEALTHCHECK, SHELL)
// are injected at container creation time or via the Docker build API.
type DockerfileInstructions struct {
	Copy    []CopyInstruction
	Args    []ArgInstruction
	UserRun []RunInstruction
	RootRun []RunInstruction
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

// RunInstruction represents a RUN instruction with OS variants.
type RunInstruction struct {
	Cmd    string
	Alpine string
	Debian string
}

// NewDockerfileManager creates a new DockerfileManager.
func NewDockerfileManager(outputDir string, cfg *VariantConfig) *DockerfileManager {
	if cfg == nil {
		cfg = DefaultVariantConfig()
	}
	return &DockerfileManager{
		outputDir: outputDir,
		config:    cfg,
	}
}

// GenerateDockerfiles generates Dockerfiles for all version/variant combinations.
func (m *DockerfileManager) GenerateDockerfiles(versions *registry.VersionsFile) error {
	dockerfilesDir := filepath.Join(m.outputDir, "dockerfiles")
	if err := config.EnsureDir(dockerfilesDir); err != nil {
		return fmt.Errorf("failed to create dockerfiles directory: %w", err)
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
		{"init-firewall.sh", FirewallScript, 0755},
		{"statusline.sh", StatuslineScript, 0755},
		{"claude-settings.json", SettingsFile, 0644},
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

// createContext creates a DockerfileContext for a given version and variant.
func (m *DockerfileManager) createContext(version, variant string) (*DockerfileContext, error) {
	isAlpine := m.config.IsAlpine(variant)
	baseImage := m.variantToBaseImage(variant)

	// OTEL endpoint defaults from monitoring config
	defaults := config.DefaultSettings()
	otelInternalURL := defaults.Monitoring.OtelCollectorInternalURL()

	return &DockerfileContext{
		BaseImage:                baseImage,
		Packages:                 []string{}, // Base packages are in template
		Username:                 DefaultUsername,
		UID:                      DefaultUID,
		GID:                      DefaultGID,
		Shell:                    "/bin/zsh",
		WorkspacePath:            "/workspace",
		ClaudeVersion:            version,
		IsAlpine:                 isAlpine,
		EnableFirewall:           true,
		BuildKitEnabled:          m.BuildKitEnabled,
		Instructions:             nil,
		Inject:                   nil,
		OtelMetricsEndpoint:      otelInternalURL + "/v1/metrics",
		OtelLogsEndpoint:         otelInternalURL + "/v1/logs",
		OtelLogsExportInterval:   5000,
		OtelMetricExportInterval: 10000,
	}, nil
}

// variantToBaseImage converts a variant name to a Docker base image.
func (m *DockerfileManager) variantToBaseImage(variant string) string {
	if m.config.IsAlpine(variant) {
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
func (m *DockerfileManager) DockerfilesDir() string {
	return filepath.Join(m.outputDir, "dockerfiles")
}

// ProjectGenerator creates Dockerfiles dynamically from project configuration (clawker.yaml).
type ProjectGenerator struct {
	config          *config.Config // gateway — provides Project and Settings
	workDir         string
	BuildKitEnabled bool // Enables --mount=type=cache directives in generated Dockerfiles
}

// NewProjectGenerator creates a new project Dockerfile generator.
func NewProjectGenerator(cfg *config.Config, workDir string) *ProjectGenerator {
	return &ProjectGenerator{
		config:  cfg,
		workDir: workDir,
	}
}

// effectiveSettings returns the loaded settings from the config gateway,
// falling back to DefaultSettings() when no gateway is available (e.g. tests).
func (g *ProjectGenerator) effectiveSettings() *config.Settings {
	if g.config != nil && g.config.Settings != nil {
		return g.config.Settings
	}
	return config.DefaultSettings()
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

	// Add firewall script if enabled
	if g.config.Project.Security.FirewallEnabled() {
		if err := addFileToTar(tw, "init-firewall.sh", []byte(FirewallScript)); err != nil {
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

	// Add any include files from agent config
	for _, include := range g.config.Project.Agent.Includes {
		includePath := include
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(g.workDir, includePath)
		}

		content, err := os.ReadFile(includePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read include file %q: %w", include, err)
		}

		// Add to archive with relative path
		if err := addFileToTar(tw, filepath.Base(include), content); err != nil {
			return nil, err
		}
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
		{"statusline.sh", StatuslineScript, 0755},
		{"claude-settings.json", SettingsFile, 0644},
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

	// Write firewall script if enabled
	if g.config.Project.Security.FirewallEnabled() {
		if err := os.WriteFile(filepath.Join(dir, "init-firewall.sh"), []byte(FirewallScript), 0755); err != nil {
			return fmt.Errorf("failed to write init-firewall.sh: %w", err)
		}
	}

	// Write include files from agent config
	for _, include := range g.config.Project.Agent.Includes {
		includePath := include
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(g.workDir, includePath)
		}

		content, err := os.ReadFile(includePath)
		if err != nil {
			return fmt.Errorf("failed to read include file %q: %w", include, err)
		}

		if err := os.WriteFile(filepath.Join(dir, filepath.Base(include)), content, 0644); err != nil {
			return fmt.Errorf("failed to write include %s: %w", include, err)
		}
	}

	return nil
}

// UseCustomDockerfile checks if a custom Dockerfile should be used.
func (g *ProjectGenerator) UseCustomDockerfile() bool {
	return g.config.Project.Build.Dockerfile != ""
}

// GetCustomDockerfilePath returns the path to the custom Dockerfile.
func (g *ProjectGenerator) GetCustomDockerfilePath() string {
	path := g.config.Project.Build.Dockerfile
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.workDir, path)
	}
	return path
}

// GetBuildContext returns the build context path.
func (g *ProjectGenerator) GetBuildContext() string {
	if g.config.Project.Build.Context != "" {
		path := g.config.Project.Build.Context
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
	p := g.config.Project
	baseImage := p.Build.Image
	if baseImage == "" {
		baseImage = "buildpack-deps:bookworm-scm"
	}

	isAlpine := strings.Contains(strings.ToLower(baseImage), "alpine")

	// OTEL endpoints from effective settings (user overrides respected)
	settings := g.effectiveSettings()
	otelInternalURL := settings.Monitoring.OtelCollectorInternalURL()

	ctx := &DockerfileContext{
		BaseImage:                baseImage,
		Packages:                 filterBasePackages(p.Build.Packages, isAlpine),
		Username:                 DefaultUsername,
		UID:                      DefaultUID,
		GID:                      DefaultGID,
		Shell:                    DefaultShell,
		WorkspacePath:            p.Workspace.RemotePath,
		ClaudeVersion:            DefaultClaudeCodeVersion,
		IsAlpine:                 isAlpine,
		EnableFirewall:           p.Security.FirewallEnabled(),
		BuildKitEnabled:          g.BuildKitEnabled,
		OtelMetricsEndpoint:      otelInternalURL + "/v1/metrics",
		OtelLogsEndpoint:         otelInternalURL + "/v1/logs",
		OtelLogsExportInterval:   5000,
		OtelMetricExportInterval: 10000,
	}

	// Populate Instructions if present (structural only — Copy, Args, RUN)
	if p.Build.Instructions != nil {
		inst := p.Build.Instructions
		ctx.Instructions = &DockerfileInstructions{
			Copy:    convertCopyInstructions(inst.Copy),
			Args:    convertArgInstructions(inst.Args),
			UserRun: convertRunInstructions(inst.UserRun),
			RootRun: convertRunInstructions(inst.RootRun),
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

func convertRunInstructions(src []config.RunInstruction) []RunInstruction {
	if src == nil {
		return nil
	}
	result := make([]RunInstruction, len(src))
	for i, r := range src {
		result[i] = RunInstruction{
			Cmd:    r.Cmd,
			Alpine: r.Alpine,
			Debian: r.Debian,
		}
	}
	return result
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
