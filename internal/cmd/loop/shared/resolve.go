package shared

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/spf13/pflag"
)

// defaultTasksTemplate wraps the task file content in a structured prompt.
const defaultTasksTemplate = `Read the following task list. Pick the next open task, complete it, then mark it done.

<tasks>
%s
</tasks>

After completing the task, update the task file to mark it as done.`

// ResolvePrompt resolves the prompt from either an inline string or a file path.
// Exactly one of prompt or promptFile should be non-empty.
func ResolvePrompt(prompt, promptFile string) (string, error) {
	if prompt != "" {
		return prompt, nil
	}

	data, err := os.ReadFile(promptFile)
	if err != nil {
		return "", fmt.Errorf("reading prompt file: %w", err)
	}

	resolved := strings.TrimSpace(string(data))
	if resolved == "" {
		return "", fmt.Errorf("prompt file %q is empty", promptFile)
	}

	return resolved, nil
}

// ResolveTasksPrompt builds the prompt for a task-driven loop.
// It reads the tasks file and combines it with either:
//   - the default template (if no custom prompt/file given)
//   - a custom inline template (taskPrompt)
//   - a custom template file (taskPromptFile)
//
// Templates containing %s get the tasks substituted in; otherwise tasks are appended.
func ResolveTasksPrompt(tasksFile, taskPrompt, taskPromptFile string) (string, error) {
	data, err := os.ReadFile(tasksFile)
	if err != nil {
		return "", fmt.Errorf("reading tasks file: %w", err)
	}

	tasks := strings.TrimSpace(string(data))
	if tasks == "" {
		return "", fmt.Errorf("tasks file %q is empty", tasksFile)
	}

	// Determine template
	template := defaultTasksTemplate
	if taskPromptFile != "" {
		tplData, err := os.ReadFile(taskPromptFile)
		if err != nil {
			return "", fmt.Errorf("reading task prompt file: %w", err)
		}
		template = strings.TrimSpace(string(tplData))
	} else if taskPrompt != "" {
		template = taskPrompt
	}

	// Apply template — use strings.Replace (not fmt.Sprintf) to avoid
	// interpreting other format verbs in user-supplied templates.
	if strings.Contains(template, "%s") {
		return strings.Replace(template, "%s", tasks, 1), nil
	}

	// No placeholder — append tasks after template
	return template + "\n\n" + tasks, nil
}

// BuildRunnerOptions maps CLI LoopOptions + command context into loop.Options.
// Config overrides are applied for any flags the user did not explicitly set.
func BuildRunnerOptions(
	loopOpts *LoopOptions,
	project, agent, containerName, prompt, workDir string,
	flags *pflag.FlagSet,
	loopCfg *config.LoopConfig,
) loop.Options {
	opts := loop.Options{
		ContainerName:          containerName,
		Project:                project,
		Agent:                  agent,
		Prompt:                 prompt,
		WorkDir:                workDir,
		MaxLoops:               loopOpts.MaxLoops,
		StagnationThreshold:    loopOpts.StagnationThreshold,
		Timeout:                time.Duration(loopOpts.TimeoutMinutes) * time.Minute,
		LoopDelaySeconds:       loopOpts.LoopDelay,
		SameErrorThreshold:     loopOpts.SameErrorThreshold,
		OutputDeclineThreshold: loopOpts.OutputDeclineThreshold,
		MaxConsecutiveTestLoops: loopOpts.MaxConsecutiveTestLoops,
		SafetyCompletionThreshold: loopOpts.SafetyCompletionThreshold,
		CompletionThreshold:    loopOpts.CompletionThreshold,
		UseStrictCompletion:    loopOpts.StrictCompletion,
		SkipPermissions:        loopOpts.SkipPermissions,
		SystemPrompt:           loop.BuildSystemPrompt(loopOpts.AppendSystemPrompt),
		CallsPerHour:           loopOpts.CallsPerHour,
		ResetCircuit:           loopOpts.ResetCircuit,
		Verbose:                loopOpts.Verbose,
	}

	// Apply config overrides for flags the user didn't explicitly set
	if loopCfg != nil && flags != nil {
		applyConfigOverrides(&opts, loopOpts, flags, loopCfg)
	}

	return opts
}

// ApplyLoopConfigDefaults applies config.LoopConfig values to LoopOptions fields
// that are consumed before the runner is created (e.g., hooks_file, append_system_prompt).
// Call this after loading the config but before SetupLoopContainer and BuildRunnerOptions.
func ApplyLoopConfigDefaults(loopOpts *LoopOptions, flags *pflag.FlagSet, cfg *config.LoopConfig) {
	if cfg == nil || flags == nil {
		return
	}
	if !flags.Changed("hooks-file") && cfg.HooksFile != "" {
		loopOpts.HooksFile = cfg.HooksFile
	}
	if !flags.Changed("append-system-prompt") && cfg.AppendSystemPrompt != "" {
		loopOpts.AppendSystemPrompt = cfg.AppendSystemPrompt
	}
}

// applyConfigOverrides applies config.LoopConfig values for any flag not explicitly set by the user.
func applyConfigOverrides(opts *loop.Options, loopOpts *LoopOptions, flags *pflag.FlagSet, cfg *config.LoopConfig) {
	if !flags.Changed("max-loops") && cfg.MaxLoops > 0 {
		opts.MaxLoops = cfg.MaxLoops
	}
	if !flags.Changed("stagnation-threshold") && cfg.StagnationThreshold > 0 {
		opts.StagnationThreshold = cfg.StagnationThreshold
	}
	if !flags.Changed("timeout") && cfg.TimeoutMinutes > 0 {
		opts.Timeout = time.Duration(cfg.TimeoutMinutes) * time.Minute
	}
	if !flags.Changed("loop-delay") && cfg.LoopDelaySeconds > 0 {
		opts.LoopDelaySeconds = cfg.LoopDelaySeconds
	}
	if !flags.Changed("same-error-threshold") && cfg.SameErrorThreshold > 0 {
		opts.SameErrorThreshold = cfg.SameErrorThreshold
	}
	if !flags.Changed("output-decline-threshold") && cfg.OutputDeclineThreshold > 0 {
		opts.OutputDeclineThreshold = cfg.OutputDeclineThreshold
	}
	if !flags.Changed("max-test-loops") && cfg.MaxConsecutiveTestLoops > 0 {
		opts.MaxConsecutiveTestLoops = cfg.MaxConsecutiveTestLoops
	}
	if !flags.Changed("safety-completion-threshold") && cfg.SafetyCompletionThreshold > 0 {
		opts.SafetyCompletionThreshold = cfg.SafetyCompletionThreshold
	}
	if !flags.Changed("completion-threshold") && cfg.CompletionThreshold > 0 {
		opts.CompletionThreshold = cfg.CompletionThreshold
	}
	if !flags.Changed("calls-per-hour") && cfg.CallsPerHour > 0 {
		opts.CallsPerHour = cfg.CallsPerHour
	}
	// SessionExpirationHours is config-only (no CLI flag), so always apply.
	if cfg.SessionExpirationHours > 0 {
		opts.SessionExpirationHours = cfg.SessionExpirationHours
	}
	// Boolean: only override if flag wasn't set and config enables it
	if !flags.Changed("skip-permissions") && cfg.SkipPermissions {
		opts.SkipPermissions = true
	}
}
