package build

import (
	"archive/tar"
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/build/registry"
	"github.com/schmitthub/clawker/pkg/logger"
)

// Embedded templates for Dockerfile generation

//go:embed templates/Dockerfile.tmpl
var dockerfileFS embed.FS

//go:embed templates/Dockerfile.tmpl
var DockerfileTemplate string

//go:embed templates/entrypoint.sh
var EntrypointScript string

//go:embed templates/init-firewall.sh
var FirewallScript string

//go:embed templates/statusline.sh
var StatuslineScript string

//go:embed templates/claude-settings.json
var SettingsFile string

//go:embed templates/host-open.sh
var HostOpenScript string

//go:embed templates/callback-forwarder.sh
var CallbackForwarderScript string

//go:embed templates/git-credential-clawker.sh
var GitCredentialScript string

// Default values for container configuration
const (
	DefaultClaudeCodeVersion = "latest"
	DefaultUsername          = "claude"
	DefaultUID               = 1001
	DefaultGID               = 1001
	DefaultShell             = "/bin/zsh"
)

// DockerfileManager generates and persists Dockerfiles for each version/variant combination.
type DockerfileManager struct {
	outputDir string
	config    *VariantConfig
}

// DockerfileContext contains the template data for generating a Dockerfile.
type DockerfileContext struct {
	BaseImage      string
	Packages       []string
	Username       string
	UID            int
	GID            int
	Shell          string
	WorkspacePath  string
	ClaudeVersion  string
	IsAlpine       bool
	EnableFirewall bool
	ExtraEnv       map[string]string
	Editor         string
	Visual         string
	Instructions   *DockerfileInstructions
	Inject         *DockerfileInject
	ImageLabels    map[string]string // Clawker internal labels (com.clawker.*)
}

// DockerfileInstructions contains type-safe Dockerfile instructions.
type DockerfileInstructions struct {
	Copy        []CopyInstruction
	Env         map[string]string
	Labels      map[string]string
	Expose      []ExposeInstruction
	Args        []ArgInstruction
	Volumes     []string
	Workdir     string
	Healthcheck *HealthcheckInstruction
	Shell       []string
	UserRun     []RunInstruction
	RootRun     []RunInstruction
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

// ExposeInstruction represents an EXPOSE instruction.
type ExposeInstruction struct {
	Port     int
	Protocol string
}

// ArgInstruction represents an ARG instruction.
type ArgInstruction struct {
	Name    string
	Default string
}

// HealthcheckInstruction represents a HEALTHCHECK instruction.
type HealthcheckInstruction struct {
	Cmd         []string
	Interval    string
	Timeout     string
	Retries     int
	StartPeriod string
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
	tmplContent, err := dockerfileFS.ReadFile("templates/Dockerfile.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read Dockerfile template: %w", err)
	}

	tmpl, err := template.New("Dockerfile").Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse Dockerfile template: %w", err)
	}

	// Generate Dockerfile for each version/variant combination
	for _, key := range versions.SortedKeys() {
		info := (*versions)[key]
		for variant := range info.Variants {
			filename := fmt.Sprintf("%s-%s.dockerfile", info.FullVersion, variant)
			path := filepath.Join(dockerfilesDir, filename)

			ctx := m.createContext(info.FullVersion, variant)
			content, err := m.renderDockerfile(tmpl, ctx)
			if err != nil {
				return fmt.Errorf("failed to render Dockerfile for %s-%s: %w", info.FullVersion, variant, err)
			}

			if err := os.WriteFile(path, content, 0644); err != nil {
				return fmt.Errorf("failed to write Dockerfile %s: %w", path, err)
			}

			firewallScriptPath := filepath.Join(dockerfilesDir, "init-firewall.sh")
			if err := os.WriteFile(firewallScriptPath, []byte(FirewallScript), 0755); err != nil {
				return fmt.Errorf("failed to write firewall script %s: %w", firewallScriptPath, err)
			}

			entrypointScriptPath := filepath.Join(dockerfilesDir, "entrypoint.sh")
			if err := os.WriteFile(entrypointScriptPath, []byte(EntrypointScript), 0755); err != nil {
				return fmt.Errorf("failed to write entrypoint script %s: %w", entrypointScriptPath, err)
			}
		}
	}

	return nil
}

// createContext creates a DockerfileContext for a given version and variant.
func (m *DockerfileManager) createContext(version, variant string) *DockerfileContext {
	isAlpine := m.config.IsAlpine(variant)
	baseImage := m.variantToBaseImage(variant)

	return &DockerfileContext{
		BaseImage:      baseImage,
		Packages:       []string{}, // Base packages are in template
		Username:       "claude",
		UID:            1001,
		GID:            1001,
		Shell:          "/bin/zsh",
		WorkspacePath:  "/workspace",
		ClaudeVersion:  version,
		IsAlpine:       isAlpine,
		EnableFirewall: true,
		ExtraEnv:       map[string]string{},
		Editor:         "nano",
		Visual:         "nano",
		Instructions:   nil,
		Inject:         nil,
	}
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
	config  *config.Config
	workDir string
}

// NewProjectGenerator creates a new project Dockerfile generator.
func NewProjectGenerator(cfg *config.Config, workDir string) *ProjectGenerator {
	return &ProjectGenerator{
		config:  cfg,
		workDir: workDir,
	}
}

