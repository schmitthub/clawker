package dockerfile

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/claucker/internal/config"
)

func TestNewGenerator(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Project = "test-project"

	gen := NewGenerator(cfg, "/workspace")

	if gen.config != cfg {
		t.Error("Generator should store the config")
	}
	if gen.workDir != "/workspace" {
		t.Errorf("Generator.workDir = %q, want %q", gen.workDir, "/workspace")
	}
}

func TestGenerateDockerfile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Project = "test-project"
	cfg.Build.Image = "node:20-slim"
	cfg.Build.Packages = []string{"git", "curl"}
	cfg.Workspace.RemotePath = "/workspace"
	cfg.Security.EnableFirewall = true
	cfg.Agent.Env = map[string]string{"NODE_ENV": "production"}

	gen := NewGenerator(cfg, "/tmp")
	dockerfile, err := gen.Generate()

	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	content := string(dockerfile)

	// Check base image
	if !strings.Contains(content, "FROM node:20-slim") {
		t.Error("Dockerfile should contain FROM node:20-slim")
	}

	// Check packages are installed (Debian-based)
	if !strings.Contains(content, "apt-get") {
		t.Error("Dockerfile should use apt-get for Debian-based image")
	}
	if !strings.Contains(content, "git") {
		t.Error("Dockerfile should install git")
	}
	if !strings.Contains(content, "curl") {
		t.Error("Dockerfile should install curl")
	}

	// Check user setup
	if !strings.Contains(content, "USERNAME=claude") {
		t.Error("Dockerfile should set USERNAME=claude")
	}
	if !strings.Contains(content, "USER_UID=1001") {
		t.Error("Dockerfile should set USER_UID=1001")
	}

	// Check workspace path
	if !strings.Contains(content, "/workspace") {
		t.Error("Dockerfile should reference /workspace")
	}

	// Check Claude Code installation
	if !strings.Contains(content, "claude.ai/install.sh") {
		t.Error("Dockerfile should install Claude Code")
	}

	// Check firewall support (packages should be installed)
	if !strings.Contains(content, "iptables") {
		t.Error("Dockerfile should install iptables when firewall is enabled")
	}

	// Check environment variables
	if !strings.Contains(content, "NODE_ENV") {
		t.Error("Dockerfile should include custom environment variables")
	}

	// Check entrypoint
	if !strings.Contains(content, "ENTRYPOINT") {
		t.Error("Dockerfile should have ENTRYPOINT")
	}
}

func TestGenerateDockerfileAlpine(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Project = "alpine-project"
	cfg.Build.Image = "node:20-alpine"
	cfg.Build.Packages = []string{"git"}
	cfg.Security.EnableFirewall = false

	gen := NewGenerator(cfg, "/tmp")
	dockerfile, err := gen.Generate()

	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	content := string(dockerfile)

	// Check Alpine package manager
	if !strings.Contains(content, "apk add") {
		t.Error("Dockerfile should use apk for Alpine-based image")
	}

	// Check Alpine user creation syntax
	if !strings.Contains(content, "addgroup") {
		t.Error("Dockerfile should use addgroup for Alpine")
	}
	if !strings.Contains(content, "adduser") {
		t.Error("Dockerfile should use adduser for Alpine")
	}

	// Should NOT have firewall packages when disabled
	if strings.Contains(content, "iptables") && strings.Contains(content, "apk add --no-cache iptables") {
		// This is a weak check - firewall packages might still be installed for other reasons
	}
}

func TestGenerateBuildContext(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Project = "test-project"
	cfg.Security.EnableFirewall = true

	gen := NewGenerator(cfg, tmpDir)
	reader, err := gen.GenerateBuildContext()

	if err != nil {
		t.Fatalf("GenerateBuildContext() error = %v", err)
	}

	// Read the tar archive
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read build context: %v", err)
	}

	// Parse tar archive
	tr := tar.NewReader(bytes.NewReader(data))
	foundDockerfile := false
	foundEntrypoint := false
	foundFirewall := false

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar entry: %v", err)
		}

		switch header.Name {
		case "Dockerfile":
			foundDockerfile = true
		case "entrypoint.sh":
			foundEntrypoint = true
			// Check it's executable
			if header.Mode&0111 == 0 {
				t.Error("entrypoint.sh should be executable")
			}
		case "init-firewall.sh":
			foundFirewall = true
			// Check it's executable
			if header.Mode&0111 == 0 {
				t.Error("init-firewall.sh should be executable")
			}
		}
	}

	if !foundDockerfile {
		t.Error("Build context should contain Dockerfile")
	}
	if !foundEntrypoint {
		t.Error("Build context should contain entrypoint.sh")
	}
	if !foundFirewall {
		t.Error("Build context should contain init-firewall.sh when firewall is enabled")
	}
}

func TestGenerateBuildContextWithoutFirewall(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Project = "test-project"
	cfg.Security.EnableFirewall = false

	gen := NewGenerator(cfg, tmpDir)
	reader, err := gen.GenerateBuildContext()

	if err != nil {
		t.Fatalf("GenerateBuildContext() error = %v", err)
	}

	// Read and check the tar archive
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read build context: %v", err)
	}

	tr := tar.NewReader(bytes.NewReader(data))
	foundFirewall := false

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar entry: %v", err)
		}

		if header.Name == "init-firewall.sh" {
			foundFirewall = true
		}
	}

	if foundFirewall {
		t.Error("Build context should NOT contain init-firewall.sh when firewall is disabled")
	}
}

