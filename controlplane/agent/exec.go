package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	moby "github.com/moby/moby/client"
	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// Per-step timeout defaults. post-init can install packages and warm
// caches, hence 600s; other steps are file-IO and should complete in
// milliseconds in steady state, 30s tolerates a slow first-boot fs.
// CP's `runStep` ceiling is now the only step wall-clock gate —
// clawkerd-as-PID-1 has no separate shell-script timeout to align
// with (the legacy bash entrypoint + fifo wait was retired by the
// PID-1 cutover; see clawkerd/CLAUDE.md).
const (
	execStepTimeoutDefault  = consts.ExecStepTimeoutDefaultSeconds
	execStepTimeoutPostInit = consts.ExecStepTimeoutPostInitSeconds
)

// defaultKnownHosts is the openssh published host-key blob for the
// common public Git forges (github.com, gitlab.com, bitbucket.org),
// seeded into ~/.ssh/known_hosts on every init run via the ssh step's
// InitialStdin (the first pipe stage's stdin). Update if upstream rotates.
//
// Source: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints
//
//	https://docs.gitlab.com/ee/user/gitlab_com/#ssh-host-keys-fingerprints
//	https://support.atlassian.com/bitbucket-cloud/docs/configure-ssh-and-two-step-verification/
const defaultKnownHosts = `github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=
github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=
gitlab.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf
gitlab.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBFSMqzJeV9rUzU4kWitGjeR4PWSa29SPqJ1fVkhtj3Hw9xjLVXVYrU9QlYWrOLXBpQ6KWjbjTDTdDkoohFzgbEY=
gitlab.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCsj2bNKTBSpIYDEGk9KxsGh3mySTRgMtXL583qmBpzeQ+jqCMRgBqB98u3z++J1sKlXHWfM9dyhSevkMwSbhoR8XIq/U0tCNyokEi/ueaBMCvbcTHhO7FcwzY92WK4Ik8Y0iQ7F3awE8ntZELLwOvLYjzo3yl7hGRM9aLhHaVCF8bCG7cJTbplCCVSLKcQzQasPAOmPTmCC/NfZqrT0cIQ2rWM8Q1xI/z3THw1h19WSSqLBgNmz8M+Z7oqlABp7UMlP8W5K5RUCTASg9K7hNg7Jy3gmr3G6V+/FFHDB5PASg8q2g9ByCVWDqt1r8I5NxpqhUJ47RCrKE01zEIyc9z
bitbucket.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO
bitbucket.org ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBPIQmuzMBuKdWeF4+a2sjSSpBK0iqitSQ+5BM9KhpexuGt20JpTVM7u5BDZngncgrqDMbWdxMWWOGtZ9UgbqgZE=
bitbucket.org ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDQeJzhupRu0u0cdegZIa8e86EG2qOCsIsD1Xw0xSeiPDlCr7kq97NLmMbpKTX6Esc30NuoqEEHCuc7yWtwp8dI76EEEB1VqY9QJq6vk+aySyboD5QF61I/1WeTwu+deCbgKMGbUijeXhtfbxSxm6JwGrXrhBdofTsbKRUsrN1WoNgUa8uqN1Vx6WAJw1JHPhglEGGHea6QICwJOAr/6mrui/oB7pkaWKHj3z7d1IC4KWLtY47elvjbaTlkN04Kc/5LFEirorGYVbt15kAUlqGM65pk6ZBxtaO3+30LVlORZkxOh+LKL/BvbZ/iRNhItLqNyieoQj/uj/4PXhq0r2tVoBqXJCmLk7k+zpcaoprJBFQDa5A7SjqPQK0pCwBvhOT0hHpF0sWH4AIQHvYAWVTD0tBFPF1yENBxnVJpfL0L2qgGxLbQCWgOG0/1ygM+Gf9n0AIksE1h/uoLERBHQXE30XuP4pHV3n+7kO5+nw5VVFIsMfrQ3oT89Si/NvvmM=
`

