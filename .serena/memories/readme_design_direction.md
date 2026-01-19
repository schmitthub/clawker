# README Design Direction

## Overview
The README was overhauled in January 2025 to follow a story-driven structure that hooks readers with the problem, shows immediate value, and progressively reveals depth.

## Design Principles

1. **Problem-first intro** - Lead with pain points (YOLO mode dangers, Docker tedium, broken OAuth, missing git creds) before the solution
2. **Progressive disclosure** - Quick Start first, details later
3. **Practical over technical** - Workflows section shows real usage patterns
4. **Visual architecture** - ASCII diagrams for system overview
5. **whail at the end** - Technical foundation explained after users understand the value

## Structure

1. Header + badges (Go, License, macOS)
2. Problem statement (conversational, polished)
3. Quick Start (minimal steps to running)
4. Clawker Generate overview for dockerfile generation (standalone and integrated)
5. Commands Overview (most used commands with brief descriptions, explain this is a port of docker CLI for familiarity)
6. Seamless Authentication (subscription users and API KEY) & Git (host proxy explained with flow diagram)
7. Workflows (containers, Claude options, detach/reattach, management)
8. Monitoring (brief, optional)
9. System Overview (ASCII architecture diagram)
10. Configuration (full clawker.yaml and ~/.local/clawker/settings.yaml example)
11. Security Defaults (compact table)
12. The whail Engine (label isolation explanation with docker vs clawker example)
13. Known Issues (Claude TUI redraw)
14. Contributing + License

## Config Schema Reference

**IMPORTANT:** When updating README or documentation, always verify config examples match the current schema.

**Schema locations:**
- Project config: `internal/config/schema.go`
- User settings: `internal/config/settings.go`

Key types to check:

**User Settings (~/.local/clawker/settings.yaml):**
- `Settings` - root structure (project, projects)
- `ProjectDefaults` - default_image
- `Config` - root structure (version, project, default_image, build, agent, workspace, security)
- `BuildConfig` - image, dockerfile, packages, context, build_args, instructions, inject
- `DockerInstructions` - copy, env, labels, expose, args, volumes, workdir, healthcheck, shell, user_run, root_run
- `InjectConfig` - after_from, after_packages, after_user_setup, after_user_switch, after_claude_install, before_entrypoint
- `AgentConfig` - includes, env, memory, editor, visual, shell
- `WorkspaceConfig` - remote_path, default_mode
- `SecurityConfig` - enable_firewall, docker_socket, allowed_domains, cap_add, enable_host_proxy, git_credentials
- `GitCredentialsConfig` - forward_https, forward_ssh, copy_git_config

## Key Decisions

- **No logo yet** - Text-only header with shields.io badges
- **GitHub URL**: `schmitthub/clawker`
- **Tone**: Fun intro with problem statement, then practical workflows
- **Auth flow diagram**: Simple ASCII showing Container → Host Proxy → Browser flow
- **System diagram**: Shows all components (CLI, Host Proxy, Container scripts, Monitoring, Docker)
- **whail featured prominently** - Moved to dedicated section at end as reusable foundation

## Commits
- `1e5d99c` - "Overhaul README with story-driven structure and system diagram"
- Pending: Added Dockerfile Generation, Commands Overview, API Key auth, settings.yaml sections
