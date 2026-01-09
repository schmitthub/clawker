package dockerfile

import (
	_ "embed"
)

// Embedded templates for Dockerfile generation

//go:embed templates/Dockerfile.tmpl
var DockerfileTemplate string

//go:embed templates/entrypoint.sh
var EntrypointScript string

//go:embed templates/init-firewall.sh
var FirewallScript string

// DefaultClaudeCodeVersion is the default Claude Code version to install
const DefaultClaudeCodeVersion = "latest"

// DefaultUsername is the default non-root user in containers
const DefaultUsername = "claude"

// DefaultUID is the default UID for the claude user
const DefaultUID = 1001

// DefaultGID is the default GID for the claude group
const DefaultGID = 1001

// DefaultShell is the default shell for the claude user
const DefaultShell = "/bin/bash"
