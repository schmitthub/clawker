// Package project provides the domain adapter for editing config.Project via storeui.
package project

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/storeui"
)

// Overrides returns field overrides for config.Project.
func Overrides() []storeui.Override {
	return []storeui.Override{
		// Build — editable
		{Path: "build.image", Label: ptr("Base Image"), Description: ptr("Docker base image for the container")},
		{Path: "build.dockerfile", Label: ptr("Dockerfile"), Description: ptr("Custom Dockerfile path (overrides image)")},
		{Path: "build.packages", Label: ptr("Packages"), Description: ptr("System packages to install (comma-separated)")},
		{Path: "build.context", Label: ptr("Build Context"), Description: ptr("Docker build context directory")},

		// Build — complex types hidden (prefix-based: hides all children)
		{Path: "build.build_args", Hidden: true},
		{Path: "build.instructions", Hidden: true},
		{Path: "build.inject", Hidden: true},

		// Agent — editable
		{Path: "agent.includes", Label: ptr("Includes"), Description: ptr("Files to include in the build context")},
		{Path: "agent.env_file", Label: ptr("Env Files"), Description: ptr("Environment files to load")},
		{Path: "agent.from_env", Label: ptr("Forward Env Vars"), Description: ptr("Host env vars to forward to the container")},
		{Path: "agent.memory", Label: ptr("Memory"), Description: ptr("Container memory limit")},
		{Path: "agent.editor", Label: ptr("Editor"), Description: ptr("Default editor inside the container")},
		{Path: "agent.visual", Label: ptr("Visual Editor"), Description: ptr("Default visual editor")},
		{Path: "agent.shell", Label: ptr("Shell"), Description: ptr("Default shell inside the container")},
		{Path: "agent.enable_shared_dir", Label: ptr("Enable Shared Dir"), Description: ptr("Mount ~/.clawker-share into the container")},
		{Path: "agent.post_init", Label: ptr("Post-Init Script"), Description: ptr("Script to run after container initialization")},

		// Agent — complex types hidden
		{Path: "agent.env", Hidden: true},
		{Path: "agent.claude_code", Hidden: true},

		// Workspace
		{Path: "workspace.default_mode", Label: ptr("Default Mode"), Description: ptr("Workspace mounting mode"),
			Kind: ptr(storeui.KindSelect), Options: []string{"bind", "snapshot"}},

		// Security — editable
		{Path: "security.docker_socket", Label: ptr("Docker Socket"), Description: ptr("Mount Docker socket inside the container")},
		{Path: "security.enable_host_proxy", Label: ptr("Host Proxy"), Description: ptr("Enable host proxy for browser auth and credential forwarding")},
		{Path: "security.firewall.add_domains", Label: ptr("Firewall Domains"), Description: ptr("Additional domains to whitelist (comma-separated)")},

		// Security — git credentials (editable as individual fields)
		{Path: "security.git_credentials.forward_https", Label: ptr("Forward HTTPS"), Description: ptr("Enable HTTPS credential forwarding")},
		{Path: "security.git_credentials.forward_ssh", Label: ptr("Forward SSH"), Description: ptr("Enable SSH agent forwarding")},
		{Path: "security.git_credentials.forward_gpg", Label: ptr("Forward GPG"), Description: ptr("Enable GPG agent forwarding")},
		{Path: "security.git_credentials.copy_git_config", Label: ptr("Copy Git Config"), Description: ptr("Copy host .gitconfig into the container")},

		// Security — complex types hidden
		{Path: "security.firewall.rules", Hidden: true},
		{Path: "security.firewall.ip_range_sources", Hidden: true},
		{Path: "security.cap_add", Hidden: true},

		// Loop — editable
		{Path: "loop.max_loops", Label: ptr("Max Loops"), Description: ptr("Maximum number of autonomous loops")},
		{Path: "loop.stagnation_threshold", Label: ptr("Stagnation Threshold"), Description: ptr("Loops without progress before stopping")},
		{Path: "loop.timeout_minutes", Label: ptr("Timeout (min)"), Description: ptr("Maximum runtime in minutes")},
		{Path: "loop.calls_per_hour", Label: ptr("Calls per Hour"), Description: ptr("Rate limit for API calls")},
		{Path: "loop.completion_threshold", Label: ptr("Completion Threshold"), Description: ptr("Score threshold to consider task complete")},
		{Path: "loop.session_expiration_hours", Label: ptr("Session Expiration (hrs)")},
		{Path: "loop.same_error_threshold", Label: ptr("Same Error Threshold")},
		{Path: "loop.output_decline_threshold", Label: ptr("Output Decline Threshold")},
		{Path: "loop.max_consecutive_test_loops", Label: ptr("Max Consecutive Test Loops")},
		{Path: "loop.loop_delay_seconds", Label: ptr("Loop Delay (sec)")},
		{Path: "loop.safety_completion_threshold", Label: ptr("Safety Completion Threshold")},
		{Path: "loop.skip_permissions", Label: ptr("Skip Permissions"), Description: ptr("Skip permission prompts in loops")},
		{Path: "loop.hooks_file", Label: ptr("Hooks File"), Description: ptr("Path to hooks file for loop events")},
		{Path: "loop.append_system_prompt", Label: ptr("Append System Prompt"), Description: ptr("Additional system prompt for loops")},
	}
}

// SaveTargets builds human-readable save target options from store layers.
func SaveTargets(store *storage.Store[config.Project]) []storeui.SaveTarget {
	layers := store.Layers()
	configDir := config.ConfigDir()
	cwd, _ := os.Getwd()

	targets := make([]storeui.SaveTarget, 0, len(layers)+1)

	// With multiple layers, offer provenance routing as the first option.
	if len(layers) > 1 {
		targets = append(targets, storeui.SaveTarget{
			Label:       "Original locations",
			Description: "Route each section back to the file it came from",
		})
	}

	for _, l := range layers {
		label, desc := layerLabel(l, configDir, cwd)
		targets = append(targets, storeui.SaveTarget{
			Label:       label,
			Description: desc,
			Filename:    l.Filename,
		})
	}

	return targets
}

// layerLabel produces a human-readable label for a layer based on its path.
func layerLabel(l storage.LayerInfo, configDir, cwd string) (label, desc string) {
	dir := filepath.Dir(l.Path)

	switch {
	case dir == configDir || strings.HasPrefix(dir, configDir+string(os.PathSeparator)):
		return "User config", shortenPath(l.Path)
	case cwd != "" && dir == cwd:
		return "Current directory", shortenPath(l.Path)
	case cwd != "" && strings.HasPrefix(dir, cwd+string(os.PathSeparator)):
		rel, _ := filepath.Rel(cwd, l.Path)
		return "Local (" + rel + ")", shortenPath(l.Path)
	default:
		return "Project root", shortenPath(l.Path)
	}
}

// shortenPath replaces $HOME with ~ for display.
func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// Edit runs an interactive project config editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Project]) (storeui.Result, error) {
	return storeui.Edit(ios, store,
		storeui.WithTitle("Project Configuration Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithSaveTargets(SaveTargets(store)),
	)
}

func ptr[T any](v T) *T {
	return &v
}
