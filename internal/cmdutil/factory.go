package cmdutil

import (
	"context"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompts"
)

// Factory provides shared dependencies for CLI commands.
// It is a dependency injection container: the struct defines what
// dependencies exist (the contract), while internal/cmd/factory
// wires the real implementations.
//
// Closure fields are set by the factory constructor and use lazy
// initialization internally. Commands extract only the fields they
// need into per-command Options structs.
type Factory struct {
	// Configuration from flags (set before command execution)
	WorkDir        string
	BuildOutputDir string // Directory for build artifacts (versions.json, dockerfiles)
	Debug          bool

	// Version info (set at build time via ldflags)
	Version string
	Commit  string

	// IO streams for input/output (for testability)
	IOStreams *iostreams.IOStreams

	// Dependency providers (closures wired by factory constructor)
	Client      func(context.Context) (*docker.Client, error)
	CloseClient func()

	ConfigLoader func() *config.Loader
	Config       func() (*config.Config, error)
	ResetConfig  func()

	SettingsLoader          func() (*config.SettingsLoader, error)
	Settings                func() (*config.Settings, error)
	InvalidateSettingsCache func()

	RegistryLoader func() (*config.RegistryLoader, error)
	Registry       func() (*config.ProjectRegistry, error)
	Resolution     func() *config.Resolution

	HostProxy       func() *hostproxy.Manager
	EnsureHostProxy func() error
	StopHostProxy   func(context.Context) error
	HostProxyEnvVar func() string

	Prompter        func() *prompts.Prompter
	RuntimeEnv      func() []string
	BuildKitEnabled func(context.Context) (bool, error)
}