// Inline self-gating shell scripts. Each script's gating predicates
// ([ -d INIT_DIR ], [ -f $HOST_GITCONFIG ], CLAWKER_GIT_HTTPS, etc.)
// keep the dispatch CP-feature-flag-free: CP doesn't need to know
// which optional features a given container has wired.
const (
	ConfigSeedScript = `INIT_DIR="$HOME/.claude-init"
CONFIG_DIR="$HOME/.claude"
[ -d "$INIT_DIR" ] || exit 0
mkdir -p "$CONFIG_DIR"
[ ! -f "$CONFIG_DIR/statusline.sh" ] && cp "$INIT_DIR/statusline.sh" "$CONFIG_DIR/statusline.sh"
if [ ! -f "$CONFIG_DIR/.config.json" ] || [ ! -s "$CONFIG_DIR/.config.json" ]; then
    cp "$INIT_DIR/.config.json" "$CONFIG_DIR/.config.json"
fi
if [ ! -f "$CONFIG_DIR/settings.json" ]; then
    cp "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json"
else
    if jq -s '.[0] * .[1]' "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json" > "$CONFIG_DIR/settings.json.tmp" 2>/dev/null; then
        mv "$CONFIG_DIR/settings.json.tmp" "$CONFIG_DIR/settings.json"
    else
        rm -f "$CONFIG_DIR/settings.json.tmp"
    fi
fi
`

	// GitconfigFilterTemplate strips [credential] sections from the
	// host-mounted gitconfig before placing it under the unprivileged
	// user's home. %q is replaced with consts.HostGitConfigStagingPath
	// (Go-quoted so the bash literal never drifts from the workspace
	// mount target).
	//
	// Three branches:
	//   - awk succeeds with non-empty output → move into place
	//   - awk succeeds with empty output (host gitconfig had ONLY
	//     [credential] blocks) → discard the empty tmp; copying the
	//     unfiltered file would leak credentials.
	//   - awk syscall failed → discard tmp and bail; same rationale.
	GitconfigFilterTemplate = `HOST_GITCONFIG=%q
[ -f "$HOST_GITCONFIG" ] || exit 0
TMP="$HOME/.gitconfig.tmp"
if awk '/^\[credential/ {in_cred=1; next} /^\[/ {in_cred=0} !in_cred {print}' "$HOST_GITCONFIG" > "$TMP" 2>/dev/null; then
    if [ -s "$TMP" ]; then
        mv "$TMP" "$HOME/.gitconfig"
    else
        rm -f "$TMP"
    fi
else
    rm -f "$TMP"
fi
`

	GitCredentialsScript = `[ -n "$` + consts.EnvHostProxy + `" ] || exit 0
[ "$` + consts.EnvGitHTTPS + `" = "true" ] || exit 0
git config --global credential.helper clawker
`

	// SshKnownHostsScript reads the host blob from stdin (PipeStage's
	// initial_stdin) and appends only lines not already present.
	// Idempotent across container restarts.
	SshKnownHostsScript = `mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
KH="$HOME/.ssh/known_hosts"
touch "$KH"
chmod 600 "$KH"
while IFS= read -r line; do
    [ -z "$line" ] && continue
    if ! grep -qF -- "$line" "$KH"; then
        printf '%s\n' "$line" >> "$KH"
    fi
done
`

	// PostInitScript runs the user's one-time post_init hook. Contract:
	// attempted at most once per container lifecycle. DONE-first short
	// circuits an already-attempted container; a missing script writes the
	// marker and exits (nothing to do); a present script runs once. On
	// success the marker is written so a restart never re-runs it; on
	// failure the marker is NOT written and the exit code propagates — the
	// Step is fatal (plan halts, agent-ready never sent). A failed container
	// is torn down rather than restarted: the Step carries exit_on_non_zero
	// so clawkerd self-exits with the mirrored code, and CP enforces a
	// grace-then-SIGKILL backstop (killAfterGrace). A manually-restarted
	// container re-runs this hook (no marker was written).
	//
	// The `[ -x … ] || { …; }` brace group is load-bearing: `|| touch && exit`
	// without braces binds as `([ -x ] || touch) && exit`, which exits 0 when
	// the script EXISTS and never runs it. Do not remove the braces.
	PostInitScript = `POST="$HOME/` + consts.DotClawkerDir + `/` + consts.HookPostInit + `.sh"
DONE="$HOME/.claude/post-initialized"
[ -f "$DONE" ] && exit 0
[ -x "$POST" ] || { touch "$DONE"; exit 0; }
if "$POST"; then
    touch "$DONE"
else
    exit $?
fi
`

	// PreRunScript runs the every-start pre_run hook. No marker (runs every
	// start) and no log/ready-file. The `[ -x … ] || exit 0` guard is a
	// defensive regression net: with always-deliver the file is present, but
	// if it ever goes missing the Step no-ops instead of failing the plan.
	// `[ -x … ] && …` would exit 1 when absent (fail the Step); `&& … || true`
	// would swallow a real failure (break the fatal contract) — this two-line
	// form no-ops when absent AND propagates the exit code when present. The
	// file carries #!/bin/bash + set -e from PrepareHookTar.
	PreRunScript = `[ -x "$HOME/` + consts.DotClawkerDir + `/` + consts.HookPreRun + `.sh" ] || exit 0
"$HOME/` + consts.DotClawkerDir + `/` + consts.HookPreRun + `.sh"
`
)

