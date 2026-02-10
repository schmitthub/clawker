Feature: Container filesystem initialization with user Claude Code configs
As a Clawker user
I want my Claude Code configuration managed during container initialization
So that containerized agents inherit my local settings, skills, and plugins

Background:
Given a lightweight Alpine test container
And the clawker containerfs init entrypoint is available
And "$HOME/.claude/" exists in the container from the build phase

# ---------------------------------------------------------------------------
# Config source resolution
# ---------------------------------------------------------------------------

Rule: The copy strategy must resolve the host config directory before copying

    Scenario: Host config dir is resolved from CLAUDE_CONFIG_DIR environment variable
      Given the environment variable "CLAUDE_CONFIG_DIR" is set to "/custom/claude-config"
      And the directory "/custom/claude-config" exists on the host
      And the config strategy is "copy"
      When the container filesystem is initialized
      Then the config source directory should be "/custom/claude-config"

    Scenario: Host config dir falls back to ~/.claude/ when CLAUDE_CONFIG_DIR is unset
      Given the environment variable "CLAUDE_CONFIG_DIR" is not set
      And the directory "~/.claude/" exists on the host
      And the config strategy is "copy"
      When the container filesystem is initialized
      Then the config source directory should be "~/.claude/"

    Scenario: Initialization fails when no host config directory is found
      Given the environment variable "CLAUDE_CONFIG_DIR" is not set
      And the directory "~/.claude/" does not exist on the host
      And the config strategy is "copy"
      When the container filesystem is initialized
      Then initialization should fail with a "claude config dir not found on host" error

# ---------------------------------------------------------------------------
# Strategy: copy
# ---------------------------------------------------------------------------

Rule: Copy strategy replicates select Claude Code config into the container

    Scenario: settings.json enabledPlugins are merged into container settings
      Given the config strategy is "copy"
      And the host config dir contains "settings.json" with an "enabledPlugins" dictionary
      When the container filesystem is initialized
      Then "$HOME/.claude/settings.json" in the container should exist
      And its "enabledPlugins" dictionary should contain the merged entries from the host

    Scenario Outline: Directories are copied in full to the container
      Given the config strategy is "copy"
      And the host config dir contains the "<directory>" directory with files
      When the container filesystem is initialized
      Then "$HOME/.claude/<directory>/" should exist in the container
      And its contents should match the host source

      Examples:
        | directory |
        | agents    |
        | skills    |
        | commands  |

    Scenario: Plugins directory is copied excluding cache artifacts
      Given the config strategy is "copy"
      And the host config dir contains the "plugins" directory with:
        | entry                      | type      |
        | some-plugin/               | directory |
        | cache/                     | directory |
        | install-counts-cache.json  | file      |
        | known_marketplaces.json    | file      |
      When the container filesystem is initialized
      Then "$HOME/.claude/plugins/" should exist in the container
      And "some-plugin/" should be present
      And "cache/" should not be present
      And "install-counts-cache.json" should not be present

    Scenario: known_marketplaces.json installPaths are rewritten for the container
      Given the config strategy is "copy"
      And the host "plugins/known_marketplaces.json" contains "installPath" values with the host's absolute path
      When the container filesystem is initialized
      Then "$HOME/.claude/plugins/known_marketplaces.json" should exist in the container
      And all "installPath" values should reference the container user's absolute path

    Scenario: Missing host files and directories are skipped without error
      Given the config strategy is "copy"
      And the host config dir does not contain "agents/"
      And the host config dir does not contain "skills/"
      And the host config dir does not contain "plugins/"
      And the host config dir does not contain "commands/"
      And the host config dir does not contain "settings.json"
      When the container filesystem is initialized
      Then initialization should succeed
      And the log file should contain entries noting each missing item was skipped

    Scenario: Symlinked files and directories are resolved before copying
      Given the config strategy is "copy"
      And the host config dir contains "agents/" as a symlink to another directory
      And the host config dir contains "settings.json" as a symlink to another file
      When the container filesystem is initialized
      Then "$HOME/.claude/agents/" should exist in the container as a real directory
      And "$HOME/.claude/settings.json" should exist in the container as a real file
      And neither should be a symlink

# ---------------------------------------------------------------------------
# Strategy: fresh
# ---------------------------------------------------------------------------

Rule: Fresh strategy produces a minimal Claude Code config

    Scenario: Fresh strategy with host auth adds only credentials
      Given the config strategy is "fresh"
      And use_host_auth is true
      When the container filesystem is initialized
      Then "$HOME/.claude/.credentials.json" should be the only file added to "$HOME/.claude/"

    Scenario: Fresh strategy without host auth adds nothing to the config directory
      Given the config strategy is "fresh"
      And use_host_auth is false
      When the container filesystem is initialized
      Then no files should be added to "$HOME/.claude/" by initialization

# ---------------------------------------------------------------------------
# Host auth: true
# ---------------------------------------------------------------------------

Rule: Host auth provisions onboarding and credential files

    Scenario: Onboarding marker is written when host auth is enabled
      Given use_host_auth is true
      When the container filesystem is initialized
      Then "$HOME/.claude.json" should exist in the container
      And it should contain:
        """json
        {
          "hasCompletedOnboarding": true
        }
        """

    Scenario: OAuth credentials file is written when host auth is enabled
      Given use_host_auth is true
      When the container filesystem is initialized
      Then "$HOME/.claude/.credentials.json" should exist in the container
      And it should contain a "claudeAiOauth" object with keys:
        | key              | type   |
        | accessToken      | string |
        | refreshToken     | string |
        | expiresAt        | number |
        | scopes           | array  |
        | subscriptionType | string |
        | rateLimitTier    | string |
      And it should contain an "organizationUuid" string

# ---------------------------------------------------------------------------
# Host auth: false
# ---------------------------------------------------------------------------

Rule: Disabled host auth leaves no credential artifacts

    Scenario: No auth files exist when host auth is disabled
      Given use_host_auth is false
      When the container filesystem is initialized
      Then "$HOME/.claude.json" should not exist in the container
      And "$HOME/.claude/.credentials.json" should not exist in the container
      And "$HOME/.claude/" should exist but contain no credential files

# ---------------------------------------------------------------------------
# Shared directory
# ---------------------------------------------------------------------------
# NOTE: These scenarios are implemented in `workspace.SetupMounts()`, not in
# `containerfs` or `opts.InitContainerConfig`. The shared volume is wired via
# `workspace.EnsureShareVolume()` and `GetShareVolumeMount()` in
# `internal/workspace/strategy.go`. Tests belong in `test/internals/workspace_test.go`.
# ---------------------------------------------------------------------------

Rule: Shared directory mirrors host content into the container

    Scenario: Shared dir is populated when enabled
      Given enable_shared_dir is true
      And the host "$CLAWKER_HOME/container-share/" contains files
      When workspace mounts are set up
      Then "$HOME/.clawker-share/" should exist in the container
      And its contents should be identical to the host's "$CLAWKER_HOME/container-share/"
      # Tested in: test/internals/workspace_test.go

    Scenario: No shared volume mounted when disabled
      Given enable_shared_dir is false
      When workspace mounts are set up
      Then no volume should be mounted at "$HOME/.clawker-share/"
      And the directory should be empty (exists from image but has no volume backing)
      # Tested in: test/internals/workspace_test.go