// Generate creates a Dockerfile based on the project configuration.
func (g *ProjectGenerator) Generate() ([]byte, error) {
	ctx := g.buildContext()

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
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	// Generate Dockerfile
	dockerfile, err := g.Generate()
	if err != nil {
		return nil, err
	}

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
	if g.config.Security.EnableFirewall {
		if err := addFileToTar(tw, "init-firewall.sh", []byte(FirewallScript)); err != nil {
			return nil, err
		}
	}

	// Add host-open script for opening URLs on host machine
	if err := addFileToTar(tw, "host-open.sh", []byte(HostOpenScript)); err != nil {
		return nil, err
	}

	// Add callback-forwarder script for OAuth callback proxying
	if err := addFileToTar(tw, "callback-forwarder.sh", []byte(CallbackForwarderScript)); err != nil {
		return nil, err
	}

	// Add git-credential-clawker script for git credential forwarding
	if err := addFileToTar(tw, "git-credential-clawker.sh", []byte(GitCredentialScript)); err != nil {
		return nil, err
	}

	// Add any include files from agent config
	for _, include := range g.config.Agent.Includes {
		includePath := include
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(g.workDir, includePath)
		}

		content, err := os.ReadFile(includePath)
		if err != nil {
			logger.Warn().Str("file", include).Err(err).Msg("failed to read include file")
			continue
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

// UseCustomDockerfile checks if a custom Dockerfile should be used.
func (g *ProjectGenerator) UseCustomDockerfile() bool {
	return g.config.Build.Dockerfile != ""
}

// GetCustomDockerfilePath returns the path to the custom Dockerfile.
func (g *ProjectGenerator) GetCustomDockerfilePath() string {
	path := g.config.Build.Dockerfile
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.workDir, path)
	}
	return path
}

// GetBuildContext returns the build context path.
func (g *ProjectGenerator) GetBuildContext() string {
	if g.config.Build.Context != "" {
		path := g.config.Build.Context
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
func (g *ProjectGenerator) buildContext() *DockerfileContext {
	baseImage := g.config.Build.Image
	if baseImage == "" {
		baseImage = "buildpack-deps:bookworm-scm"
	}

	isAlpine := docker.IsAlpineImage(baseImage)

	// Set editor defaults
	editor := g.config.Agent.Editor
	if editor == "" {
		editor = "nano"
	}
	visual := g.config.Agent.Visual
	if visual == "" {
		visual = "nano"
	}

	ctx := &DockerfileContext{
		BaseImage:      baseImage,
		Packages:       filterBasePackages(g.config.Build.Packages, isAlpine),
		Username:       DefaultUsername,
		UID:            DefaultUID,
		GID:            DefaultGID,
		Shell:          DefaultShell,
		WorkspacePath:  g.config.Workspace.RemotePath,
		ClaudeVersion:  DefaultClaudeCodeVersion,
		IsAlpine:       isAlpine,
		EnableFirewall: g.config.Security.EnableFirewall,
		ExtraEnv:       g.config.Agent.Env,
		Editor:         editor,
		Visual:         visual,
		ImageLabels:    docker.ImageLabels(g.config.Project, g.config.Version),
	}

	// Populate Instructions if present
	if g.config.Build.Instructions != nil {
		inst := g.config.Build.Instructions
		ctx.Instructions = &DockerfileInstructions{
			Copy:        convertCopyInstructions(inst.Copy),
			Env:         inst.Env,
			Labels:      inst.Labels,
			Expose:      convertExposeInstructions(inst.Expose),
			Args:        convertArgInstructions(inst.Args),
			Volumes:     inst.Volumes,
			Workdir:     inst.Workdir,
			Healthcheck: convertHealthcheck(inst.Healthcheck),
			Shell:       inst.Shell,
			UserRun:     convertRunInstructions(inst.UserRun),
			RootRun:     convertRunInstructions(inst.RootRun),
		}
	}

	// Populate Inject if present
	if g.config.Build.Inject != nil {
		inj := g.config.Build.Inject
		ctx.Inject = &DockerfileInject{
			AfterFrom:          inj.AfterFrom,
			AfterPackages:      inj.AfterPackages,
			AfterUserSetup:     inj.AfterUserSetup,
			AfterUserSwitch:    inj.AfterUserSwitch,
			AfterClaudeInstall: inj.AfterClaudeInstall,
			BeforeEntrypoint:   inj.BeforeEntrypoint,
		}
	}

	return ctx
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

func convertExposeInstructions(src []config.ExposePort) []ExposeInstruction {
	if src == nil {
		return nil
	}
	result := make([]ExposeInstruction, len(src))
	for i, e := range src {
		result[i] = ExposeInstruction{
			Port:     e.Port,
			Protocol: e.Protocol,
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

func convertHealthcheck(src *config.HealthcheckConfig) *HealthcheckInstruction {
	if src == nil {
		return nil
	}
	return &HealthcheckInstruction{
		Cmd:         src.Cmd,
		Interval:    src.Interval,
		Timeout:     src.Timeout,
		Retries:     src.Retries,
		StartPeriod: src.StartPeriod,
	}
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

	// Walk the directory
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
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
