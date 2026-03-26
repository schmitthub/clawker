// Package init provides the project initialization subcommand.
package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// ProjectInitOptions contains the options for the project init command.
type ProjectInitOptions struct {
	IOStreams      *iostreams.IOStreams
	TUI            *tui.TUI
	Config         func() (config.Config, error)
	Logger         func() (*logger.Logger, error)
	ProjectManager func() (project.ProjectManager, error)

	Name  string // Positional arg: project name
	Force bool
	Yes   bool // Non-interactive mode
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

Use --yes/-y to skip prompts and use the Bare preset with all defaults.`,
		Example: `  # Interactive setup with preset picker
  clawker project init

  # Specify project name (still prompts for preset)
  clawker project init my-project

  # Non-interactive with Bare preset defaults
  clawker project init --yes

  # Overwrite existing configuration
  clawker project init --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Name = args[0]
			}
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return Run(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite existing configuration files")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Non-interactive mode, accept all defaults")

	return cmd
}

// Run executes the project init command logic.
func Run(ctx context.Context, opts *ProjectInitOptions) error {
	if opts.Yes || !opts.IOStreams.IsInteractive() {
		return runNonInteractive(ctx, opts)
	}
	return runInteractive(ctx, opts)
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

	// Resolve preset and branching.
	projectName := result.Values["project_name"]
	presetName := result.Values["preset"]
	action := result.Values["action"]

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
		configPath:  configPath,
		wd:          env.wd,
		force:       opts.Force,
		customize:   customize,
	})
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

	preset, ok := presetByName(config.Presets(), "Bare")
	if !ok {
		return fmt.Errorf("internal error: Bare preset not found")
	}

	configPath := filepath.Join(env.wd, env.configFileName)

	return performProjectSetup(ctx, performSetupInput{
		ios:         ios,
		log:         env.log,
		cfg:         env.cfg,
		pm:          env.pm,
		projectName: env.projectName,
		preset:      preset,
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
