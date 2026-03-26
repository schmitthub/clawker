// Package init provides the project initialization subcommand.
package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/schmitthub/clawker/internal/cmd/project/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/storeui"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

var projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

const (
	actionSave      = "Save and get started"
	actionCustomize = "Customize this preset"
)

// VCS provider constants.
const (
	vcsGitHub    = "github"
	vcsGitLab    = "gitlab"
	vcsBitbucket = "bitbucket"
)

// VCS protocol constants.
const (
	protoHTTPS = "https"
	protoSSH   = "ssh"
)

// vcsProviderDomains maps VCS provider keys to the HTTPS domains they require.
var vcsProviderDomains = map[string][]string{
	vcsGitHub:    {"github.com", "api.github.com"},
	vcsGitLab:    {"gitlab.com", "registry.gitlab.com"},
	vcsBitbucket: {"bitbucket.org", "api.bitbucket.org"},
}

// vcsSSHHosts maps VCS provider keys to the host that needs a port 22 rule.
var vcsSSHHosts = map[string]string{
	vcsGitHub:    "github.com",
	vcsGitLab:    "gitlab.com",
	vcsBitbucket: "bitbucket.org",
}

// vcsProviders returns the valid --vcs flag values.
func vcsProviders() []string { return []string{vcsGitHub, vcsGitLab, vcsBitbucket} }

// vcsProtocols returns the valid --git-protocol flag values.
func vcsProtocols() []string { return []string{protoHTTPS, protoSSH} }

// vcsSettings holds resolved VCS configuration from wizard or flags.
type vcsSettings struct {
	Provider   string // github, gitlab, bitbucket
	Protocol   string // https, ssh
	ForwardGPG bool
}

// defaultVCSSettings returns the defaults for non-interactive mode.
func defaultVCSSettings() vcsSettings {
	return vcsSettings{Provider: vcsGitHub, Protocol: protoHTTPS, ForwardGPG: true}
}

// applyVCSToProject mutates a *config.Project with VCS-derived config:
//   - Appends provider domains to security.firewall.add_domains
//   - If SSH: appends an EgressRule for port 22
//   - If GPG disabled: sets security.git_credentials.forward_gpg = false
func applyVCSToProject(p *config.Project, s vcsSettings) {
	domains := vcsProviderDomains[s.Provider]
	if p.Security.Firewall == nil {
		p.Security.Firewall = &config.FirewallConfig{}
	}

	// Append provider domains (dedup).
	existing := make(map[string]bool, len(p.Security.Firewall.AddDomains))
	for _, d := range p.Security.Firewall.AddDomains {
		existing[d] = true
	}
	for _, d := range domains {
		if !existing[d] {
			p.Security.Firewall.AddDomains = append(p.Security.Firewall.AddDomains, d)
		}
	}

	// SSH: add port 22 egress rule for the provider host.
	if s.Protocol == protoSSH {
		host := vcsSSHHosts[s.Provider]
		p.Security.Firewall.Rules = append(p.Security.Firewall.Rules, config.EgressRule{
			Dst:    host,
			Port:   22,
			Proto:  "ssh",
			Action: "allow",
		})
	}

	// GPG: set forward_gpg to false if disabled (defaults are all true).
	if !s.ForwardGPG {
		if p.Security.GitCredentials == nil {
			p.Security.GitCredentials = &config.GitCredentialsConfig{}
		}
		f := false
		p.Security.GitCredentials.ForwardGPG = &f
	}
}

// IsValidVCSProvider checks if a string is a valid --vcs value.
func IsValidVCSProvider(s string) bool {
	return slices.Contains(vcsProviders(), s)
}

// IsValidGitProtocol checks if a string is a valid --git-protocol value.
func IsValidGitProtocol(s string) bool {
	return slices.Contains(vcsProtocols(), s)
}

// VCSProviderNames returns valid provider names for error messages.
func VCSProviderNames() []string { return vcsProviders() }