// gitconfigFilterScript returns the rendered git-step body; the %q slot
// carries the workspace const.
func gitconfigFilterScript() string {
	return fmt.Sprintf(GitconfigFilterTemplate, consts.HostGitConfigStagingPath)
}

// Step is one entry in an Executor plan (init or boot). Sealed sum:
// ShellStep, AgentReadyStep, or AgentInitializedStep. Adding a new Step kind is a compile-time change
// (implement Step) — runStep's type switch loses its runtime
// "unknown Step kind" branch entirely.
type Step interface {
	StepName() string
	// Command builds the wire payload for this Step under commandID.
	// followCloseStdin reports whether runStep should follow with a
	// CloseStdin frame (true for shell steps that don't consume
	// stdin; false for AgentReady which has no stdin pipe).
	Command(commandID string) (cmd *clawkerdv1.Command, followCloseStdin bool)
	// IsStep is the unexported sealing marker. A third implementer
	// outside this package is rejected at compile time; package-
	// internal additions still need a paired runStep / plan() update
	// by convention.
	IsStep()
}

type ShellStep struct {
	Name  string
	Shell *clawkerdv1.ShellCommand
}

func (s ShellStep) StepName() string { return s.Name }
func (ShellStep) IsStep()            {}
func (s ShellStep) Command(id string) (*clawkerdv1.Command, bool) {
	return &clawkerdv1.Command{
		CommandId: id,
		Payload:   &clawkerdv1.Command_Shell{Shell: s.Shell},
	}, true
}

type AgentReadyStep struct {
	Name string
}

type AgentInitializedStep struct {
	Name string
}

func (s AgentReadyStep) StepName() string { return s.Name }
func (AgentReadyStep) IsStep()            {}
func (s AgentReadyStep) Command(id string) (*clawkerdv1.Command, bool) {
	return &clawkerdv1.Command{
		CommandId: id,
		Payload:   &clawkerdv1.Command_AgentReady{AgentReady: &clawkerdv1.AgentReady{}},
	}, false
}

func (s AgentInitializedStep) StepName() string { return s.Name }
func (AgentInitializedStep) IsStep()            {}
func (s AgentInitializedStep) Command(id string) (*clawkerdv1.Command, bool) {
	return &clawkerdv1.Command{
		CommandId: id,
		Payload:   &clawkerdv1.Command_AgentInitialized{AgentInitialized: &clawkerdv1.AgentInitialized{}},
	}, false
}

// ExecTarget identifies the agent the Executor is executing against.
// Threaded through every event so subscribers see consistent
// identity fields without re-deriving from the registry.
type ExecTarget struct {
	ContainerID string
	AgentName   string
	Project     string
}

// agent projects the ExecTarget onto the AgentEvent identity triple so
// every exec-axis event carries consistent (container, agent, project)
// fields.
func (t ExecTarget) agent() Agent {
	return Agent{
		ContainerID: t.ContainerID,
		AgentName:   t.AgentName,
		Project:     t.Project,
	}
}

// Executor dispatches a static CP-driven plan (init or boot) against an open
// Session stream. Owns Recv during Run; the dialer's drainStream
// takes over after Run returns.
//
// Plan idempotency contract: every Session establish runs the full
// plan, including reconnects after CP restart. Each step is
// idempotent (file-presence gates, append-if-missing, marker-file
// post-init), and AgentReady is no-op success when clawkerd already
// spawned the user CMD (spawnState's CAS rejects re-fork; handler
// replies Done{0}). Trade: a small volume of shell commands
// fires on every reconnect; gain: no per-container completed flag.
// Executor is shared across all containers — the dialer constructs
// one at CP boot and calls Run from a goroutine per DialAgent (one per
// agent container). Run holds no Executor-scoped mutable state: every
// call gets its own (ctx, stream, target) and drives its own stream's
// Recv loop in a single goroutine. The Run-owns-Recv invariant is
// per-stream (one Run, one stream, one Recv-driving goroutine), not
// per-Executor — concurrent Runs across different containers must not
// be serialized.
type Executor struct {
	topic     *pubsub.Topic[AgentEvent]
	dockerCli *docker.Client
	log       *logger.Logger
}

