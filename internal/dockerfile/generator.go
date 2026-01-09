package dockerfile

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/schmitthub/claucker/internal/config"
	"github.com/schmitthub/claucker/internal/engine"
	"github.com/schmitthub/claucker/pkg/logger"
)

// GenerateContext holds the data for Dockerfile template rendering
type GenerateContext struct {
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
}

// Generator creates Dockerfiles dynamically from configuration
type Generator struct {
	config  *config.Config
	workDir string
}

// NewGenerator creates a new Dockerfile generator
func NewGenerator(cfg *config.Config, workDir string) *Generator {
	return &Generator{
		config:  cfg,
		workDir: workDir,
	}
}

// Generate creates a Dockerfile based on the configuration
func (g *Generator) Generate() ([]byte, error) {
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

// GenerateBuildContext creates a tar archive containing the Dockerfile and scripts
func (g *Generator) GenerateBuildContext() (io.Reader, error) {
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

	// Add firewall script if enabled
	if g.config.Security.EnableFirewall {
		if err := addFileToTar(tw, "init-firewall.sh", []byte(FirewallScript)); err != nil {
			return nil, err
		}
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

// UseCustomDockerfile checks if a custom Dockerfile should be used
func (g *Generator) UseCustomDockerfile() bool {
	return g.config.Build.Dockerfile != ""
}

// GetCustomDockerfilePath returns the path to the custom Dockerfile
func (g *Generator) GetCustomDockerfilePath() string {
	path := g.config.Build.Dockerfile
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.workDir, path)
	}
	return path
}

// GetBuildContext returns the build context path
func (g *Generator) GetBuildContext() string {
	if g.config.Build.Context != "" {
		path := g.config.Build.Context
		if !filepath.IsAbs(path) {
			path = filepath.Join(g.workDir, path)
		}
		return path
	}
	return g.workDir
}

// buildContext creates the template context from config
func (g *Generator) buildContext() GenerateContext {
	baseImage := g.config.Build.Image
	if baseImage == "" {
		baseImage = "node:20-slim"
	}

	return GenerateContext{
		BaseImage:      baseImage,
		Packages:       g.config.Build.Packages,
		Username:       DefaultUsername,
		UID:            DefaultUID,
		GID:            DefaultGID,
		Shell:          DefaultShell,
		WorkspacePath:  g.config.Workspace.RemotePath,
		ClaudeVersion:  DefaultClaudeCodeVersion,
		IsAlpine:       engine.IsAlpineImage(baseImage),
		EnableFirewall: g.config.Security.EnableFirewall,
		ExtraEnv:       g.config.Agent.Env,
	}
}

// addFileToTar adds a file to a tar archive
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

// CreateBuildContextFromDir creates a tar archive from a directory for custom Dockerfiles
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
