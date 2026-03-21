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
		{Path: "build.image", Label: storeui.Ptr("Base Image"), Description: storeui.Ptr("Docker base image for the container")},
		{Path: "build.dockerfile", Label: storeui.Ptr("Dockerfile"), Description: storeui.Ptr("Custom Dockerfile path (overrides image)")},
		{Path: "build.packages", Label: storeui.Ptr("Packages"), Description: storeui.Ptr("System packages to install (comma-separated)")},
		{Path: "build.context", Label: storeui.Ptr("Build Context"), Description: storeui.Ptr("Docker build context directory")},

		// Build — complex types hidden (prefix-based: hides all children)
		{Path: "build.build_args", Hidden: true},
		{Path: "build.instructions", Hidden: true},
		{Path: "build.inject", Hidden: true},

		// Agent — editable
		{Path: "agent.includes", Label: storeui.Ptr("Includes"), Description: storeui.Ptr("Files to include in the build context")},
		{Path: "agent.env_file", Label: storeui.Ptr("Env Files"), Description: storeui.Ptr("Environment files to load")},
		{Path: "agent.from_env", Label: storeui.Ptr("Forward Env Vars"), Description: storeui.Ptr("Host env vars to forward to the container")},
		{Path: "agent.memory", Label: storeui.Ptr("Memory"), Description: storeui.Ptr("Container memory limit")},
		{Path: "agent.editor", Label: storeui.Ptr("Editor"), Description: storeui.Ptr("Default editor inside the container")},
		{Path: "agent.visual", Label: storeui.Ptr("Visual Editor"), Description: storeui.Ptr("Default visual editor")},
		{Path: "agent.shell", Label: storeui.Ptr("Shell"), Description: storeui.Ptr("Default shell inside the container")},
		{Path: "agent.enable_shared_dir", Label: storeui.Ptr("Enable Shared Dir"), Description: storeui.Ptr("Mount ~/.clawker-share into the container")},
		{Path: "agent.post_init", Label: storeui.Ptr("Post-Init Script"), Description: storeui.Ptr("Script to run after container initialization")},

		// Agent — complex types hidden
		{Path: "agent.env", Hidden: true},
		{Path: "agent.claude_code", Hidden: true},

		// Workspace
		{Path: "workspace.default_mode", Label: storeui.Ptr("Default Mode"), Description: storeui.Ptr("Workspace mounting mode"),
			Kind: storeui.Ptr(storeui.KindSelect), Options: []string{"bind", "snapshot"}},

		// Security — editable (defaults match SecurityConfig convenience methods)
		{Path: "security.docker_socket", Label: storeui.Ptr("Docker Socket"), Description: storeui.Ptr("Mount Docker socket inside the container")},
		{Path: "security.enable_host_proxy", Label: storeui.Ptr("Host Proxy"), Description: storeui.Ptr("Enable host proxy for browser auth and credential forwarding")},
		{Path: "security.firewall.add_domains", Label: storeui.Ptr("Firewall Domains"), Description: storeui.Ptr("Additional domains to whitelist (comma-separated)")},

		// Security — git credentials (defaults match GitCredentialsConfig convenience methods)
		{Path: "security.git_credentials.forward_https", Label: storeui.Ptr("Forward HTTPS"), Description: storeui.Ptr("Enable HTTPS credential forwarding")},
		{Path: "security.git_credentials.forward_ssh", Label: storeui.Ptr("Forward SSH"), Description: storeui.Ptr("Enable SSH agent forwarding")},
		{Path: "security.git_credentials.forward_gpg", Label: storeui.Ptr("Forward GPG"), Description: storeui.Ptr("Enable GPG agent forwarding")},
		{Path: "security.git_credentials.copy_git_config", Label: storeui.Ptr("Copy Git Config"), Description: storeui.Ptr("Copy host .gitconfig into the container")},

		// Security — complex types hidden
		{Path: "security.firewall.rules", Hidden: true},
		{Path: "security.firewall.ip_range_sources", Hidden: true},
		{Path: "security.cap_add", Hidden: true},

		// Loop — editable
		{Path: "loop.max_loops", Label: storeui.Ptr("Max Loops"), Description: storeui.Ptr("Maximum number of autonomous loops")},
		{Path: "loop.stagnation_threshold", Label: storeui.Ptr("Stagnation Threshold"), Description: storeui.Ptr("Loops without progress before stopping")},
		{Path: "loop.timeout_minutes", Label: storeui.Ptr("Timeout (min)"), Description: storeui.Ptr("Maximum runtime in minutes")},
		{Path: "loop.calls_per_hour", Label: storeui.Ptr("Calls per Hour"), Description: storeui.Ptr("Rate limit for API calls")},
		{Path: "loop.completion_threshold", Label: storeui.Ptr("Completion Threshold"), Description: storeui.Ptr("Score threshold to consider task complete")},
		{Path: "loop.session_expiration_hours", Label: storeui.Ptr("Session Expiration (hrs)")},
		{Path: "loop.same_error_threshold", Label: storeui.Ptr("Same Error Threshold")},
		{Path: "loop.output_decline_threshold", Label: storeui.Ptr("Output Decline Threshold")},
		{Path: "loop.max_consecutive_test_loops", Label: storeui.Ptr("Max Consecutive Test Loops")},
		{Path: "loop.loop_delay_seconds", Label: storeui.Ptr("Loop Delay (sec)")},
		{Path: "loop.safety_completion_threshold", Label: storeui.Ptr("Safety Completion Threshold")},
		{Path: "loop.skip_permissions", Label: storeui.Ptr("Skip Permissions"), Description: storeui.Ptr("Skip permission prompts in loops")},
		{Path: "loop.hooks_file", Label: storeui.Ptr("Hooks File"), Description: storeui.Ptr("Path to hooks file for loop events")},
		{Path: "loop.append_system_prompt", Label: storeui.Ptr("Append System Prompt"), Description: storeui.Ptr("Additional system prompt for loops")},
	}
}