// NewExecutor constructs an Executor. nil log is replaced with
// logger.Nop(). topic is required — Run publishes exec events
// unconditionally, so a nil topic is a wiring bug the caller must catch
// at construction. Returning an error lets the caller
// (the orchestrator) log the wiring bug to the structured log surface
// and degrade gracefully (executor = nil → dialer logs
// agent_<plan>_executor_unset per dial) instead of crashing CP and
// stranding the failure on os.Stderr where only `docker logs` sees it.
// Matches the nil-topic contract on agent.NewDialer for the dialer.
func NewExecutor(topic *pubsub.Topic[AgentEvent], dockerCli *docker.Client, log *logger.Logger) (*Executor, error) {
	if topic == nil {
		return nil, errors.New("agent.NewExecutor: topic is required")
	}
	if log == nil {
		log = logger.Nop()
	}
	return &Executor{topic: topic, dockerCli: dockerCli, log: log}, nil
}

func (e *Executor) Run(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, target ExecTarget, plan []Step, label string) (runErr error) {
	startedAt := time.Now()
	Publish(e.topic, newAgentEvent(target.agent(), Message{
		Type:      ExecutorEventType,
		Action:    ActionExecStarted,
		StepCount: len(plan),
	}))
	log := e.log.With(
		"component", fmt.Sprintf("agent.%s", label),
		"container_id", target.ContainerID,
		"agent", target.AgentName,
		"project", target.Project,
	)
	log.Info().
		Str("event", fmt.Sprintf("agent_%s_started", label)).
		Int("step_count", len(plan)).
		Msg(fmt.Sprintf("agent.%s: dispatching plan", label))

	// currentIdx / currentName are written at the head of each step
	// iteration so the panic recover below can publish a synthetic
	// ExecStepFailed for the in-flight step. -1 means the panic
	// happened before any step started (plan setup, log composition,
	// etc.) — only ExecFailed is synthesized in that case.
	currentIdx, currentName := -1, ""
	defer func() {
		if r := recover(); r != nil {
			runErr = e.handleRunPanic(r, target, label, log, startedAt, currentIdx, currentName)
		}
	}()

	for i, st := range plan {
		currentIdx = i
		currentName = st.StepName()
		stepStart := time.Now()
		e.announceStepStarted(target, label, log, i, len(plan), st)

		out, err := e.runStep(ctx, stream, target.ContainerID, label, i, st, log)
		dur := time.Since(stepStart)
		if out.Failed() {
			return e.reportStepFailure(ctx, target, label, log, startedAt, dur, i, st, out, err)
		}
		e.announceStepCompleted(target, label, log, i, dur, st, out)
		// Reset between steps: a panic here (between iterations,
		// e.g. during defer scheduling) must not be mis-attributed
		// to the just-completed step. The recover gates synthetic
		// ExecStepFailed on currentIdx >= 0; -1 means "between
		// steps — publish only ExecFailed".
		currentIdx, currentName = -1, ""
	}

	totalDur := time.Since(startedAt)
	Publish(e.topic, newAgentEvent(target.agent(), Message{
		Type:     ExecutorEventType,
		Action:   ActionExecCompleted,
		Duration: totalDur,
	}))
	log.Info().
		Str("event", fmt.Sprintf("agent_%s_completed", label)).
		Dur("duration", totalDur).
		Msg(fmt.Sprintf("agent.%s: plan completed", label))
	return nil
}

// announceStepStarted publishes the ExecStepStarted event and logs the start
// of step i.
func (e *Executor) announceStepStarted(target ExecTarget, label string, log *logger.Logger, i, stepCount int, st Step) {
	Publish(e.topic, newAgentEvent(target.agent(), Message{
		Type:      ExecutorEventType,
		Action:    ActionExecStepStarted,
		StepName:  st.StepName(),
		StepIndex: i,
		StepCount: stepCount,
	}))
	log.Info().
		Str("event", fmt.Sprintf("agent_%s_step_started", label)).
		Str("step", st.StepName()).
		Int("step_index", i).
		Msg(fmt.Sprintf("agent.%s: step started", label))
}

// announceStepCompleted publishes the ExecStepCompleted event and logs the
// successful completion of step i.
func (e *Executor) announceStepCompleted(target ExecTarget, label string, log *logger.Logger, i int, dur time.Duration, st Step, out stepOutcome) {
	Publish(e.topic, newAgentEvent(target.agent(), Message{
		Type:      ExecutorEventType,
		Action:    ActionExecStepCompleted,
		StepName:  st.StepName(),
		StepIndex: i,
		Duration:  dur,
		ExitCode:  out.ExitCode,
	}))
	log.Info().
		Str("event", fmt.Sprintf("agent_%s_step_completed", label)).
		Str("step", st.StepName()).
		Int("step_index", i).
		Dur("duration", dur).
		Msg(fmt.Sprintf("agent.%s: step completed", label))
}