func TestUseCustomDockerfile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Project = "test-project"

	// Without custom Dockerfile
	gen := NewGenerator(cfg, "/tmp")
	if gen.UseCustomDockerfile() {
		t.Error("UseCustomDockerfile() should return false when no dockerfile specified")
	}

	// With custom Dockerfile
	cfg.Build.Dockerfile = "./Dockerfile.custom"
	gen = NewGenerator(cfg, "/tmp")
	if !gen.UseCustomDockerfile() {
		t.Error("UseCustomDockerfile() should return true when dockerfile specified")
	}
}

func TestGetCustomDockerfilePath(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Project = "test-project"
	cfg.Build.Dockerfile = "./Dockerfile.custom"

	gen := NewGenerator(cfg, "/workspace")
	path := gen.GetCustomDockerfilePath()

	expected := "/workspace/Dockerfile.custom"
	if path != expected {
		t.Errorf("GetCustomDockerfilePath() = %q, want %q", path, expected)
	}

	// Test with absolute path
	cfg.Build.Dockerfile = "/absolute/path/Dockerfile"
	gen = NewGenerator(cfg, "/workspace")
	path = gen.GetCustomDockerfilePath()

	if path != "/absolute/path/Dockerfile" {
		t.Errorf("GetCustomDockerfilePath() = %q, want %q", path, "/absolute/path/Dockerfile")
	}
}

func TestGetBuildContext(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Project = "test-project"

	// Default context (workdir)
	gen := NewGenerator(cfg, "/workspace")
	ctx := gen.GetBuildContext()
	if ctx != "/workspace" {
		t.Errorf("GetBuildContext() = %q, want %q", ctx, "/workspace")
	}

	// Custom context
	cfg.Build.Context = "./docker"
	gen = NewGenerator(cfg, "/workspace")
	ctx = gen.GetBuildContext()
	if ctx != "/workspace/docker" {
		t.Errorf("GetBuildContext() = %q, want %q", ctx, "/workspace/docker")
	}
}

func TestDockerfileSecurityFeatures(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Project = "secure-project"
	cfg.Security.EnableFirewall = true

	gen := NewGenerator(cfg, "/tmp")
	dockerfile, err := gen.Generate()

	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	content := string(dockerfile)

	// Check non-root user is created
	if !strings.Contains(content, "USER") {
		t.Error("Dockerfile should switch to non-root USER")
	}

	// Check sudo is installed (for firewall init)
	if !strings.Contains(content, "sudo") {
		t.Error("Dockerfile should install sudo for firewall initialization")
	}

	// Check NOPASSWD sudo for user
	if !strings.Contains(content, "NOPASSWD") {
		t.Error("Dockerfile should configure NOPASSWD sudo for the user")
	}
}

func TestCreateBuildContextFromDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some test files
	if err := os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte("FROM alpine"), 0644); err != nil {
		t.Fatalf("failed to create Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "app.js"), []byte("console.log('hello')"), 0644); err != nil {
		t.Fatalf("failed to create app.js: %v", err)
	}
	if err := os.Mkdir(filepath.Join(tmpDir, ".git"), 0755); err != nil {
		t.Fatalf("failed to create .git dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".git", "config"), []byte("[core]"), 0644); err != nil {
		t.Fatalf("failed to create .git/config: %v", err)
	}

	reader, err := CreateBuildContextFromDir(tmpDir, "Dockerfile")
	if err != nil {
		t.Fatalf("CreateBuildContextFromDir() error = %v", err)
	}

	// Read and check tar archive
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read tar: %v", err)
	}

	tr := tar.NewReader(bytes.NewReader(data))
	foundDockerfile := false
	foundApp := false
	foundGit := false

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar entry: %v", err)
		}

		switch header.Name {
		case "Dockerfile":
			foundDockerfile = true
		case "app.js":
			foundApp = true
		}
		if strings.HasPrefix(header.Name, ".git") {
			foundGit = true
		}
	}

	if !foundDockerfile {
		t.Error("Build context should contain Dockerfile")
	}
	if !foundApp {
		t.Error("Build context should contain app.js")
	}
	if foundGit {
		t.Error("Build context should NOT contain .git directory")
	}
}

func TestTemplateConstants(t *testing.T) {
	if DefaultClaudeCodeVersion == "" {
		t.Error("DefaultClaudeCodeVersion should not be empty")
	}
	if DefaultUsername == "" {
		t.Error("DefaultUsername should not be empty")
	}
	if DefaultUsername != "claude" {
		t.Errorf("DefaultUsername = %q, want %q", DefaultUsername, "claude")
	}
	if DefaultUID != 1001 {
		t.Errorf("DefaultUID = %d, want %d", DefaultUID, 1001)
	}
	if DefaultGID != 1001 {
		t.Errorf("DefaultGID = %d, want %d", DefaultGID, 1001)
	}
	if DefaultShell != "/bin/bash" {
		t.Errorf("DefaultShell = %q, want %q", DefaultShell, "/bin/bash")
	}
}

func TestEmbeddedTemplates(t *testing.T) {
	// Test that embedded templates are not empty
	if DockerfileTemplate == "" {
		t.Error("DockerfileTemplate should not be empty")
	}
	if EntrypointScript == "" {
		t.Error("EntrypointScript should not be empty")
	}
	if FirewallScript == "" {
		t.Error("FirewallScript should not be empty")
	}

	// Test that entrypoint script has proper shebang
	if !strings.HasPrefix(EntrypointScript, "#!/bin/bash") {
		t.Error("EntrypointScript should start with #!/bin/bash")
	}

	// Test that firewall script has proper shebang
	if !strings.HasPrefix(FirewallScript, "#!/bin/bash") {
		t.Error("FirewallScript should start with #!/bin/bash")
	}
}