// LayerTargets builds the per-field save destinations for project config.
// Targets: Local (CWD dot-file), User (config dir), plus Original if provenance exists.
// Paths and filenames come from config accessors, never hardcoded.
func LayerTargets(store *storage.Store[config.Project], cfg config.Config) []storeui.LayerTarget {
	filename := cfg.ProjectConfigFileName()

	var targets []storeui.LayerTarget
	seen := make(map[string]bool)

	// Local: CWD dot-file (skipped if CWD is unavailable).
	cwd, cwdErr := os.Getwd()
	if cwdErr == nil {
		localPath := storeui.ResolveLocalPath(cwd, filename)
		targets = append(targets, storeui.LayerTarget{
			Label:       "Local",
			Description: storeui.ShortenHome(localPath),
			Path:        localPath,
		})
		seen[localPath] = true
	}

	// User: config dir file.
	userPath := filepath.Join(config.ConfigDir(), filename)
	if !seen[userPath] {
		targets = append(targets, storeui.LayerTarget{
			Label:       "User",
			Description: storeui.ShortenHome(userPath),
			Path:        userPath,
		})
		seen[userPath] = true
	}

	// Original: add any discovered layers not already in the list.
	for _, l := range store.Layers() {
		if !seen[l.Path] {
			targets = append(targets, storeui.LayerTarget{
				Label:       layerLabel(l, config.ConfigDir(), cwd),
				Description: storeui.ShortenHome(l.Path),
				Path:        l.Path,
			})
			seen[l.Path] = true
		}
	}

	return targets
}

// layerLabel produces a human-readable label for a layer based on its path.
func layerLabel(l storage.LayerInfo, configDir, cwd string) string {
	dir := filepath.Dir(l.Path)

	switch {
	case dir == configDir || strings.HasPrefix(dir, configDir+string(os.PathSeparator)):
		return "User"
	case cwd != "" && dir == cwd:
		return "Local"
	case cwd != "" && strings.HasPrefix(dir, cwd+string(os.PathSeparator)):
		rel, _ := filepath.Rel(cwd, l.Path)
		return "Local (" + rel + ")"
	default:
		return "Project"
	}
}

// Edit runs an interactive project config editor.
func Edit(ios *iostreams.IOStreams, store *storage.Store[config.Project], cfg config.Config) (storeui.Result, error) {
	return storeui.Edit(ios, store,
		storeui.WithTitle("Project Configuration Editor"),
		storeui.WithOverrides(Overrides()),
		storeui.WithLayerTargets(LayerTargets(store, cfg)),
	)
}
