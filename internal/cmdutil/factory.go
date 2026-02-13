package cmdutil

import (
	"context"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

// Factory provides shared dependencies for CLI commands.
// It is a dependency injection container: the struct defines what
// dependencies exist (the contract), while internal/cmd/factory
// wires the real implementations.
//
// Fields are either eager (set at construction) or lazy nouns
// (closures that return cached instances on first call).
// Commands extract only the fields they need into per-command
// Options structs.
type Factory struct {
	// Eager (set at construction)
	Version   string
	IOStreams *iostreams.IOStreams
	TUI       *tui.TUI

	// Lazy nouns
	Client       func(context.Context) (*docker.Client, error)
	Config       func() *config.Config
	GitManager   func() (*git.GitManager, error)
	HostProxy    func() hostproxy.HostProxyService
	SocketBridge func() socketbridge.SocketBridgeManager
	Prompter     func() *prompter.Prompter
}