// recoverRun is Run's deferred panic handler. On a recovered panic it
// synthesizes the terminal ExecStepFailed (when a step was in flight) +
// ExecFailed events and converts the panic into runErr so dialer.runExec hits
// the existing error path (the Session is held open per asymmetric trust).
// Re-panicking would land in the dial goroutine's outer recover and strand
// the Exec axis at Running because that recover doesn't know step state. The
// returned error is what Run's deferred recover assigns to its named return so
// dialer.runExec hits the existing error path (the Session is held open per
// asymmetric trust).
func (e *Executor) handleRunPanic(r any, target ExecTarget, label string, log *logger.Logger, startedAt time.Time, currentIdx int, currentName string) error {
	now := time.Now()
	dur := now.Sub(startedAt)
	detail := fmt.Sprintf("Executor.Run panicked: %v", r)
	log.Error().
		Interface("panic", r).
		Bytes("stack", debug.Stack()).
		Str("event", fmt.Sprintf("agent_%s_panic", label)).
		Int("step_index", currentIdx).
		Str("step", currentName).
		Msg(fmt.Sprintf("agent.%s: Executor.Run panicked; publishing synthetic terminal events; Session held open for containment", label))
	if currentIdx >= 0 {
		Publish(e.topic, newAgentEvent(target.agent(), Message{
			Type:      ExecutorEventType,
			Action:    ActionExecStepFailed,
			StepName:  currentName,
			StepIndex: currentIdx,
			Duration:  dur,
			ExitCode:  -1,
			Reason:    ReasonUnknown,
			Detail:    detail,
		}))
	}
	Publish(e.topic, newAgentEvent(target.agent(), Message{
		Type:     ExecutorEventType,
		Action:   ActionExecFailed,
		StepName: currentName,
		Reason:   ReasonUnknown,
		Detail:   detail,
		Duration: dur,
	}))
	return errors.New(detail)
}

// reportStepFailure publishes the terminal ExecStepFailed + ExecFailed events
// for a failed step, logs the halt, and returns the error Run bubbles up. On a
// transport break (err != nil) the Session is already gone and the dial loop's
// teardown handles the container, so no kill is raced here. On a command-level
// failure it enforces container teardown via KillAfterGrace before returning.
func (e *Executor) reportStepFailure(ctx context.Context, target ExecTarget, label string, log *logger.Logger, startedAt time.Time, dur time.Duration, i int, st Step, out stepOutcome, err error) error {
	Publish(e.topic, newAgentEvent(target.agent(), Message{
		Type:      ExecutorEventType,
		Action:    ActionExecStepFailed,
		StepName:  st.StepName(),
		StepIndex: i,
		Duration:  dur,
		ExitCode:  out.ExitCode,
		Reason:    out.Reason,
		Detail:    out.Detail,
	}))
	Publish(e.topic, newAgentEvent(target.agent(), Message{
		Type:     ExecutorEventType,
		Action:   ActionExecFailed,
		StepName: st.StepName(),
		Reason:   out.Reason,
		Detail:   out.Detail,
		Duration: time.Since(startedAt),
	}))
	log.Error().
		Str("event", fmt.Sprintf("agent_%s_failed", label)).
		Str("step", st.StepName()).
		Int("step_index", i).
		Int32("exit_code", out.ExitCode).
		Str("reason", string(out.Reason)).
		Str("detail", out.Detail).
		Msg(fmt.Sprintf("agent.%s: plan halted on step failure", label))
	if err != nil {
		// Transport-level failure (the stream broke): the Session
		// is already gone and the dial loop's teardown handles the
		// container. Don't race a kill here — a transient blip
		// re-establishes and re-runs the plan (idempotency
		// contract).
		return err
	}
	// Command-level failure (non-zero exit / classified Error).
	// The command definitively failed; enforce container teardown
	// with the grace-then-SIGKILL backstop. Steps carry
	// exit_on_non_zero so a healthy clawkerd self-exits within the
	// grace; the SIGKILL only catches a wedged one.
	if killErr := e.KillAfterGrace(ctx, target.ContainerID, log); killErr != nil {
		return fmt.Errorf("agent.%s: step %q failed: %s; additionally, failed to kill container: %w", label, st.StepName(), out.Detail, killErr)
	}
	return fmt.Errorf("agent.%s: step %q failed: %s", label, st.StepName(), out.Detail)
}

