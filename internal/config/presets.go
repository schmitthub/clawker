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
  toolchains:
    - python
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
  toolchains:
    - go
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
  toolchains:
    - rust
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

// The node toolchain declaration provides node + npm before the project's
// build instructions run. registry.npmjs.org is in the required firewall set
// (see internal/config/defaults.go). TypeScript-specific tooling (pnpm, tsc)
// layers on top.
const typescriptPreset = `agent:
  pre_run: |
    if [ -f package.json ]; then
      npm install || echo "warning: npm install failed; continuing"
    fi
build:
  toolchains:
    - node
  packages:
    - ripgrep
  instructions:
    user_run:
      - npm install -g pnpm typescript
`

const javaPreset = `build:
  packages:
    - ripgrep
    - default-jdk
    - maven
security:
  firewall:
    add_domains:
      - repo1.maven.org
      - central.sonatype.com
`

const rubyPreset = `build:
  packages:
    - ripgrep
    - ruby-full
    - build-essential
security:
  firewall:
    add_domains:
      - rubygems.org
      - index.rubygems.org
`

const cppPreset = `build:
  packages:
    - ripgrep
    - cmake
    - g++
`

const dotnetPreset = `build:
  packages:
    - ripgrep
security:
  firewall:
    add_domains:
      - api.nuget.org
`

const barePreset = `build:
  packages:
    - ripgrep
`
