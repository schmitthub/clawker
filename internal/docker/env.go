package docker

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
)

// RuntimeEnvOpts describes the environment variables RuntimeEnv can produce.
// Each field maps to a specific env var or category of env vars.
type RuntimeEnvOpts struct {
	// Clawker identity (consumed by statusline)
	Version         string
	Project         string
	Agent           string
	WorkspaceMode   string // "bind" or "snapshot"
	WorkspaceSource string // host path being mounted
	// Worktree marks a workspace that is a linked git worktree, with the main
	// repo's .git directory mounted separately (see workspace.SetupMounts).
	Worktree bool

	// Editor preferences
	Editor string
	Visual string

	// Firewall
	FirewallEnabled bool
	// CPHealthzURL is the plain-HTTP /healthz URL the entrypoint polls
	// before running CMD. Empty string skips the poll (only meaningful
	// when FirewallEnabled=false). Reachable via Docker's bridge DNS
	// because the agent container and CP share the clawker network.
	CPHealthzURL string

	// clawkerd bootstrap targets. clawkerd reads these to find the CP's
	// Hydra public endpoint (token exchange) and the CP's agent gRPC
	// listener on the clawker network (Connect dial), and to know which slot
	// to consume at Connect time. Empty string omits the env var.
	ClawkerdHydraURL  string // CLAWKER_CP_HYDRA_URL
	ClawkerdAgentAddr string // CLAWKER_CP_AGENT_ADDR

	// Monitoring stack
	MonitoringActive bool // Whether the monitoring stack (otel-collector) is running

	// Socket forwarding (consumed by socket-forwarder in container)
	GPGForwardingEnabled bool // Enable GPG agent forwarding
	SSHForwardingEnabled bool // Enable SSH agent forwarding

	// Terminal capabilities (from host)
	Is256Color bool
	TrueColor  bool

	// User-defined overrides (arbitrary pass-through)
	AgentEnv       map[string]string
	InstructionEnv map[string]string
}