// maxOutputCapture caps how much of a command's combined output Run
// folds into the failed-step Detail — a bounded log line, not the full
// stream (which the caller receives in its entirety as OutputChunks).
// Truncation is marked explicitly so operators see the boundary instead
// of guessing.
const maxOutputCapture = 4096

// captureCapped appends data to buf, bounding the total at
// maxOutputCapture and folding any overflow into *truncated. Shared by
// runStep's combined-stdout and intermediate-stage-stderr capture.
func captureCapped(buf *strings.Builder, truncated *int, data []byte) {
	remaining := maxOutputCapture - buf.Len()
	if remaining <= 0 {
		*truncated += len(data)
		return
	}
	if len(data) > remaining {
		*truncated += len(data) - remaining
		data = data[:remaining]
	}
	buf.Write(data)
}

// stepOutcome bundles the per-step result fields runStep produces.
// Zero value means the Step succeeded; populated values are produced
// only via the constructors below, which keep Reason / ExitCode /
// Detail coherent. Run reads outcome.Failed() to decide whether to
// Publish terminal events.
type stepOutcome struct {
	ExitCode int32
	Reason   Reason
	Detail   string
}

func (o stepOutcome) Failed() bool {
	return o.Reason != ReasonNone
}

// stepSucceeded is the zero outcome — the only success shape.
func stepSucceeded() stepOutcome { return stepOutcome{} }

// stepFailedTransport classifies any transport break (Send error,
// Recv error, ctx cancel, premature EOF). The paired transport error
// returned alongside drives Run's dispatch-halt branch; the outcome
// carries the human-readable detail for the ExecStepFailed event.
func stepFailedTransport(detail string) stepOutcome {
	return stepOutcome{
		ExitCode: -1,
		Reason:   ReasonTransportError,
		Detail:   detail,
	}
}

// stepFailedExit classifies a clawkerd Done with a non-zero exit
// code. The command's combined output (truncated to maxOutputCapture)
// is folded into detail upstream of this constructor.
func stepFailedExit(exit int32, detail string) stepOutcome {
	return stepOutcome{
		ExitCode: exit,
		Reason:   ReasonExitCode,
		Detail:   detail,
	}
}

// stepFailedClassified classifies a clawkerd Response_Error frame
// (timeout, spawn failed, IO error, protocol violation, ...) into
// the typed reason vocabulary subscribers branch on.
func stepFailedClassified(reason Reason, detail string) stepOutcome {
	return stepOutcome{
		ExitCode: -1,
		Reason:   reason,
		Detail:   detail,
	}
}

// KillAfterGrace ensures the agent container is torn down after a fatal
// command. A command carrying exit_on_non_zero makes a healthy clawkerd
// echo the output and self-exit PID 1 with the mirrored code, so CP waits
// consts.CPAgentKillGrace for that clean self-exit — keyed on real
// container liveness via ContainerWait — and escalates to SIGKILL only if
// the container is still running past the grace (a wedged clawkerd, or a
// timeout where clawkerd never self-exits). Generic to the CP→clawkerd
// command service; returns an error only when the SIGKILL itself fails, so
// the caller can report that the doomed container could not be torn down.
//
// ctx is the CP-lifetime context (NOT the dial ctx — that one is cancelled
// by the very container/die this awaits). The wait rides ctx, so a CP
// shutdown during the grace abandons the wait and proceeds straight to the
// kill: a doomed container must not outlive CP. The kill itself runs on
// context.Background(), because if ctx is what woke the wait (shutdown),
// the moby client rejects a request on a cancelled ctx before it reaches
// the daemon — the SIGKILL would never issue and the container would leak.
func (e *Executor) KillAfterGrace(ctx context.Context, containerID string, log *logger.Logger) error {
	waitCtx, cancel := context.WithTimeout(ctx, consts.CPAgentKillGrace)
	defer cancel()
	wait := e.dockerCli.APIClient.ContainerWait(waitCtx, containerID, moby.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case <-wait.Result:
		log.Info().
			Str("event", "agent_cmd_container_self_exit").
			Str("container_id", containerID).
			Msg("agent: container self-exited after fatal command; no kill needed")
		return nil
	case <-wait.Error:
	case <-waitCtx.Done():
	}
	log.Warn().
		Str("event", "agent_cmd_self_exit_grace_elapsed").
		Str("container_id", containerID).
		Msg("agent: container did not self-exit in time; escalating to SIGKILL")
	if _, err := e.dockerCli.APIClient.ContainerKill(context.Background(), containerID, moby.ContainerKillOptions{Signal: "SIGKILL"}); err != nil {
		log.Error().Err(err).
			Str("event", "agent_cmd_kill_failed").
			Str("container_id", containerID).
			Msg("agent: SIGKILL after fatal command failed; manual cleanup may be required")
		return fmt.Errorf("agent: SIGKILL after fatal command failed: %w", err)
	}
	return nil
}

