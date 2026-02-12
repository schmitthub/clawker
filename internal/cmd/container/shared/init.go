// Package shared provides domain logic shared between container run and create commands.
package shared

import (
	"context"
	"fmt"
	"os"

	copts "github.com/schmitthub/clawker/internal/cmd/container/opts"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/spf13/pflag"
)

// ContainerInitializer is the Factory noun for container initialization.
// Constructed from Factory, captures eager + lazy deps. Run() performs
// the progress-tracked initialization with runtime values.
type ContainerInitializer struct {
	ios       *iostreams.IOStreams
	tui       *tui.TUI
	gitMgr    func() (*git.GitManager, error)
	hostProxy func() *hostproxy.Manager
}

// NewContainerInitializer creates a ContainerInitializer from the Factory.
func NewContainerInitializer(f *cmdutil.Factory) *ContainerInitializer {
	return &ContainerInitializer{
		ios:       f.IOStreams,
		tui:       f.TUI,
		gitMgr:    f.GitManager,
		hostProxy: f.HostProxy,
	}
}

// InitParams holds runtime values resolved during the pre-progress phase.
type InitParams struct {
	Client           *docker.Client
	Config           *config.Project
	ContainerOptions *copts.ContainerOptions
	Flags            *pflag.FlagSet
	Image            string // resolved image reference
	StartAfterCreate bool   // true for detached run (adds "Start container" step)
	AltScreen        bool   // use alternate screen for progress (clears display for clean TTY handoff)
}

// InitResult holds outputs needed by the post-progress phase.
type InitResult struct {
	ContainerID      string
	AgentName        string
	ContainerName    string
	HostProxyRunning bool
	Warnings         []string // deferred warnings (can't print during progress)
}

// initOutcome carries the result from the init goroutine to the caller
// via a channel, providing explicit synchronization (no shared variables).
type initOutcome struct {
	result *InitResult
	err    error
}

// Run performs container initialization with TUI progress display.
// It expects image resolution to have already been completed (pre-progress phase).
func (ci *ContainerInitializer) Run(ctx context.Context, params InitParams) (*InitResult, error) {
	ch := make(chan tui.ProgressStep, 32)
	doneCh := make(chan initOutcome, 1)

	go func() {
		defer close(ch)
		result, err := ci.runSteps(ctx, params, ch)
		doneCh <- initOutcome{result: result, err: err}
	}()

	progressResult := ci.tui.RunProgress("auto", tui.ProgressDisplayConfig{
		Title:          "Initializing",
		Subtitle:       params.Config.Project,
		CompletionVerb: "Ready",
		AltScreen:      params.AltScreen,
	}, ch)

	outcome := <-doneCh // explicit sync — goroutine always sends before close(ch)

	if progressResult.Err != nil {
		return nil, progressResult.Err
	}
	if outcome.err != nil {
		return nil, outcome.err
	}
	return outcome.result, nil
}

