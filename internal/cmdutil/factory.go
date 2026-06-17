package cmdutil

import (
	"context"
	"net/http"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/state"
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
	Client   func(context.Context) (*docker.Client, error)
	Config   func() (config.Config, error)
	Logger   func() (*logger.Logger, error)
	CLIState func() (state.StateStore, error)
	// ProjectRegistry is the process-wide project registry facade — the
	// single constructor of registry storage. Config walk-up anchoring,
	// the project manager, and any command needing project-root resolution
	// all share this instance.
	ProjectRegistry func() (*project.Registry, error)
	ProjectManager  func() (project.ProjectManager, error)
	// CLI runtime-state (internal/state) and the changelog loader
	// (internal/changelog) are intentionally NOT Factory nouns: both are used
	// only by Main (the background update check + the show-once teaser), so Main
	// constructs them directly rather than carrying single-consumer closures here.
	GitManager   func() (*git.GitManager, error)
	HostProxy    func() hostproxy.HostProxyService
	SocketBridge func() socketbridge.SocketBridgeManager
	Prompter     func() *prompter.Prompter
	AdminClient  func(context.Context) (adminv1.AdminServiceClient, error)
	ControlPlane func() cpboot.Manager
	// HttpClient returns the *http.Client used for outbound HTTP from the
	// CLI (first consumer: npm registry lookups for Claude Code version
	// resolution). Tests substitute by setting this field to a closure that
	// returns a client whose Transport is a stubbed http.RoundTripper —
	// same pattern as cli/cli's pkg/httpmock.Registry. No project-defined
	// interface; the stdlib RoundTripper IS the seam.
	HttpClient func() (*http.Client, error)
}