// runStep dispatches one Step's wire payload and waits for its Done
// or Error. Returns:
//   - outcome: zero value on success, populated with Reason/Detail/
//     ExitCode on Step failure or transport break. Run consumes this
//     to publish the terminal ExecStepFailed + ExecFailed events.
//   - transport error: non-nil iff the stream is broken; the caller
//     should bail Run after publishing terminal events. A non-nil
//     err always pairs with outcome.Failed() == true (Reason ==
//     ExecFailureReasonTransportError) so Run branches on a single
//     check.
//
// Bounding wait time: clawkerd enforces the per-stage timeout server-
// side (ShellCommand.TimeoutSeconds → time.AfterFunc → SIGKILL +
// ERROR_CODE_TIMEOUT response). gRPC keepalive (consts.Clawkerd*)
// breaks a wedged transport. CP-side wall-clock deadlines are
// deliberately omitted — a duplicate budget here would race the
// server-side timer and risk misclassifying a server-detected timeout
// as a client-side break.
func (e *Executor) runStep(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, containerID, label string, idx int, st Step, log *logger.Logger) (stepOutcome, error) {
	commandID := buildCommandID(label, containerID, st.StepName(), idx)
	if outcome, err := sendStepCommand(stream, commandID, st); err != nil {
		return outcome, err
	}
	return awaitStepResult(ctx, stream, commandID, st, label, log)
}

// sendStepCommand dispatches a Step's wire payload and (when the Step requests
// it) the trailing CloseStdin frame. On a send error it returns a transport
// outcome paired with the wrapped error; otherwise the zero outcome and nil.
func sendStepCommand(stream clawkerdv1.ClawkerdService_SessionClient, commandID string, st Step) (stepOutcome, error) {
	cmd, followCloseStdin := st.Command(commandID)
	if err := stream.Send(cmd); err != nil {
		wrapped := fmt.Errorf("send %s: %w", st.StepName(), err)
		return stepFailedTransport(wrapped.Error()), wrapped
	}

	// CloseStdin invariant: clawkerd's runShellCommand publishes
	// stdinW under the registry lock at startShellCommand entry, so
	// the Send below cannot race the worker goroutine's pipe
	// registration. Without this Close, every shell step that doesn't
	// consume stdin would hang in exec.Cmd.Wait's awaitGoroutines
	// until the entrypoint timeout fires.
	if followCloseStdin {
		closeCmd := &clawkerdv1.Command{
			CommandId: commandID,
			Payload:   &clawkerdv1.Command_CloseStdin{CloseStdin: &clawkerdv1.CloseStdin{}},
		}
		if err := stream.Send(closeCmd); err != nil {
			wrapped := fmt.Errorf("send %s close_stdin: %w", st.StepName(), err)
			return stepFailedTransport(wrapped.Error()), wrapped
		}
	}
	return stepOutcome{}, nil
}

// awaitStepResult reads the Session stream until the terminal Done/Error frame
// for commandID, folding in output frames and discarding frames addressed to
// other commands. It returns the step outcome and a non-nil transport error
// only when the stream breaks (ctx cancel, EOF, recv error).
func awaitStepResult(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, commandID string, st Step, label string, log *logger.Logger) (stepOutcome, error) {
	var outputBuf strings.Builder
	outputTruncated := 0

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stepFailedTransport(ctxErr.Error()), ctxErr
		}
		resp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				const eofDetail = "stream EOF before terminal response"
				return stepFailedTransport(eofDetail), errors.New(eofDetail)
			}
			wrapped := fmt.Errorf("recv %s: %w", st.StepName(), err)
			return stepFailedTransport(wrapped.Error()), wrapped
		}
		if resp.GetCommandId() != commandID {
			log.Debug().
				Str("event", fmt.Sprintf("agent_%s_unexpected_command_id", label)).
				Str("got", resp.GetCommandId()).
				Str("expected", commandID).
				Str("payload_type", fmt.Sprintf("%T", resp.GetPayload())).
				Msg(fmt.Sprintf("agent.%s: ignoring response with non-matching command_id", label))
			continue
		}
		if outcome, terminal := classifyStepResponse(resp, st, label, &outputBuf, &outputTruncated, log); terminal {
			return outcome, nil
		}
	}
}