// runSteps executes the initialization steps, sending progress events to ch.
func (ci *ContainerInitializer) runSteps(ctx context.Context, params InitParams, ch chan<- tui.ProgressStep) (*InitResult, error) {
	cfg := params.Config
	containerOpts := params.ContainerOptions
	client := params.Client

	result := &InitResult{}

	// --- Step 1: Prepare workspace ---
	sendStep(ctx, ch, "workspace", "Prepare workspace", tui.StepRunning)

	agentName := containerOpts.GetAgentName()
	if agentName == "" {
		agentName = docker.GenerateRandomName()
	}
	result.AgentName = agentName
	result.ContainerName = docker.ContainerName(cfg.Project, agentName)

	wd, projectRootDir, err := ci.resolveWorkDir(ctx, containerOpts, cfg, agentName)
	if err != nil {
		sendStep(ctx, ch, "workspace", "Prepare workspace", tui.StepError)
		return nil, err
	}

	wsResult, err := workspace.SetupMounts(ctx, client, workspace.SetupMountsConfig{
		ModeOverride:   containerOpts.Mode,
		Config:         cfg,
		AgentName:      agentName,
		WorkDir:        wd,
		ProjectRootDir: projectRootDir,
	})
	if err != nil {
		sendStep(ctx, ch, "workspace", "Prepare workspace", tui.StepError)
		return nil, err
	}
	workspaceMounts := wsResult.Mounts
	sendStep(ctx, ch, "workspace", "Prepare workspace", tui.StepComplete)

	// --- Step 2: Initialize config ---
	if wsResult.ConfigVolumeResult.ConfigCreated {
		sendStep(ctx, ch, "config", "Initialize config", tui.StepRunning)
		if err := InitContainerConfig(ctx, InitConfigOpts{
			ProjectName:      cfg.Project,
			AgentName:        agentName,
			ContainerWorkDir: cfg.Workspace.RemotePath,
			ClaudeCode:       cfg.Agent.ClaudeCode,
			CopyToVolume:     client.CopyToVolume,
		}); err != nil {
			sendStep(ctx, ch, "config", "Initialize config", tui.StepError)
			return nil, fmt.Errorf("container init: %w", err)
		}
		sendStep(ctx, ch, "config", "Initialize config", tui.StepComplete)
	} else {
		sendStep(ctx, ch, "config", "Initialize config", tui.StepCached)
	}

	// --- Step 3: Setup environment ---
	sendStep(ctx, ch, "environment", "Setup environment", tui.StepRunning)

	hostProxyRunning := ci.setupHostProxy(cfg, containerOpts, result)

	gitSetup := workspace.SetupGitCredentials(cfg.Security.GitCredentials, hostProxyRunning)
	workspaceMounts = append(workspaceMounts, gitSetup.Mounts...)
	containerOpts.Env = append(containerOpts.Env, gitSetup.Env...)

	runtimeEnv, err := ci.buildRuntimeEnv(cfg, containerOpts, agentName, wd)
	if err != nil {
		sendStep(ctx, ch, "environment", "Setup environment", tui.StepError)
		return nil, err
	}
	containerOpts.Env = append(containerOpts.Env, runtimeEnv...)
	sendStep(ctx, ch, "environment", "Setup environment", tui.StepComplete)

	// --- Step 4: Create container ---
	sendStep(ctx, ch, "container", fmt.Sprintf("Create container (%s)", result.ContainerName), tui.StepRunning)

	if err := containerOpts.ValidateFlags(); err != nil {
		sendStep(ctx, ch, "container", fmt.Sprintf("Create container (%s)", result.ContainerName), tui.StepError)
		return nil, err
	}

	containerConfig, hostConfig, networkConfig, err := containerOpts.BuildConfigs(params.Flags, workspaceMounts, cfg)
	if err != nil {
		sendStep(ctx, ch, "container", fmt.Sprintf("Create container (%s)", result.ContainerName), tui.StepError)
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	extraLabels := map[string]string{
		docker.LabelProject: cfg.Project,
		docker.LabelAgent:   agentName,
	}

	resp, err := client.ContainerCreate(ctx, docker.ContainerCreateOptions{
		Config:           containerConfig,
		HostConfig:       hostConfig,
		NetworkingConfig: networkConfig,
		Name:             result.ContainerName,
		ExtraLabels:      docker.Labels{extraLabels},
		EnsureNetwork: &docker.EnsureNetworkOptions{
			Name: docker.NetworkName,
		},
	})
	if err != nil {
		sendStep(ctx, ch, "container", fmt.Sprintf("Create container (%s)", result.ContainerName), tui.StepError)
		return nil, fmt.Errorf("creating container: %w", err)
	}
	result.ContainerID = resp.ID

	// Inject onboarding file if host auth is enabled.
	if cfg.Agent.ClaudeCode.UseHostAuthEnabled() {
		if err := InjectOnboardingFile(ctx, InjectOnboardingOpts{
			ContainerID:     result.ContainerID,
			CopyToContainer: NewCopyToContainerFn(client),
		}); err != nil {
			sendStep(ctx, ch, "container", fmt.Sprintf("Create container (%s)", result.ContainerName), tui.StepError)
			return nil, fmt.Errorf("inject onboarding: %w", err)
		}
	}

	// Inject post-init script if configured.
	if cfg.Agent.PostInit != "" {
		if err := InjectPostInitScript(ctx, InjectPostInitOpts{
			ContainerID:     result.ContainerID,
			Script:          cfg.Agent.PostInit,
			CopyToContainer: NewCopyToContainerFn(client),
		}); err != nil {
			sendStep(ctx, ch, "container", fmt.Sprintf("Create container (%s)", result.ContainerName), tui.StepError)
			return nil, fmt.Errorf("inject post-init script: %w", err)
		}
	}

	for _, warning := range resp.Warnings {
		result.Warnings = append(result.Warnings, "Warning: "+warning)
	}

	sendStep(ctx, ch, "container", fmt.Sprintf("Create container (%s)", result.ContainerName), tui.StepComplete)

	// --- Step 5: Start container (detached mode only) ---
	if params.StartAfterCreate {
		sendStep(ctx, ch, "start", "Start container", tui.StepRunning)
		if _, err := client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: result.ContainerID}); err != nil {
			sendStep(ctx, ch, "start", "Start container", tui.StepError)
			return nil, fmt.Errorf("starting container: %w", err)
		}
		sendStep(ctx, ch, "start", "Start container", tui.StepComplete)
	}

	return result, nil
}