// GitProtocolNames returns valid protocol names for error messages.
func GitProtocolNames() []string { return vcsProtocols() }

// ProjectInitOptions contains the options for the project init command.
type ProjectInitOptions struct {
	IOStreams      *iostreams.IOStreams
	TUI            *tui.TUI
	Config         func() (config.Config, error)
	Logger         func() (*logger.Logger, error)
	ProjectManager func() (project.ProjectManager, error)

	Name        string // Positional arg: project name
	Preset      string // --preset flag: select a preset by name
	VCS         string // --vcs flag: github|gitlab|bitbucket
	GitProtocol string // --git-protocol flag: https|ssh
	NoGPG       bool   // --no-gpg flag: disable GPG forwarding
	Force       bool
	Yes         bool // Non-interactive mode
}

// NewCmdProjectInit creates the project init command.
func NewCmdProjectInit(f *cmdutil.Factory, runF func(context.Context, *ProjectInitOptions) error) *cobra.Command {
	opts := &ProjectInitOptions{
		IOStreams:      f.IOStreams,
		TUI:            f.TUI,
		Config:         f.Config,
		Logger:         f.Logger,
		ProjectManager: f.ProjectManager,
	}

	cmd := &cobra.Command{
		Use:   "init [project-name]",
		Short: "Initialize a new clawker project in the current directory",
		Long: `Creates a .clawker.yaml configuration file and .clawkerignore in the current directory.

Provides language-based presets for quick setup, plus a "Build from scratch" path
that walks through each config field step by step.

If no project name is provided, you will be prompted to enter one (or accept the
current directory name as the default).

Use --yes/-y to skip all prompts (defaults to Bare preset with GitHub HTTPS).
Combine --yes with --preset, --vcs, --git-protocol, and --no-gpg for full control.`,
		Example: `  # Interactive setup with preset picker and VCS config
  clawker project init

  # Non-interactive with Bare preset defaults
  clawker project init --yes

  # Non-interactive with a specific preset and VCS
  clawker project init --yes --preset Go --vcs github
  clawker project init --yes --preset Python --vcs gitlab --git-protocol ssh

  # Non-interactive with SSH and GPG disabled
  clawker project init --yes --preset Rust --vcs github --git-protocol ssh --no-gpg

  # Overwrite existing configuration
  clawker project init --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Name = args[0]
			}
			if opts.Preset != "" && !opts.Yes {
				return cmdutil.FlagErrorf("--preset requires --yes")
			}
			if (opts.VCS != "" || opts.GitProtocol != "" || opts.NoGPG) && !opts.Yes {
				return cmdutil.FlagErrorf("--vcs, --git-protocol, and --no-gpg require --yes")
			}
			if opts.VCS != "" && !IsValidVCSProvider(opts.VCS) {
				return cmdutil.FlagErrorf("invalid --vcs value %q; valid: %s", opts.VCS, strings.Join(vcsProviders(), ", "))
			}
			if opts.GitProtocol != "" && !IsValidGitProtocol(opts.GitProtocol) {
				return cmdutil.FlagErrorf("invalid --git-protocol value %q; valid: %s", opts.GitProtocol, strings.Join(vcsProtocols(), ", "))
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return Run(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite existing configuration files")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")
	cmd.Flags().StringVar(&opts.Preset, "preset", "", "Select a language preset (requires --yes)")
	cmd.Flags().StringVar(&opts.VCS, "vcs", "", "VCS provider: github, gitlab, bitbucket (requires --yes)")
	cmd.Flags().StringVar(&opts.GitProtocol, "git-protocol", "", "Git protocol: https, ssh (requires --yes)")
	cmd.Flags().BoolVar(&opts.NoGPG, "no-gpg", false, "Disable GPG agent forwarding (requires --yes)")

	cmd.RegisterFlagCompletionFunc("preset", func(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) { //nolint:errcheck // cobra registers completion internally
		return PresetCompletions(), cobra.ShellCompDirectiveNoFileComp
	})
	cmd.RegisterFlagCompletionFunc("vcs", func(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) { //nolint:errcheck
		return VCSCompletions(), cobra.ShellCompDirectiveNoFileComp
	})
	cmd.RegisterFlagCompletionFunc("git-protocol", func(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) { //nolint:errcheck
		return GitProtocolCompletions(), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// Run executes the project init command logic.
func Run(ctx context.Context, opts *ProjectInitOptions) error {
	if opts.Yes || !opts.IOStreams.IsInteractive() {
		return runNonInteractive(ctx, opts)
	}
	return runInteractive(ctx, opts)
}

// PresetCompletions builds cobra.Completion values from config.Presets()
// for shell completion of the --preset flag.
func PresetCompletions() []cobra.Completion {
	presets := config.Presets()
	completions := make([]cobra.Completion, 0, len(presets))
	for _, p := range presets {
		if p.AutoCustomize {
			continue // "Build from scratch" is interactive-only
		}
		completions = append(completions, cobra.CompletionWithDesc(p.Name, p.Description))
	}
	return completions
}

// VCSCompletions returns cobra completions for the --vcs flag.
func VCSCompletions() []cobra.Completion {
	return []cobra.Completion{
		cobra.CompletionWithDesc(vcsGitHub, "GitHub (github.com)"),
		cobra.CompletionWithDesc(vcsGitLab, "GitLab (gitlab.com)"),
		cobra.CompletionWithDesc(vcsBitbucket, "Bitbucket (bitbucket.org)"),
	}
}

// GitProtocolCompletions returns cobra completions for the --git-protocol flag.
func GitProtocolCompletions() []cobra.Completion {
	return []cobra.Completion{
		cobra.CompletionWithDesc(protoHTTPS, "HTTPS credential forwarding"),
		cobra.CompletionWithDesc(protoSSH, "SSH key authentication"),
	}
}

// wizardContext captures external state needed by wizard field definitions.
type wizardContext struct {
	configExists   bool
	force          bool
	nameDefault    string
	configFileName string
	presets        []config.Preset
}

// overwriteDeclined returns true when the overwrite field was answered "no".
func overwriteDeclined(vals tui.WizardValues) bool {
	return vals["overwrite"] == "no"
}

// initEnv holds the resolved dependencies and derived state shared by both
// the interactive and non-interactive init paths.
type initEnv struct {
	log            *logger.Logger
	cfg            config.Config
	pm             project.ProjectManager
	wd             string
	dirName        string
	configFileName string
	configExists   bool
	projectName    string // default name (may be overridden by wizard)
}

// resolveInitEnv resolves factory lazy closures, bootstraps settings, and
// computes derived state that both runInteractive and runNonInteractive need.
func resolveInitEnv(opts *ProjectInitOptions) (*initEnv, error) {
	log, err := opts.Logger()
	if err != nil {
		return nil, fmt.Errorf("initializing logger: %w", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	cfg, err := opts.Config()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	pm, err := opts.ProjectManager()
	if err != nil {
		return nil, fmt.Errorf("initializing project manager: %w", err)
	}

	// Ensure settings.yaml exists with schema defaults. The store's virtual
	// defaults layer makes the entire file dirty when no physical file exists,
	// so Write() persists it. If the file already exists, Write() is a no-op.
	if bsErr := cfg.SettingsStore().Write(); bsErr != nil {
		log.Warn().Err(bsErr).Msg("settings bootstrap failed")
		fmt.Fprintf(opts.IOStreams.ErrOut, "Warning: could not create settings file: %s\n", bsErr)
	}

	configFileName := "." + cfg.ProjectConfigFileName()
	configExists := shared.HasLocalProjectConfig(cfg, wd)

	absPath, err := filepath.Abs(wd)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	dirName := filepath.Base(absPath)

	projectName := strings.ToLower(dirName)
	if opts.Name != "" {
		projectName = strings.ToLower(opts.Name)
	}

	return &initEnv{
		log:            log,
		cfg:            cfg,
		pm:             pm,
		wd:             wd,
		dirName:        dirName,
		configFileName: configFileName,
		configExists:   configExists,
		projectName:    projectName,
	}, nil
}

// runInteractive runs the preset-based interactive flow.
func runInteractive(ctx context.Context, opts *ProjectInitOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	env, err := resolveInitEnv(opts)
	if err != nil {
		return err
	}

	fmt.Fprintln(ios.Out, "Setting up clawker project...")
	fmt.Fprintln(ios.Out)

	presets := config.Presets()
	wctx := wizardContext{
		configExists:   env.configExists,
		force:          opts.Force,
		nameDefault:    env.projectName,
		configFileName: env.configFileName,
		presets:        presets,
	}
	result, err := opts.TUI.RunWizard(buildInitWizardSteps(wctx))
	if err != nil {
		return fmt.Errorf("wizard failed: %w", err)
	}
	if !result.Submitted {
		fmt.Fprintln(ios.Out, "Setup cancelled.")
		return nil
	}

	// Handle overwrite-declined: register only.
	if overwriteDeclined(result.Values) {
		if _, regErr := env.pm.Register(ctx, strings.ToLower(env.dirName), env.wd); regErr != nil {
			env.log.Debug().Err(regErr).Msg("failed to register project during init (non-overwrite path)")
			return fmt.Errorf("could not register project: %w", regErr)
		}
		fmt.Fprintf(ios.Out, "%s Registered project '%s'\n", cs.SuccessIcon(), strings.ToLower(env.dirName))
		return nil
	}

	// Resolve preset, VCS, and branching.
	projectName := result.Values["project_name"]
	presetName := result.Values["preset"]
	action := result.Values["action"]
	vcs := vcsSettingsFromWizard(result.Values)

	preset, ok := presetByName(presets, presetName)
	if !ok {
		return fmt.Errorf("unknown preset: %s", presetName)
	}

	configPath := filepath.Join(env.wd, env.configFileName)
	customize := preset.AutoCustomize || action == actionCustomize

	return performProjectSetup(ctx, performSetupInput{
		ios:         ios,
		tui:         opts.TUI,
		log:         env.log,
		cfg:         env.cfg,
		pm:          env.pm,
		projectName: projectName,
		preset:      preset,
		vcs:         vcs,
		configPath:  configPath,
		wd:          env.wd,
		force:       opts.Force,
		customize:   customize,
	})
}

// vcsSettingsFromWizard extracts VCS settings from wizard values,
// mapping display labels to internal keys.
func vcsSettingsFromWizard(vals tui.WizardValues) vcsSettings {
	s := defaultVCSSettings()

	switch vals["vcs_provider"] {
	case "GitHub":
		s.Provider = vcsGitHub
	case "GitLab":
		s.Provider = vcsGitLab
	case "Bitbucket":
		s.Provider = vcsBitbucket
	}

	switch vals["git_protocol"] {
	case "HTTPS":
		s.Protocol = protoHTTPS
	case "SSH":
		s.Protocol = protoSSH
	}

	if vals["gpg_forward"] == "no" {
		s.ForwardGPG = false
	}

	return s
}

// runNonInteractive runs the non-interactive (--yes) path with no prompts.
func runNonInteractive(ctx context.Context, opts *ProjectInitOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()

	env, err := resolveInitEnv(opts)
	if err != nil {
		return err
	}

	if env.configExists && !opts.Force {
		fmt.Fprintf(ios.ErrOut, "%s %s already exists\n", cs.FailureIcon(), env.configFileName)
		fmt.Fprintln(ios.ErrOut)
		fmt.Fprintln(ios.ErrOut, "Next Steps:")
		fmt.Fprintln(ios.ErrOut, "  - Use --force to overwrite the existing configuration")
		fmt.Fprintln(ios.ErrOut, "  - Or edit the existing .clawker.yaml manually")
		fmt.Fprintln(ios.ErrOut, "  - Or run 'clawker project register' to register the existing project")
		return fmt.Errorf("configuration already exists")
	}

	fmt.Fprintln(ios.ErrOut, "Setting up clawker project...")
	fmt.Fprintln(ios.ErrOut)

	presetName := "Bare"
	if opts.Preset != "" {
		presetName = opts.Preset
	}

	preset, ok := presetByName(config.Presets(), presetName)
	if !ok {
		return fmt.Errorf("unknown preset %q (see --help for available presets)", presetName)
	}

	vcs := defaultVCSSettings()
	if opts.VCS != "" {
		vcs.Provider = opts.VCS
	}
	if opts.GitProtocol != "" {
		vcs.Protocol = opts.GitProtocol
	}
	if opts.NoGPG {
		vcs.ForwardGPG = false
	}

	configPath := filepath.Join(env.wd, env.configFileName)

	return performProjectSetup(ctx, performSetupInput{
		ios:         ios,
		log:         env.log,
		cfg:         env.cfg,
		pm:          env.pm,
		projectName: env.projectName,
		preset:      preset,
		vcs:         vcs,
		configPath:  configPath,
		wd:          env.wd,
		force:       opts.Force,
	})
}

// performSetupInput groups pre-resolved dependencies for performProjectSetup,
// avoiding repeated calls to factory lazy closures.
type performSetupInput struct {
	ios         *iostreams.IOStreams
	tui         *tui.TUI
	log         *logger.Logger
	cfg         config.Config
	pm          project.ProjectManager
	projectName string
	preset      config.Preset
	vcs         vcsSettings
	configPath  string
	wd          string
	force       bool
	customize   bool
}

// performProjectSetup creates the project config from a preset, optionally runs
// the customize wizard, writes files, and registers the project.
func performProjectSetup(ctx context.Context, in performSetupInput) error {
	cs := in.ios.ColorScheme()

	if err := validateProjectName(in.projectName); err != nil {
		return fmt.Errorf("invalid project name %q: %w", in.projectName, err)
	}

	configFileName := filepath.Base(in.configPath)
	ignoreFileName := in.cfg.ClawkerIgnoreName()
	ignorePath := filepath.Join(in.wd, ignoreFileName)

	in.log.Debug().
		Str("project", in.projectName).
		Str("preset", in.preset.Name).
		Str("workdir", in.wd).
		Bool("customize", in.customize).
		Bool("force", in.force).
		Msg("initializing project")

	// Construct a config with the preset YAML as the project store's virtual
	// defaults layer. Walk-up + config dir discovery layers existing files on
	// top. The preset values are in the base layer — if no project file exists
	// yet, Write() persists them to create one.
	presetCfg, err := config.NewConfig(config.WithDefaultProjectYAML(in.preset.YAML))
	if err != nil {
		return fmt.Errorf("loading config with preset %q: %w", in.preset.Name, err)
	}
	store := presetCfg.ProjectStore()

	// Apply VCS configuration (provider domains, SSH rules, GPG settings).
	if err := store.Set(func(p *config.Project) {
		applyVCSToProject(p, in.vcs)
	}); err != nil {
		return fmt.Errorf("applying VCS config: %w", err)
	}

	if in.customize {
		browser, buildErr := storeui.BuildBrowser(
			store,
			storeui.WithTitle("Customize "+in.preset.Name),
			storeui.WithOnlyPaths(customizeFields()...),
			storeui.WithOverrides(customizeOverrides()),
			storeui.WithLayerTargets([]storeui.LayerTarget{
				{Label: "Project", Description: storeui.ShortenHome(in.configPath), Path: in.configPath},
			}),
		)
		if buildErr != nil {
			return fmt.Errorf("building customize browser: %w", buildErr)
		}

		customizeResult, wizErr := in.tui.RunWizard([]tui.WizardStep{
			{
				ID:       "customize",
				Title:    "Customize",
				Page:     tui.NewBrowserPage(browser),
				HelpKeys: []string{"↑↓", "navigate", "enter", "edit", "q", "done", "esc", "back", "ctrl+c", "quit"},
			},
		})
		if wizErr != nil {
			return fmt.Errorf("customize wizard: %w", wizErr)
		}
		if !customizeResult.Submitted {
			fmt.Fprintln(in.ios.Out, "Setup cancelled.")
			return nil
		}
	}

	// Persist the store to the project config file. WriteTo routes all
	// dirty paths (preset defaults + any customize edits) to this file.
	if err := store.WriteTo(in.configPath); err != nil {
		return fmt.Errorf("writing %s: %w", configFileName, err)
	}
	in.log.Debug().Str("file", in.configPath).Msg("created configuration file")

	// Create .clawkerignore if it doesn't exist (or --force).
	ignoreCreated := false
	_, statErr := os.Stat(ignorePath)
	switch {
	case statErr != nil && !os.IsNotExist(statErr):
		return fmt.Errorf("checking %s: %w", ignoreFileName, statErr)
	case os.IsNotExist(statErr) || in.force:
		if err := os.WriteFile(ignorePath, []byte(config.DefaultIgnoreFile), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", ignoreFileName, err)
		}
		in.log.Debug().Str("file", ignorePath).Msg("created ignore file")
		ignoreCreated = true
	}

	fmt.Fprintln(in.ios.Out)
	fmt.Fprintf(in.ios.Out, "%s Created: %s\n", cs.SuccessIcon(), configFileName)
	if ignoreCreated {
		fmt.Fprintf(in.ios.Out, "%s Created: %s\n", cs.SuccessIcon(), ignoreFileName)
	} else {
		fmt.Fprintf(in.ios.Out, "%s Exists:  %s\n", cs.InfoIcon(), ignoreFileName)
	}
	fmt.Fprintf(in.ios.Out, "%s Project: %s (preset: %s)\n", cs.InfoIcon(), in.projectName, in.preset.Name)

	if _, err := in.pm.Register(ctx, in.projectName, in.wd); err != nil {
		return fmt.Errorf("could not register project: %w", err)
	}

	fmt.Fprintln(in.ios.Out)
	fmt.Fprintln(in.ios.Out, "Next Steps:")
	fmt.Fprintf(in.ios.Out, "  1. Run 'clawker build' to build your project's container image\n")
	fmt.Fprintf(in.ios.Out, "  2. Run 'clawker run -it --agent <agent-name> @' to start a container\n")
	fmt.Fprintln(in.ios.Out)
	fmt.Fprintf(in.ios.Out, "To customize further, run 'clawker project edit'\n")
	return nil
}

// buildInitWizardSteps returns wizard steps for the setup flow:
// overwrite confirmation, project name, preset picker, and save-or-customize action.
func buildInitWizardSteps(wctx wizardContext) []tui.WizardStep {
	presetOptions := make([]tui.FieldOption, len(wctx.presets))
	for i, p := range wctx.presets {
		presetOptions[i] = tui.FieldOption{
			Label:       p.Name,
			Description: p.Description,
		}
	}

	return []tui.WizardStep{
		{
			ID:    "overwrite",
			Title: "Overwrite",
			Page: tui.NewConfirmPage(
				"overwrite",
				fmt.Sprintf("%s already exists. Overwrite?", wctx.configFileName),
				false,
			),
			HelpKeys: []string{"←→", "toggle", "y/n", "set", "enter", "confirm", "esc", "back", "ctrl+c", "quit"},
			SkipIf: func(_ tui.WizardValues) bool {
				return !wctx.configExists || wctx.force
			},
		},
		{
			ID:    "project_name",
			Title: "Project",
			Page: tui.NewTextPage(
				"project_name",
				"Project name",
				tui.WithDefault(wctx.nameDefault),
				tui.WithPlaceholder("my-project"),
				tui.WithRequired(),
				tui.WithValidator(validateProjectName),
			),
			HelpKeys: []string{"enter", "confirm", "esc", "back", "ctrl+c", "quit"},
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:       "preset",
			Title:    "Template",
			Page:     tui.NewSelectPage("preset", "Choose a starting template", presetOptions, 0),
			HelpKeys: []string{"↑↓", "select", "enter", "confirm", "esc", "back", "ctrl+c", "quit"},
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:    "vcs_provider",
			Title: "VCS",
			Page: tui.NewSelectPage("vcs_provider", "Which VCS provider do you use?", []tui.FieldOption{
				{Label: "GitHub", Description: "github.com"},
				{Label: "GitLab", Description: "gitlab.com"},
				{Label: "Bitbucket", Description: "bitbucket.org"},
			}, 0),
			HelpKeys: []string{"↑↓", "select", "enter", "confirm", "esc", "back", "ctrl+c", "quit"},
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:    "git_protocol",
			Title: "Protocol",
			Page: tui.NewSelectPage("git_protocol", "Which git protocol do you use?", []tui.FieldOption{
				{Label: "HTTPS", Description: "Credential-based authentication"},
				{Label: "SSH", Description: "SSH key authentication"},
			}, 0),
			HelpKeys: []string{"↑↓", "select", "enter", "confirm", "esc", "back", "ctrl+c", "quit"},
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:       "gpg_forward",
			Title:    "GPG",
			Page:     tui.NewConfirmPage("gpg_forward", "Forward GPG agent for commit signing?", true),
			HelpKeys: []string{"←→", "toggle", "y/n", "set", "enter", "confirm", "esc", "back", "ctrl+c", "quit"},
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:    "action",
			Title: "Action",
			Page: tui.NewSelectPage("action", "What would you like to do?", []tui.FieldOption{
				{Label: actionSave, Description: "Write config and start building"},
				{Label: actionCustomize, Description: "Walk through key config fields before saving"},
			}, 0),
			HelpKeys: []string{"↑↓", "select", "enter", "confirm", "esc", "back", "ctrl+c", "quit"},
			SkipIf: func(vals tui.WizardValues) bool {
				if overwriteDeclined(vals) {
					return true
				}
				preset, ok := presetByName(wctx.presets, vals["preset"])
				return ok && preset.AutoCustomize
			},
		},
	}
}

// customizeFields returns the dotted paths shown in the customize browser.
func customizeFields() []string {
	return []string{
		"build.image",
		"build.packages",
		"build.instructions.root_run",
		"build.instructions.user_run",
		"build.inject.after_from",
		"build.inject.after_packages",
		"security.firewall.add_domains",
		"workspace.default_mode",
	}
}

// customizeOverrides returns overrides for the customize browser.
func customizeOverrides() []storeui.Override {
	return []storeui.Override{
		{
			Path:    "workspace.default_mode",
			Kind:    storeui.Ptr(storeui.KindSelect),
			Options: []string{"bind", "snapshot"},
		},
	}
}

// presetByName finds a preset by its display name.
func presetByName(presets []config.Preset, name string) (config.Preset, bool) {
	for _, p := range presets {
		if p.Name == name {
			return p, true
		}
	}
	return config.Preset{}, false
}

// validateProjectName checks that a project name is valid for clawker resource
// naming. Stricter than Docker's container name rules: lowercase-only, must
// start with a letter or digit.
func validateProjectName(s string) error {
	if s == "" {
		return fmt.Errorf("project name is required")
	}
	if s != strings.ToLower(s) {
		return fmt.Errorf("must be lowercase (try %q)", strings.ToLower(s))
	}
	if strings.Contains(s, " ") {
		return fmt.Errorf("must not contain spaces")
	}
	if !projectNameRe.MatchString(s) {
		return fmt.Errorf("must start with a letter or digit, and contain only lowercase letters, digits, dots, underscores, or hyphens")
	}
	return nil
}
