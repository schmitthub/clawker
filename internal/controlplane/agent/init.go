package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// Init step timeout defaults. Per-step shells are file-IO + small
// process spawns and complete in milliseconds in the steady state;
// 30s tolerates a slow filesystem on first boot. post-init runs
// user-supplied bash that may install packages or warm caches —
// 10 minutes is the same wall-clock ceiling the prior entrypoint
// would have honored implicitly via container's overall start
// budget.
const (
	initStepTimeoutDefault  uint32 = 30
	initStepTimeoutPostInit uint32 = 600
)

// Default known_hosts blob seeded into ~/.ssh/known_hosts on every
// init run. Sourced from openssh's published host keys for the most
// common public Git forges. Idempotent: the ssh step appends only
// lines not already present, so re-running does not duplicate.
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

// Inline self-gating shell scripts for the init plan. Each script's
// effect is a strict subset of what the prior entrypoint.sh did
// inline; the gating predicates ([ -d INIT_DIR ], [ -f /tmp/host-gitconfig ],
// CLAWKER_GIT_HTTPS, etc.) are preserved here so CP doesn't have to
// know which container has which optional features wired.
const (
	configSeedScript = `INIT_DIR="$HOME/.claude-init"
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

	// gitconfigFilterScript strips [credential] sections from the host
	// gitconfig before placing it under the unprivileged user's home.
	//
	// Three branches:
	//   - awk succeeds with non-empty output → move into place
	//   - awk succeeds with EMPTY output (host gitconfig contained
	//     ONLY [credential] blocks) → discard the empty tmp and leave
	//     ~/.gitconfig untouched. Copying the unfiltered host file
	//     here would leak credentials into the container — exactly
	//     what the filter exists to prevent.
	//   - awk syscall failed (extremely rare exec failure) → discard
	//     the partial tmp and bail. We do NOT fall back to copying
	//     the unfiltered file; same credential-leak rationale.
	gitconfigFilterScript = `HOST_GITCONFIG="/tmp/host-gitconfig"
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

	gitCredentialsScript = `[ -n "$CLAWKER_HOST_PROXY" ] || exit 0
[ "$CLAWKER_GIT_HTTPS" = "true" ] || exit 0
git config --global credential.helper clawker
`

	// sshKnownHostsScript reads the host blob from stdin (delivered by
	// PipeStage.initial_stdin) and appends only lines not already
	// present in known_hosts. Idempotent across container restarts —
	// fixes the dup-on-restart bug the prior entrypoint had.
	sshKnownHostsScript = `mkdir -p "$HOME/.ssh"
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

	postInitScript = `POST="$HOME/.clawker/post-init.sh"
DONE="$HOME/.claude/post-initialized"
[ -x "$POST" ] || exit 0
[ -f "$DONE" ] && exit 0
if "$POST"; then
    touch "$DONE"
else
    exit $?
fi
`
)

// stepKind classifies a step's wire payload. Adding a new kind
// requires updating runStep's switch.
type stepKind int

const (
	stepKindShell stepKind = iota
	stepKindAgentReady
)

// step is one entry in the init plan. Shell is non-nil iff
// Kind == stepKindShell. Per-step timeout for shell steps lives on
// Shell.TimeoutSeconds (the wire field clawkerd already enforces);
// non-shell steps (agent-ready) inherit the package-level
// initStepTimeoutDefault since they don't carry a Shell payload.
type step struct {
	Name  string
	Kind  stepKind
	Shell *clawkerdv1.ShellCommand
}

// InitTarget identifies the agent the Executor is initializing.
// Threaded through every init event so subscribers see consistent
// identity fields without re-deriving from the registry.
type InitTarget struct {
	ContainerID string
	AgentName   string
	Project     string
}

// Executor dispatches the static CP-driven init plan against an open
// Session stream. Owns Recv during Run; the dialer's drainStream
// takes over after Run returns.
//
// V1 design choice: every Session establish runs the full plan,
// including reconnects after CP restart. Each step is idempotent
// (file-presence gates, append-if-missing, marker-file post-init),
// and AgentReady is no-op success when the entrypoint has already
// released. Trade: a small volume of shell commands fires on every
// reconnect; gain: no per-container init-completed flag to track.
type Executor struct {
	bus *overseer.Overseer
	log *logger.Logger
}

// NewExecutor constructs an Executor. The unprivileged container user
// the user-space init steps drop to (uid/gid + username + home) is
// fixed by the bundler and lives in consts: ContainerUID,
// ContainerGID, ContainerUser. The root-only docker-socket step uses
// uid=0 regardless. The ssh step's known_hosts payload is the package-
// level defaultKnownHosts blob (forge host keys for github / gitlab /
// bitbucket).
func NewExecutor(bus *overseer.Overseer, log *logger.Logger) *Executor {
	return &Executor{bus: bus, log: log}
}

// Run dispatches the init plan one step at a time, awaiting Done or
// Error per command before sending the next. Publishes init events
// throughout. Returns the error that halted the run (transport
// failure, step failure surfaced as a Go error), or nil on full
// success.
//
// Caller invariant: stream must already have completed Hello/HelloAck
// and any Register handshake. Run owns stream.Recv until it returns;
// drainStream must not be running concurrently.
func (e *Executor) Run(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, target InitTarget) error {
	plan := e.plan()
	startedAt := time.Now()
	overseer.Publish(e.bus, InitStarted{
		ContainerID: target.ContainerID,
		AgentName:   target.AgentName,
		Project:     target.Project,
		StepCount:   len(plan),
		At:          startedAt,
	})
	log := e.log.With(
		"component", "agent.init",
		"container_id", target.ContainerID,
		"agent", target.AgentName,
		"project", target.Project,
	)
	log.Info().
		Str("event", "agent_init_started").
		Int("step_count", len(plan)).
		Msg("agent.init: dispatching plan")

	for i, st := range plan {
		stepStart := time.Now()
		overseer.Publish(e.bus, InitStepStarted{
			ContainerID: target.ContainerID,
			AgentName:   target.AgentName,
			Project:     target.Project,
			StepName:    st.Name,
			StepIndex:   i,
			StepCount:   len(plan),
			At:          stepStart,
		})
		log.Info().
			Str("event", "agent_init_step_started").
			Str("step", st.Name).
			Int("step_index", i).
			Msg("agent.init: step started")

		exit, reason, err := e.runStep(ctx, stream, target.ContainerID, i, st, log)
		dur := time.Since(stepStart)
		failed := err != nil || exit != 0 || reason != ""
		if failed {
			if reason == "" {
				if err != nil {
					reason = err.Error()
				} else {
					reason = fmt.Sprintf("exit_code=%d", exit)
				}
			}
			overseer.Publish(e.bus, InitStepFailed{
				ContainerID: target.ContainerID,
				AgentName:   target.AgentName,
				Project:     target.Project,
				StepName:    st.Name,
				StepIndex:   i,
				Duration:    dur,
				ExitCode:    exit,
				Reason:      reason,
				At:          time.Now(),
			})
			overseer.Publish(e.bus, InitFailed{
				ContainerID: target.ContainerID,
				AgentName:   target.AgentName,
				Project:     target.Project,
				FailedStep:  st.Name,
				Reason:      reason,
				Duration:    time.Since(startedAt),
				At:          time.Now(),
			})
			log.Error().
				Str("event", "agent_init_failed").
				Str("step", st.Name).
				Int("step_index", i).
				Int32("exit_code", exit).
				Str("reason", reason).
				Msg("agent.init: plan halted on step failure")
			if err != nil {
				return err
			}
			return fmt.Errorf("agent.init: step %q failed: %s", st.Name, reason)
		}
		overseer.Publish(e.bus, InitStepCompleted{
			ContainerID: target.ContainerID,
			AgentName:   target.AgentName,
			Project:     target.Project,
			StepName:    st.Name,
			StepIndex:   i,
			Duration:    dur,
			ExitCode:    exit,
			At:          time.Now(),
		})
		log.Info().
			Str("event", "agent_init_step_completed").
			Str("step", st.Name).
			Int("step_index", i).
			Dur("duration", dur).
			Msg("agent.init: step completed")
	}

	totalDur := time.Since(startedAt)
	overseer.Publish(e.bus, InitCompleted{
		ContainerID: target.ContainerID,
		AgentName:   target.AgentName,
		Project:     target.Project,
		Duration:    totalDur,
		At:          time.Now(),
	})
	log.Info().
		Str("event", "agent_init_completed").
		Dur("duration", totalDur).
		Msg("agent.init: plan completed")
	return nil
}

// runStep dispatches one step's wire payload and waits for the matching
// Done or Error. Returns (exit_code, reason, transport_err). All three
// signals are needed because they have distinct downstream semantics:
//
//   - transport_err non-nil: stream is broken; caller bails out of Run
//   - reason non-empty: CP saw an Error response (spawn fail, timeout, etc.)
//     OR a non-zero Done that we annotated with stderr
//   - exit_code != 0: step ran to completion but exited non-zero
//
// Bounding wait time: clawkerd enforces the per-stage timeout server-
// side (ShellCommand.TimeoutSeconds → time.AfterFunc → SIGKILL +
// ERROR_CODE_TIMEOUT response), and the gRPC keepalive on this stream
// (consts.ClawkerdKeepalive*) breaks a wedged transport. CP-side
// wall-clock deadlines are deliberately omitted — a duplicate budget
// here would race the server-side timer and risk misclassifying a
// server-detected timeout as a client-side break.
func (e *Executor) runStep(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, containerID string, idx int, st step, log *logger.Logger) (int32, string, error) {
	commandID := buildCommandID(containerID, st.Name, idx)

	var cmd *clawkerdv1.Command
	switch st.Kind {
	case stepKindShell:
		cmd = &clawkerdv1.Command{
			CommandId: commandID,
			Payload:   &clawkerdv1.Command_Shell{Shell: st.Shell},
		}
	case stepKindAgentReady:
		cmd = &clawkerdv1.Command{
			CommandId: commandID,
			Payload:   &clawkerdv1.Command_AgentReady{AgentReady: &clawkerdv1.AgentReady{}},
		}
	default:
		return -1, "", fmt.Errorf("unknown step kind %d", st.Kind)
	}

	if err := stream.Send(cmd); err != nil {
		return -1, "", fmt.Errorf("send: %w", err)
	}

	// For ShellCommand steps, immediately follow with CloseStdin.
	// clawkerd's runShellCommand wires stage[0].Stdin through an
	// io.Pipe; exec.Cmd.Wait blocks in awaitGoroutines until the
	// stdin-copy goroutine drains, and that goroutine sits on Read
	// until the writer (clawkerd's stdinW) closes. CloseStdin is the
	// signal that closes stdinW. Without this, every init step that
	// doesn't consume stdin (which is all of them) would hang until
	// the entrypoint timeout fires. The receiver loop in clawkerd is
	// sequential, so the post-Send-startShellCommand registration of
	// rc.stdin happens-before our CloseStdin's routeCloseStdin
	// lookup — race-free as long as clawkerd publishes stdinW under
	// the registry lock at startShellCommand entry (it does, see
	// cmd/clawkerd/session.go).
	if st.Kind == stepKindShell {
		closeCmd := &clawkerdv1.Command{
			CommandId: commandID,
			Payload:   &clawkerdv1.Command_CloseStdin{CloseStdin: &clawkerdv1.CloseStdin{}},
		}
		if err := stream.Send(closeCmd); err != nil {
			return -1, "", fmt.Errorf("send close_stdin: %w", err)
		}
	}

	const maxStderrCapture = 4096
	var stderrBuf strings.Builder

	for {
		if ctx.Err() != nil {
			return -1, "", ctx.Err()
		}
		resp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return -1, "", fmt.Errorf("stream EOF before terminal response")
			}
			return -1, "", fmt.Errorf("recv: %w", err)
		}
		if resp.GetCommandId() != commandID {
			log.Debug().
				Str("event", "agent_init_unexpected_command_id").
				Str("got", resp.GetCommandId()).
				Str("expected", commandID).
				Str("payload_type", fmt.Sprintf("%T", resp.Payload)).
				Msg("agent.init: ignoring response with non-matching command_id")
			continue
		}
		switch p := resp.Payload.(type) {
		case *clawkerdv1.Response_Started, *clawkerdv1.Response_StageExit, *clawkerdv1.Response_Stdout:
			continue
		case *clawkerdv1.Response_Stderr:
			if p.Stderr != nil {
				if remaining := maxStderrCapture - stderrBuf.Len(); remaining > 0 {
					data := p.Stderr.GetData()
					if len(data) > remaining {
						data = data[:remaining]
					}
					stderrBuf.Write(data)
				}
			}
		case *clawkerdv1.Response_Done:
			exit := p.Done.GetFinalExitCode()
			if exit == 0 {
				return 0, "", nil
			}
			reason := fmt.Sprintf("exit_code=%d", exit)
			if s := strings.TrimSpace(stderrBuf.String()); s != "" {
				reason += "; stderr: " + s
			}
			return exit, reason, nil
		case *clawkerdv1.Response_Error:
			code := p.Error.GetCode().String()
			msg := p.Error.GetMessage()
			return -1, fmt.Sprintf("%s: %s", code, msg), nil
		default:
			log.Debug().
				Str("event", "agent_init_unknown_payload").
				Str("payload_type", fmt.Sprintf("%T", resp.Payload)).
				Msg("agent.init: ignoring unknown response payload")
		}
	}
}

// buildCommandID composes a stable, human-debuggable command_id for one
// init step dispatch. Bounded length keeps log lines compact while
// retaining enough container-id prefix to triage in multi-agent CP
// setups.
func buildCommandID(containerID, stepName string, idx int) string {
	prefix := containerID
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return fmt.Sprintf("init-%s-%s-%d", prefix, stepName, idx)
}

// plan builds the static init step list. Order is the load-bearing
// invariant: docker-socket runs first as the only privileged step,
// then user-scoped steps populate ~/.claude / ~/.gitconfig / ~/.ssh
// before post-init runs (which may reference any of them), and
// agent-ready closes the sequence to release the entrypoint.
func (e *Executor) plan() []step {
	return []step{
		{
			Name: "docker-socket",
			Kind: stepKindShell,
			Shell: &clawkerdv1.ShellCommand{
				Stages: []*clawkerdv1.PipeStage{{
					Argv: []string{"sh", "-c", `[ -S /var/run/docker.sock ] && chgrp docker /var/run/docker.sock || true`},
					Uid:  0,
					Gid:  0,
				}},
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		{
			Name: "config",
			Kind: stepKindShell,
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(configSeedScript)},
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		{
			Name: "git",
			Kind: stepKindShell,
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(gitconfigFilterScript)},
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		{
			Name: "git-credentials",
			Kind: stepKindShell,
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(gitCredentialsScript)},
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		{
			Name: "ssh",
			Kind: stepKindShell,
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(sshKnownHostsScript)},
				InitialStdin:   []byte(defaultKnownHosts),
				TimeoutSeconds: initStepTimeoutDefault,
			},
		},
		{
			Name: "post-init",
			Kind: stepKindShell,
			Shell: &clawkerdv1.ShellCommand{
				Stages:         []*clawkerdv1.PipeStage{userStage(postInitScript)},
				TimeoutSeconds: initStepTimeoutPostInit,
			},
		},
		{
			Name: "agent-ready",
			Kind: stepKindAgentReady,
		},
	}
}

// containerHomeDir is the unprivileged container user's home, fixed by
// the bundler's Dockerfile template (`/home/${USERNAME}` where USERNAME
// is consts.ContainerUser). Computed once here so the planner doesn't
// repeat the join.
var containerHomeDir = "/home/" + consts.ContainerUser

// userStage returns a fresh PipeStage running `sh -c <script>` as the
// unprivileged container user. consts is the source of truth for
// uid/gid/username — see consts.ContainerUID/ContainerGID/ContainerUser.
//
// HOME/USER override: clawkerd runs as root and spawns each stage with
// SysProcAttr.Credential to drop privileges, but Linux's setuid syscall
// does NOT update the process's HOME/USER env — those stay inherited
// from clawkerd (HOME=/root). Init scripts reference $HOME for config
// seed paths, gitconfig output, ssh known_hosts, post-init script
// location, etc.; without an explicit override they'd write to /root
// (permission denied → exit 1). We set HOME/USER per-stage so $HOME
// resolves to the unprivileged user's home regardless of clawkerd's
// environment. CALLERS dispatching ANY future ShellCommand with
// uid != 0 must do the same — clawkerd is a dumb pipe and won't fix
// HOME for you.
func userStage(script string) *clawkerdv1.PipeStage {
	return &clawkerdv1.PipeStage{
		Argv: []string{"sh", "-c", script},
		Uid:  uint32(consts.ContainerUID),
		Gid:  uint32(consts.ContainerGID),
		Env: map[string]string{
			"HOME": containerHomeDir,
			"USER": consts.ContainerUser,
		},
	}
}