// resolveWorkDir determines the working directory for the container.
func (ci *ContainerInitializer) resolveWorkDir(_ context.Context, containerOpts *copts.ContainerOptions, cfg *config.Project, agentName string) (wd string, projectRootDir string, err error) {
	if containerOpts.Worktree != "" {
		wtSpec, err := cmdutil.ParseWorktreeFlag(containerOpts.Worktree, agentName)
		if err != nil {
			return "", "", fmt.Errorf("invalid --worktree flag: %w", err)
		}

		gitMgr, err := ci.gitMgr()
		if err != nil {
			return "", "", fmt.Errorf("cannot use --worktree: %w", err)
		}

		wd, err = gitMgr.SetupWorktree(cfg, wtSpec.Branch, wtSpec.Base)
		if err != nil {
			return "", "", fmt.Errorf("setting up worktree %q for agent %q: %w", wtSpec.Branch, agentName, err)
		}
		logger.Debug().Str("worktree", wd).Str("branch", wtSpec.Branch).Msg("using git worktree")
		return wd, cfg.RootDir(), nil
	}

	wd = cfg.RootDir()
	if wd == "" {
		wd, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("failed to get working directory: %w", err)
		}
	}
	return wd, "", nil
}

// setupHostProxy starts the host proxy if enabled. Non-fatal — failures produce warnings.
func (ci *ContainerInitializer) setupHostProxy(cfg *config.Project, containerOpts *copts.ContainerOptions, result *InitResult) bool {
	if !cfg.Security.HostProxyEnabled() {
		logger.Debug().Msg("host proxy disabled by config")
		return false
	}

	if ci.hostProxy == nil {
		return false
	}

	hp := ci.hostProxy()
	if hp == nil {
		return false
	}

	if err := hp.EnsureRunning(); err != nil {
		logger.Warn().Err(err).Msg("failed to start host proxy server")
		result.Warnings = append(result.Warnings,
			"Host proxy failed to start. Browser authentication may not work.",
			"To disable: set 'security.enable_host_proxy: false' in clawker.yaml",
		)
		return false
	}

	logger.Debug().Msg("host proxy started successfully")
	result.HostProxyRunning = true

	if hp.IsRunning() {
		envVar := "CLAWKER_HOST_PROXY=" + hp.ProxyURL()
		containerOpts.Env = append(containerOpts.Env, envVar)
		logger.Debug().Str("env", envVar).Msg("injected host proxy env var")
	}

	return true
}

// buildRuntimeEnv constructs container runtime environment variables.
func (ci *ContainerInitializer) buildRuntimeEnv(cfg *config.Project, containerOpts *copts.ContainerOptions, agentName, wd string) ([]string, error) {
	workspaceMode := containerOpts.Mode
	if workspaceMode == "" {
		workspaceMode = cfg.Workspace.DefaultMode
	}

	envOpts := docker.RuntimeEnvOpts{
		Project:         cfg.Project,
		Agent:           agentName,
		WorkspaceMode:   workspaceMode,
		WorkspaceSource: wd,
		Editor:          cfg.Agent.Editor,
		Visual:          cfg.Agent.Visual,
		Is256Color:      ci.ios.Is256ColorSupported(),
		TrueColor:       ci.ios.IsTrueColorSupported(),
		AgentEnv:        cfg.Agent.Env,
	}
	if cfg.Security.FirewallEnabled() {
		envOpts.FirewallEnabled = true
		envOpts.FirewallDomains = cfg.Security.Firewall.GetFirewallDomains(config.DefaultFirewallDomains)
		envOpts.FirewallOverride = cfg.Security.Firewall.IsOverrideMode()
		envOpts.FirewallIPRangeSources = cfg.Security.Firewall.GetIPRangeSources()
	}
	if cfg.Security.GitCredentials != nil {
		envOpts.GPGForwardingEnabled = cfg.Security.GitCredentials.GPGEnabled()
		envOpts.SSHForwardingEnabled = cfg.Security.GitCredentials.GitSSHEnabled()
	}
	if cfg.Build.Instructions != nil {
		envOpts.InstructionEnv = cfg.Build.Instructions.Env
	}

	return docker.RuntimeEnv(envOpts)
}

// sendStep sends a progress step event to the channel.
// Uses select with context to avoid blocking forever if the TUI consumer stops reading.
func sendStep(ctx context.Context, ch chan<- tui.ProgressStep, id, name string, status tui.ProgressStepStatus) {
	select {
	case <-ctx.Done():
	case ch <- tui.ProgressStep{
		ID:     id,
		Name:   name,
		Status: status,
	}:
	}
}