// RuntimeEnv produces container environment variables from explicit options.
// Precedence (last wins): base defaults → terminal capabilities → agent env → instruction env.
// The result is sorted by key for deterministic ordering.
func RuntimeEnv(opts RuntimeEnvOpts) ([]string, error) {
	m := make(map[string]string)

	// Clawker identity (consumed by the statusline AND by clawkerd
	// as `req.AgentName` at Connect — same value, single source).
	if opts.Project != "" {
		m[consts.EnvProject] = opts.Project
	}
	if opts.Agent != "" {
		m[consts.EnvAgent] = opts.Agent
	}
	if opts.WorkspaceMode != "" {
		m[consts.EnvWorkspaceMode] = opts.WorkspaceMode
	}
	if opts.WorkspaceSource != "" {
		m[consts.EnvWorkspaceSource] = opts.WorkspaceSource
	}
	if opts.Version != "" {
		m[consts.EnvVersion] = opts.Version
	}

	// Base defaults: editor/visual
	editor := opts.Editor
	if editor == "" {
		editor = "nano"
	}
	visual := opts.Visual
	if visual == "" {
		visual = "nano"
	}
	m["EDITOR"] = editor
	m["VISUAL"] = visual

	// Terminal capabilities from host
	if opts.Is256Color {
		m["TERM"] = "xterm-256color"
	}
	if opts.TrueColor {
		m["COLORTERM"] = "truecolor"
	}

	// Firewall (simple flag — eBPF programs attached post-start via manager.Enable)
	if opts.FirewallEnabled {
		m[consts.EnvFirewallEnabled] = "true"
	}
	if opts.CPHealthzURL != "" {
		m[consts.EnvCPHealthzURL] = opts.CPHealthzURL
	}

	// clawkerd bootstrap env vars — only what the daemon can authoritatively
	// assert. Container ID is server-derived from the slot at Connect;
	// project + agent travel as separate wire fields and the CP composes
	// the AgentFullName on its side via auth.AgentFullName.
	if opts.ClawkerdHydraURL != "" {
		m[consts.EnvClawkerdHydraURL] = opts.ClawkerdHydraURL
	}
	if opts.ClawkerdAgentAddr != "" {
		m[consts.EnvClawkerdAgentAddr] = opts.ClawkerdAgentAddr
	}

	// Telemetry resource attributes for per-project/agent segmentation.
	// The OTEL collector's transform/metrics processor copies these onto
	// datapoint attributes so Prometheus exposes them as metric labels;
	// the OpenSearch log exporters also persist them as resource fields,
	// where the index templates applied by clawker-opensearch-bootstrap
	// type them explicitly as keywords (see otel-config.yaml + the
	// component-templates/clawker-common.json mapping). They are not
	// dynamically-mapped fields.
	var resourceAttrs []string
	if opts.Project != "" {
		resourceAttrs = append(resourceAttrs, "project="+opts.Project)
	}
	if opts.Agent != "" {
		resourceAttrs = append(resourceAttrs, "agent="+opts.Agent)
	}
	if len(resourceAttrs) > 0 {
		m[consts.EnvOTelResourceAttributes] = strings.Join(resourceAttrs, ",")
	}

	// Disable telemetry when monitoring stack is not running.
	// The Dockerfile sets CLAUDE_CODE_ENABLE_TELEMETRY=1 by default; override it
	// here so containers don't silently attempt exports to unreachable otel-collector.
	// Users can still force-enable via agent.env (applied after this).
	if !opts.MonitoringActive {
		m[consts.EnvClaudeCodeEnableTelemetry] = "0"
	}

	// Socket forwarding (consumed by clawker-socket-server binary inside container)
	if opts.GPGForwardingEnabled || opts.SSHForwardingEnabled {
		var sockets []map[string]string
		if opts.GPGForwardingEnabled {
			sockets = append(sockets, map[string]string{
				"path": consts.ContainerHomeDir + "/.gnupg/S.gpg-agent",
				"type": consts.SocketTypeGPGAgent,
			})
			// Override gpg.program via env vars so the container's gpg binary is used
			// regardless of what the host's gitconfig (global or local) specifies.
			// Env-based config overrides all file-based git config levels including
			// local .git/config, which is bind-mounted from the host in bind mode.
			m["GIT_CONFIG_COUNT"] = "1"
			m["GIT_CONFIG_KEY_0"] = "gpg.program"
			m["GIT_CONFIG_VALUE_0"] = "/usr/bin/gpg"
		}
		if opts.SSHForwardingEnabled {
			sshAgentSock := consts.ContainerHomeDir + "/.ssh/agent.sock"
			sockets = append(sockets, map[string]string{
				"path": sshAgentSock,
				"type": consts.SocketTypeSSHAgent,
			})
			// SSH tools need SSH_AUTH_SOCK to find the forwarded socket
			m["SSH_AUTH_SOCK"] = sshAgentSock
		}
		socketsBytes, err := json.Marshal(sockets)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal remote sockets: %w", err)
		}
		m[consts.EnvRemoteSockets] = string(socketsBytes)
	}

	// Worktree containers: disable Go VCS stamping. Go's VCS discovery only
	// recognizes a .git *directory* — it skips a linked worktree's .git file
	// and walks up to the bind-mounted main .git, where `git status` either
	// fails with exit 128 (dubious ownership: the repo top-level is a
	// root-owned Docker scaffold dir) or, if unblocked, would stamp the wrong
	// revision (the main checkout's HEAD with the entire tree appearing
	// deleted, since only .git is mounted). Stamping can never be correct in
	// this topology, so default it off. agent.env / instruction env override.
	if opts.Worktree {
		m["GOFLAGS"] = "-buildvcs=false"
	}

	// Agent env vars (override base defaults and terminal)
	maps.Copy(m, opts.AgentEnv)

	// User-defined env from build instructions (highest precedence)
	maps.Copy(m, opts.InstructionEnv)

	// Sort keys for deterministic output
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+m[k])
	}
	return env, nil
}
