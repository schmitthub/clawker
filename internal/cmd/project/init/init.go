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
	"github.com/schmitthub/clawker/internal/storage"
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

	if bsErr := bootstrapSettings(); bsErr != nil {
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
	result, err := opts.TUI.RunWizard(buildInitWizardFields(wctx))
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

	// Create a store from the preset YAML with schema defaults filling gaps.
	store, err := storage.NewFromString[config.Project](
		in.preset.YAML,
		storage.WithDefaultsFromStruct[config.Project](),
	)
	if err != nil {
		return fmt.Errorf("loading preset %q: %w", in.preset.Name, err)
	}

	// If customizing, run the store-backed wizard before writing.
	alreadySaved := false
	if in.customize {
		wizResult, wizErr := storeui.Wizard(
			in.tui,
			store,
			storeui.WithWizardTitle("Customize "+in.preset.Name+" preset"),
			storeui.WithWizardFields(customizeWizardFields()...),
			storeui.WithWizardOverrides(customizeWizardOverrides()...),
			storeui.WithWizardWritePath(in.configPath),
		)
		if wizErr != nil {
			return fmt.Errorf("customize wizard: %w", wizErr)
		}
		if wizResult.Cancelled {
			fmt.Fprintln(in.ios.Out, "Setup cancelled.")
			return nil
		}
		alreadySaved = wizResult.Saved
	}

	// Persist the store if the wizard did not already write it (Saved=true
	// means fields were modified and the wizard auto-wrote; if unchanged the
	// caller must still persist the preset).
	if !alreadySaved {
		if err := store.Write(storage.ToPath(in.configPath)); err != nil {
			return fmt.Errorf("writing %s: %w", configFileName, err)
		}
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

// bootstrapSettings ensures a settings.yaml exists with schema defaults.
// Creates the file if missing; no-op otherwise.
func bootstrapSettings() error {
	settingsPath, err := config.SettingsFilePath()
	if err != nil {
		return fmt.Errorf("resolving settings path: %w", err)
	}

	_, statErr := os.Stat(settingsPath)
	if statErr == nil {
		return nil // file exists
	}
	if !os.IsNotExist(statErr) {
		return fmt.Errorf("checking settings file: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	defaultsYAML := storage.GenerateDefaultsYAML[config.Settings]()
	if defaultsYAML == "" {
		defaultsYAML = "{}\n"
	}

	return os.WriteFile(settingsPath, []byte(defaultsYAML), 0644)
}

// buildInitWizardFields returns wizard fields for the setup flow:
// overwrite confirmation, project name, preset picker, and save-or-customize action.
func buildInitWizardFields(wctx wizardContext) []tui.WizardField {
	presetOptions := make([]tui.FieldOption, len(wctx.presets))
	for i, p := range wctx.presets {
		presetOptions[i] = tui.FieldOption{
			Label:       p.Name,
			Description: p.Description,
		}
	}

	return []tui.WizardField{
		{
			ID:         "overwrite",
			Title:      "Overwrite",
			Prompt:     fmt.Sprintf("%s already exists. Overwrite?", wctx.configFileName),
			Kind:       tui.FieldConfirm,
			DefaultYes: false,
			SkipIf: func(_ tui.WizardValues) bool {
				return !wctx.configExists || wctx.force
			},
		},
		{
			ID:          "project_name",
			Title:       "Project",
			Prompt:      "Project name",
			Kind:        tui.FieldText,
			Default:     wctx.nameDefault,
			Placeholder: "my-project",
			Required:    true,
			Validator:   validateProjectName,
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:         "preset",
			Title:      "Template",
			Prompt:     "Choose a starting template",
			Kind:       tui.FieldSelect,
			Options:    presetOptions,
			DefaultIdx: 0,
			SkipIf: func(vals tui.WizardValues) bool {
				return overwriteDeclined(vals)
			},
		},
		{
			ID:     "action",
			Title:  "Action",
			Prompt: "What would you like to do?",
			Kind:   tui.FieldSelect,
			Options: []tui.FieldOption{
				{Label: actionSave, Description: "Write config and start building"},
				{Label: actionCustomize, Description: "Walk through key config fields before saving"},
			},
			DefaultIdx: 0,
			SkipIf: func(vals tui.WizardValues) bool {
				if overwriteDeclined(vals) {
					return true
				}
				// AutoCustomize presets skip this — they always customize.
				preset, ok := presetByName(wctx.presets, vals["preset"])
				return ok && preset.AutoCustomize
			},
		},
	}
}

// customizeWizardFields returns the ordered list of store field paths shown
// in the customize wizard.
func customizeWizardFields() []string {
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

// customizeWizardOverrides returns overrides that customize field presentation
// in the store-backed wizard.
func customizeWizardOverrides() []storeui.Override {
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
