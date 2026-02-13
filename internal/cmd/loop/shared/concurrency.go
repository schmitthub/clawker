package shared

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
)

// ConcurrencyAction represents the user's chosen action after a concurrency check.
type ConcurrencyAction int

const (
	// ActionProceed continues without worktree isolation.
	ActionProceed ConcurrencyAction = iota
	// ActionWorktree uses a worktree for isolation.
	ActionWorktree
	// ActionAbort cancels the operation.
	ActionAbort
)

// ConcurrencyCheckConfig holds inputs for CheckConcurrency.
type ConcurrencyCheckConfig struct {
	// Client is the Docker client for listing containers.
	Client *docker.Client

	// Project is the project name to check.
	Project string

	// WorkDir is the current absolute host working directory.
	WorkDir string

	// IOStreams is used for warning output.
	IOStreams *iostreams.IOStreams

	// Prompter returns the interactive prompter. Nil means non-interactive.
	Prompter func() *prompter.Prompter
}

// CheckConcurrency checks for running containers in the same project and working
// directory. If a conflict is found, it either warns (non-interactive) or prompts
// the user to choose between worktree isolation, proceeding anyway, or aborting.
func CheckConcurrency(ctx context.Context, cfg *ConcurrencyCheckConfig) (ConcurrencyAction, error) {
	containers, err := cfg.Client.ListContainersByProject(ctx, cfg.Project, false)
	if err != nil {
		return ActionProceed, fmt.Errorf("checking for concurrent sessions: %w", err)
	}

	// Find running containers with the same working directory
	var conflicts []docker.Container
	for _, c := range containers {
		if c.Status == "running" && c.Workdir == cfg.WorkDir {
			conflicts = append(conflicts, c)
		}
	}

	if len(conflicts) == 0 {
		return ActionProceed, nil
	}

	cs := cfg.IOStreams.ColorScheme()
	first := conflicts[0]

	// Non-interactive: warn and proceed
	if cfg.Prompter == nil || !cfg.IOStreams.IsInteractive() {
		fmt.Fprintf(cfg.IOStreams.ErrOut, "%s A loop session (%s) is already running in %s\n",
			cs.WarningIcon(), first.Name, cfg.WorkDir)
		fmt.Fprintf(cfg.IOStreams.ErrOut, "  Consider using --worktree for isolation\n")
		return ActionProceed, nil
	}

	// Interactive: prompt the user
	fmt.Fprintf(cfg.IOStreams.ErrOut, "%s A loop session (%s) is already running in %s\n",
		cs.WarningIcon(), first.Name, cfg.WorkDir)

	p := cfg.Prompter()
	idx, err := p.Select("How would you like to proceed?", []prompter.SelectOption{
		{Label: "Use a worktree for isolation", Description: "Recommended"},
		{Label: "Proceed anyway", Description: "Risk: file conflicts"},
		{Label: "Abort"},
	}, 0)
	if err != nil {
		return ActionProceed, fmt.Errorf("prompting for concurrency action: %w", err)
	}

	switch idx {
	case 0:
		return ActionWorktree, nil
	case 1:
		return ActionProceed, nil
	case 2:
		return ActionAbort, nil
	default:
		return ActionProceed, nil
	}
}
