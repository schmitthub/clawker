// Package shared holds the stack-preparation and compose plumbing common to
// the monitor subcommands that render and apply the monitoring stack
// (`monitor up`, `monitor reload`).
package shared

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	internalmonitor "github.com/schmitthub/clawker/internal/monitor"
)

// PrepareStack resolves the current projection, merges it into an in-memory
// view of the host ledger (a C5 collision is a hard error), validates the
// union, and renders the stack config over it. It returns the projection's
// units — the set the caller persists via SeedLedger after a successful
// compose up — and the render result. The in-memory merge here is
// render-only; the authoritative persisted merge happens under SeedLedger's
// file lock.
func PrepareStack(
	cfg config.Config,
	monitorDir string,
) ([]internalmonitor.ResolvedUnit, internalmonitor.StackRender, error) {
	cwdUnits, err := internalmonitor.ResolveUnits(cfg)
	if err != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("resolve monitoring extensions: %w", err)
	}

	ledger, err := internalmonitor.LoadLedger(monitorDir)
	if err != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("load monitoring units ledger: %w", err)
	}
	// C5 is a hard error: a same-named loose extension with different content
	// from another project refuses to seed rather than clobbering the stack.
	if mergeErr := ledger.Merge(cwdUnits, time.Now()); mergeErr != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("seed monitoring extensions: %w", mergeErr)
	}
	union := ledger.Union()
	if validateErr := internalmonitor.ValidateSeededSet(union); validateErr != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("validate seeded monitoring units: %w", validateErr)
	}

	data, err := internalmonitor.PrepareTemplateData(cfg.SettingsStore().Read(), union)
	if err != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("build monitor template data: %w", err)
	}
	render, err := internalmonitor.RenderStack(monitorDir, data, cwdUnits, true)
	if err != nil {
		return nil, internalmonitor.StackRender{}, fmt.Errorf("render monitoring stack config: %w", err)
	}
	return cwdUnits, render, nil
}

// composeCmd is the docker subcommand every stack operation goes through —
// the CLI owns the compose lifecycle.
const composeCmd = "compose"

// ComposeUp brings the stack up with a plain `docker compose up`. It never
// removes or recreates a service — applying a changed collector config to a
// running stack is `monitor reload`'s job. The error is returned raw; the
// caller adds the single contextual wrap.
func ComposeUp(
	ctx context.Context,
	ios *iostreams.IOStreams,
	log *logger.Logger,
	composePath string,
	detach bool,
) error {
	upArgs := []string{composeCmd, "-f", composePath, "up", "--remove-orphans"}
	if detach {
		upArgs = append(upArgs, "-d")
	}
	log.Debug().Strs("args", upArgs).Msg("running docker compose up")
	return RunComposeCmd(ctx, ios, upArgs, "Starting monitoring stack...")
}

// RemoveCollector stops and removes the otel-collector service so the next
// compose up creates it fresh, reading the current rendered config. Compose
// never recreates a container because a bind-mounted file's CONTENT changed,
// so this explicit removal is the only way a config edit reaches a running
// collector. The error is returned raw; the caller adds the single contextual
// wrap.
func RemoveCollector(
	ctx context.Context,
	ios *iostreams.IOStreams,
	log *logger.Logger,
	composePath string,
) error {
	rmArgs := []string{
		composeCmd, "-f", composePath, "rm", "--stop", "--force",
		consts.MonitoringServiceOtelCollector,
	}
	log.Debug().Strs("args", rmArgs).Msg("removing otel-collector so up recreates it with the current config")
	return RunComposeCmd(ctx, ios, rmArgs, "Applying updated collector config...")
}

// CollectorRunning reports whether the otel-collector service has a running
// container, via `docker compose ps`. Errors are reported as not-running: the
// caller only uses this to decide whether to print an advisory warning, and a
// failing ps must not block the bring-up it precedes.
func CollectorRunning(ctx context.Context, composePath string) bool {
	psArgs := []string{
		composeCmd, "-f", composePath, "ps", "-q", "--status", "running",
		consts.MonitoringServiceOtelCollector,
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", psArgs...)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return false
	}
	return strings.TrimSpace(out.String()) != ""
}

// RunComposeCmd runs one docker compose invocation under a spinner. Errors are
// returned raw — docker's own stderr already streamed to the user, and the
// caller adds the one contextual wrap.
func RunComposeCmd(ctx context.Context, ios *iostreams.IOStreams, args []string, label string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = ios.Out
	cmd.Stderr = ios.ErrOut
	ios.StartSpinner(label)
	err := cmd.Run()
	ios.StopSpinner()
	//nolint:wrapcheck // raw by design: docker's stderr already streamed; caller adds the single contextual wrap
	return err
}
