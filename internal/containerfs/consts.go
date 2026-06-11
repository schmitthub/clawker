package containerfs

// Claude Code configuration layout — the file and directory names Claude
// Code itself reads under its config dir. Seeding a container's config
// volume reproduces this layout; these names are Claude Code's contract,
// not clawker's to vary.
const (
	// claudeConfigDirEnv overrides the host Claude config dir location.
	claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"
	// credentialsFile holds the OAuth credential blob.
	credentialsFile = ".credentials.json"
	// settingsFile is Claude Code's settings.json.
	settingsFile = "settings.json"
	// claudeMDFile is the user's global instructions file.
	claudeMDFile = "CLAUDE.md"
	// enabledPluginsKey is the settings.json key carried into containers.
	enabledPluginsKey = "enabledPlugins"
	// Subdirectories copied into the container config volume.
	agentsSubdir   = "agents"
	skillsSubdir   = "skills"
	commandsSubdir = "commands"
	pluginsSubdir  = "plugins"
	// Plugin registry files; marketplace/plugin paths are rewritten to
	// container paths, the install-counts cache is skipped.
	knownMarketplacesFile = "known_marketplaces.json"
	installedPluginsFile  = "installed_plugins.json"
	installCountsFile     = "install-counts-cache.json"
)
