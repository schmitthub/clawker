package shared

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

const (
	MarketplaceSource = "schmitthub/claude-plugins"
	PluginName        = "clawker-support@schmitthub-plugins"
)

// ValidScopes is the set of scopes the Claude CLI accepts for plugin operations.
var ValidScopes = []string{"user", "project", "local"}

// ValidateScope returns a FlagError if scope is not one of the valid Claude CLI scopes.
func ValidateScope(scope string) error {
	if slices.Contains(ValidScopes, scope) {
		return nil
	}
	return cmdutil.FlagErrorf("--scope must be user, project, or local; got %q", scope)
}

// CheckClaudeCLI verifies the claude binary is available in PATH.
func CheckClaudeCLI() error {
	_, err := exec.LookPath("claude")
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("claude CLI not found in PATH — install it from https://docs.anthropic.com/en/docs/claude-code")
	}
	return fmt.Errorf("claude CLI not usable: %w", err)
}

// RunClaude executes a claude CLI command, wiring stdin/stdout/stderr to the
// provided IOStreams. On failure it returns an actionable error message.
func RunClaude(ctx context.Context, ios *iostreams.IOStreams, args ...string) error {
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Stdin = ios.In
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("operation cancelled")
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("claude %s exited with status %d — check the output above for details",
				strings.Join(args, " "), exitErr.ExitCode())
		}
		return fmt.Errorf("running claude %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
