package shared

import (
	"errors"
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
)

// ErrProjectConfigUntrusted is returned when the project config security
// settings changed and no prompt can ask the user to accept them.
var ErrProjectConfigUntrusted = errors.New("project config security settings changed since the last trusted run")

// TrustDeps are the Factory nouns the config trust gate needs.
type TrustDeps struct {
	IOStreams       *iostreams.IOStreams
	Config          func() (config.Config, error)
	ProjectRegistry func() (project.Registry, error)
	Prompter        func() *prompter.Prompter
}

// TrustStatus compares the project's current config trust digests with the
// ones recorded in the registry.
type TrustStatus struct {
	Entry   project.ProjectEntry
	Current map[string]string
	// Changed lists the covered key groups whose digest differs from the
	// recorded one. Empty when the recorded and current digests agree.
	Changed []string
	// FirstRun is true when the registry holds no recorded digests.
	FirstRun bool
}

// ProjectTrustStatus computes the trust status of the registered project
// the config is anchored at. The second value is false when the config is
// not anchored at a registered project: there is nothing to trust.
func ProjectTrustStatus(cfg config.Config, reg project.Registry) (*TrustStatus, bool, error) {
	root := cfg.ProjectRoot()
	if root == "" {
		return nil, false, nil
	}
	entry, ok, err := reg.ProjectByRoot(root)
	if err != nil {
		return nil, false, fmt.Errorf("reading project registry: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	current, err := config.ProjectTrustDigests(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("computing project config trust: %w", err)
	}
	return &TrustStatus{
		Entry:    entry,
		Current:  current,
		Changed:  config.ChangedTrustGroups(entry.ConfigTrust, current),
		FirstRun: entry.ConfigTrust == nil,
	}, true, nil
}

// RecordProjectTrust stores the current digests as the accepted ones.
func RecordProjectTrust(reg project.Registry, status *TrustStatus) error {
	entry := status.Entry
	entry.ConfigTrust = status.Current
	if entry.ConfigTrust == nil {
		// A project that sets no covered key still records an accepted
		// (empty) state, so a later addition is a change the user sees.
		entry.ConfigTrust = map[string]string{}
	}
	if _, err := reg.Update(entry); err != nil {
		return fmt.Errorf("recording project config trust: %w", err)
	}
	return nil
}

// EnsureProjectConfigTrusted gates every host action on the project config.
// The first run records the current settings. A later change to a covered
// key group asks the user to accept it, or fails when no prompt is possible.
// Outside a registered project it does nothing.
func EnsureProjectConfigTrusted(deps TrustDeps) error {
	cfg, err := deps.Config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.ProjectRoot() == "" {
		return nil
	}
	reg, err := deps.ProjectRegistry()
	if err != nil {
		return fmt.Errorf("loading project registry: %w", err)
	}
	status, registered, err := ProjectTrustStatus(cfg, reg)
	if err != nil || !registered {
		return err
	}
	ios := deps.IOStreams
	switch {
	case status.FirstRun:
		fmt.Fprintln(ios.ErrOut, "Trusting the project config security settings (first run). "+
			"Review them with `clawker project trust --check`.")
	case len(status.Changed) == 0:
		return nil
	case !ios.CanPrompt():
		return fmt.Errorf("%w: %s; review the project config, then run `clawker project trust`",
			ErrProjectConfigUntrusted, strings.Join(status.Changed, ", "))
	default:
		confirmed, promptErr := deps.Prompter().Confirm(fmt.Sprintf(
			"Project config security settings changed since the last trusted run: %s. Accept?",
			strings.Join(status.Changed, ", ")), false)
		if promptErr != nil {
			return fmt.Errorf("confirming project config trust: %w", promptErr)
		}
		if !confirmed {
			return cmdutil.SilentError
		}
	}
	return RecordProjectTrust(reg, status)
}
