# Container Initialize Feature

## Overview

New containers with Claude Code require authentication and plugin installation each time. This is an exhausting
exercise for clawker users. This feature will allow support of copying a golden claude files to copy into the container's
home dir for initial claude code settings. There is plenty of existing infra to support this. Part of this feature
work will involve refactoring / auditing the existing code for workspace and setup mounts

reference documents, remember these exist but don't read them unless you need to:

- `@.serena/claude-code-authentication.md`: covers claude code authentication
- `@.serena/claude-code-settings.md`: covers claude code settings

### Claude Code Internals

* Claude code instances require a shared, hard coded ~/.claude.json file. This file tracks session information across system wide claude code instances using atomic writes "temp rename strategy, with writefs fallback". each claude code instance relies on this file and writes to it constantly with overall state like usage metrics, project registration, etc
* Claude code uses a ~/.claude/ directory to store plugins, user settings, statusline scripts, skill files, commmand files, plan files, task files. All claude code instances also share read/write this directory
* Claude stores authentication tokens in the system keyring if one exists or falls back to ~/.claude/.credentials.json. the schema is as follows (we already have a feature for this using "shared-globals" but it is brittle)
```json
{
  "claudeAiOauth": {
    "accessToken": "",
    "refreshToken": "",
    "expiresAt": 1770658802316,
    "scopes": [
      "",
      "",
      "",
      ""
    ],
    "subscriptionType": "",
    "rateLimitTier": ""
  },
  "organizationUuid": ""
}
```

## Requirements

### Container names volumes

Containers always get two named volumes:
* $containerName-command-history: /commandhistory
* $containerName-claude-code-config: ~/.claude/

How they are populated is based on the clawker project configuration

### User project level agent claude config option

* User controls agent.claude_code.config.strategy in clawker.yaml. Options are "copy" or "fresh"
  * Fresh: creates two empty named volumes
  * Copy:
    * does a one time copy of specific files and directories from the user's ~/.claude/ directory

#### User's claude config copy
Clawker should first check if the host has `CLAUDE_CONFIG_DIR` or use `~/.claude/` as the copy dir source and confirm
it exists. if it doesn't return a claude config dir not found on host error

Within this directory you need to do the following int the container users $HOME/.claude/ dir

* **settings.json**: merge the "enabledPlugins" dictionary (just like we do already with statusline)
* **agents/**: copy the entire directory and contents
* **skills/**: copy the entire directory and contents
* **commands/**: copy the entire directory and contents
* **plugins/**: copy the entire directory and contents except for
  * `cache/`: completely ignore
  * `install-counts-cache.json`: completely ignore
  * `known_marketplaces.json`: str replace all "installPath" values from the host's abs path to the container's abs path

Some of these files and directories may not exist on the host, if they don't skip them and log to file that they don't exist and won't be copied, but this is not an error
Some of these files may be symlinks so we need to resolve them and copy the real file or directory, not the symlink

### Always create an initial .claude.json in the container home dir

Regardless of config mode, a `~/.claude.json` always needs to be created in the container one time during creation
with defaults before claude code runs, hopefully claude code merges its init data to it when it first starts
(it should) but we'll have to test.

the file should contain. This will hopefully stop claude from asking the user to auth
```json
{
  "hasCompletedOnboarding": true
}
```

### Refactoring

#### Refactor clawker-globals

Rename to `clawker-share` and bind mount it to $CLAWKER_HOME/container-share/. We can document that users can freely
drop files into this directory if they want to access arbitrary files from any of their containers. Ensure it is read only




