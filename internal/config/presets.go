package config

// Preset defines a language-specific project configuration template.
// Each preset provides a partial YAML overlay — fields not specified
// are filled from schema defaults via WithDefaultsFromStruct[Project]().
type Preset struct {
	Name          string // Display name (used as select option label)
	Description   string // Short description (used as select option secondary text)
	YAML          string // Partial clawker.yaml content
	AutoCustomize bool   // If true, skip "save and get started" and go straight to wizard
}

// Presets returns the ordered list of language presets for the init wizard.
// The last entry ("Build from scratch") is Bare with AutoCustomize=true.
func Presets() []Preset {
	return []Preset{
		{
			Name:        "Python",
			Description: "Python development with pip and venv",
			YAML:        pythonPreset,
		},
		{
			Name:        "Go",
			Description: "Go development with module support",
			YAML:        goPreset,
		},
		{
			Name:        "Rust",
			Description: "Rust development with Cargo",
			YAML:        rustPreset,
		},
		{
			Name:        "TypeScript",
			Description: "Node.js and TypeScript development",
			YAML:        typescriptPreset,
		},
		{
			Name:        "Java",
			Description: "Java development with Maven",
			YAML:        javaPreset,
		},
		{
			Name:        "Ruby",
			Description: "Ruby development with Bundler",
			YAML:        rubyPreset,
		},
		{
			Name:        "C/C++",
			Description: "C/C++ development with GCC and CMake",
			YAML:        cppPreset,
		},
		{
			Name:        "C#/.NET",
			Description: ".NET SDK development",
			YAML:        dotnetPreset,
		},
		{
			Name:        "Bare",
			Description: "Minimal base with common tools, no language runtime",
			YAML:        barePreset,
		},
		{
			Name:          "Build from scratch",
			Description:   "Start with a minimal base and customize everything",
			YAML:          barePreset,
			AutoCustomize: true,
		},
	}
}

const pythonPreset = `build:
  image: "python:3.12-bookworm"
  packages:
    - ripgrep
    - build-essential
security:
  firewall:
    add_domains:
      - pypi.org
      - files.pythonhosted.org
`

const goPreset = `build:
  image: "golang:1.25-bookworm"
  packages:
    - ripgrep
security:
  firewall:
    add_domains:
      - proxy.golang.org
      - sum.golang.org
      - storage.googleapis.com
`

const rustPreset = `build:
  image: "rust:1-bookworm"
  packages:
    - ripgrep
    - build-essential
    - pkg-config
security:
  firewall:
    add_domains:
      - crates.io
      - static.crates.io
      - index.crates.io
`

const typescriptPreset = `agent:
  env:
    NODE_USE_SYSTEM_CA: 1
build:
  image: "node:24-bookworm"
  packages:
    - ripgrep
    - ca-certificates
    - openssh-client
  inject:
    after_user_switch:
      - ENV NVM_DIR="/home/${USERNAME}/.nvm"
      - ENV NODE_VERSION="24"
  instructions:
    user_run:
      - curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
      - |
          . "$NVM_DIR/nvm.sh" && \
          nvm install $NODE_VERSION && \
          nvm use $NODE_VERSION && \
          nvm alias default $NODE_VERSION
      - |
          . "$NVM_DIR/nvm.sh" && \
          npm install -g pnpm typescript
      - |
          echo 'export NVM_DIR="$HOME/.nvm"' >> ~/.bashrc && \
          echo '[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"' >> ~/.bashrc && \
          echo '[ -s "$NVM_DIR/bash_completion" ] && . "$NVM_DIR/bash_completion"' >> ~/.bashrc
security:
  firewall:
    add_domains:
      - registry.npmjs.org
`

const javaPreset = `build:
  image: "eclipse-temurin:21-jdk-alpine"
  packages:
    - ripgrep
    - maven
security:
  firewall:
    add_domains:
      - repo1.maven.org
      - central.sonatype.com
`

const rubyPreset = `build:
  image: "ruby:3.3-bookworm"
  packages:
    - ripgrep
    - build-essential
security:
  firewall:
    add_domains:
      - rubygems.org
      - index.rubygems.org
`

const cppPreset = `build:
  image: "buildpack-deps:bookworm"
  packages:
    - ripgrep
    - cmake
    - g++
`

const dotnetPreset = `build:
  image: "mcr.microsoft.com/dotnet/sdk:9.0-bookworm-slim"
  packages:
    - ripgrep
security:
  firewall:
    add_domains:
      - api.nuget.org
`

const barePreset = `build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - ripgrep
`