// classifyStepResponse folds one matched-command_id response frame into the
// step outcome. terminal is true iff resp is a Done/Error frame that ends the
// step (outcome carries the result); false for lifecycle/output frames that
// runStep keeps looping on. Output frames append to outputBuf (capped) so the
// failure detail can carry the tail of combined output.
func classifyStepResponse(resp *clawkerdv1.Response, st Step, label string, outputBuf *strings.Builder, outputTruncated *int, log *logger.Logger) (stepOutcome, bool) {
	switch p := resp.GetPayload().(type) {
	case *clawkerdv1.Response_Started, *clawkerdv1.Response_StageExit:
		// Lifecycle frames — not part of the failure detail; await
		// the terminal Done/Error.
		return stepOutcome{}, false
	case *clawkerdv1.Response_Output:
		// The command's combined output (the final stage's stdout and
		// every stage's stderr, merged in write order). Capture it
		// (capped) for the failure detail; the full stream reaches the
		// caller regardless of this cap.
		if p.Output != nil {
			captureCapped(outputBuf, outputTruncated, p.Output.GetData())
		}
		return stepOutcome{}, false
	case *clawkerdv1.Response_Done:
		exit := p.Done.GetFinalExitCode()
		if exit == 0 {
			return stepSucceeded(), true
		}
		detail := fmt.Sprintf("exit_code=%d", exit)
		if s := strings.TrimSpace(outputBuf.String()); s != "" {
			detail += "; output: " + s
			if *outputTruncated > 0 {
				detail += fmt.Sprintf(" ... [%d bytes truncated]", *outputTruncated)
			}
		}
		return stepFailedExit(exit, detail), true
	case *clawkerdv1.Response_Error:
		return stepFailedClassified(
			classifyErrorCode(p.Error.GetCode()),
			fmt.Sprintf("%s: %s", p.Error.GetCode().String(), p.Error.GetMessage()),
		), true
	default:
		// Warn-level: an unknown payload variant means the
		// clawkerd-CP wire vocabulary has drifted. Production
		// Debug logs are typically off — operators would otherwise
		// see only the eventual server-side timeout with no hint
		// that a new payload variant slipped past the switch.
		log.Warn().
			Str("event", fmt.Sprintf("agent_%s_unknown_payload", label)).
			Str("command_id", resp.GetCommandId()).
			Str("step", st.StepName()).
			Str("payload_type", fmt.Sprintf("%T", resp.GetPayload())).
			Msg(fmt.Sprintf("agent.%s: ignoring unknown response payload — wire vocabulary drift", label))
		return stepOutcome{}, false
	}
}

// classifyErrorCode maps a clawkerd ErrorCode to the typed step
// failure classification. New codes default to Unknown so producers
// don't drop information silently — the human-readable detail still
// carries the ErrorCode string.
func classifyErrorCode(code clawkerdv1.ErrorCode) Reason {
	switch code {
	case clawkerdv1.ErrorCode_ERROR_CODE_TIMEOUT:
		return ReasonTimeout
	case clawkerdv1.ErrorCode_ERROR_CODE_SPAWN_FAILED:
		return ReasonSpawnFailed
	case clawkerdv1.ErrorCode_ERROR_CODE_IO_ERROR, clawkerdv1.ErrorCode_ERROR_CODE_NOT_FOUND:
		return ReasonIOError
	case clawkerdv1.ErrorCode_ERROR_CODE_INVALID_REQUEST,
		clawkerdv1.ErrorCode_ERROR_CODE_UNKNOWN_COMMAND_ID:
		return ReasonProtocolError
	default:
		return ReasonUnknown
	}
}

// buildCommandID composes a stable, human-debuggable command_id for
// one Step dispatch. Prefix is bounded so log lines stay compact.
func buildCommandID(label, containerID, stepName string, idx int) string {
	prefix := containerID
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return fmt.Sprintf("%s-%s-%s-%d", label, prefix, stepName, idx)
}

// userStage returns a fresh PipeStage running `sh -c <script>` as the
// unprivileged container user.
func userStage(script string) *clawkerdv1.PipeStage {
	return &clawkerdv1.PipeStage{
		Argv: []string{"sh", "-c", script},
		Uid:  consts.HostUID(),
		Gid:  consts.HostGID(),
		Env: map[string]string{
			"HOME": consts.ContainerHomeDir,
			"USER": consts.ContainerUser,
		},
	}
}
