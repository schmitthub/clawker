# Clawker Troubleshooting

Diagnostic decision trees for common failures. Follow each flow step by step.

## Contents

- [clawker not found](#clawker-not-found)
- [Container can't reach a domain](#container-cant-reach-a-domain)
- [Build failed](#build-failed)
- [Credentials not working](#credentials-not-working)
- [MCP server won't start](#mcp-server-wont-start)
- [Config not taking effect](#config-not-taking-effect)
- [Container won't start](#container-wont-start)

---

## clawker not found

User reports `clawker: command not found` or similar.

1. **Check install method**:
   ```bash
   # Homebrew?
   brew list clawker 2>/dev/null && echo "installed via brew"
   # Binary in common locations?
   ls -la /usr/local/bin/clawker ~/.local/bin/clawker 2>/dev/null
   ```

2. **Check PATH**:
   ```bash
   echo $PATH | tr ':' '\n' | grep -E 'local|brew|go'
   ```
   Common missing paths:
   - Homebrew: `/opt/homebrew/bin` (Apple Silicon) or `/usr/local/bin` (Intel)
   - Install script: `~/.local/bin`
   - Go install: `~/go/bin`

3. **Shell profile not sourced**: If installed just now, the user needs to:
   ```bash
   source ~/.zshrc  # or ~/.bashrc
   ```
   Or open a new terminal.

4. **Wrong architecture**: On Apple Silicon, make sure the binary is arm64:
   ```bash
   file $(which clawker)
   ```

---

## Container can't reach a domain

User reports network errors, timeouts, or "connection refused" from inside a container.

1. **Is the firewall enabled?**
   ```bash
   clawker firewall status
   ```
   If the firewall is running, all egress is deny-by-default.

2. **Is the domain in the allowlist?**
   ```bash
   clawker firewall list
   ```
   Check if the target domain appears. Remember:
   - Only these are hardcoded: `api.anthropic.com`, `platform.claude.com`,
     `claude.ai`, `sentry.io`, `statsig.anthropic.com`, `statsig.com`
   - Everything else must be explicitly added

3. **Is it the right protocol?**
   - `add_domains` creates TLS (port 443) rules only
   - SSH (port 22) needs a `rules` entry with `proto: ssh`
   - Custom ports need a `rules` entry with `proto: tcp`

4. **Quick test with bypass**:
   ```bash
   clawker firewall bypass 5m --agent <agent-name>
   ```
   If it works with bypass, the issue is a missing firewall rule.

5. **Add the domain**:
   ```bash
   # Runtime (immediate, doesn't persist to config):
   clawker firewall add <domain>
   # Persistent (add to .clawker.yaml):
   ```
   ```yaml
   security:
     firewall:
       add_domains:
         - <domain>
   ```

6. **DNS resolution**: If the domain resolves to multiple IPs or uses CDN,
   clawker's CoreDNS handles this. But if the domain has non-standard DNS,
   check that CoreDNS is running:
   ```bash
   clawker firewall status
   ```

---

## Build failed

User reports errors during `clawker build` or first `clawker run` (which triggers a build).

1. **Identify which layer failed**: The build output shows which Dockerfile
   step failed. Map it to the config section:

   | Error occurs during | Config section | Context |
   |---|---|---|
   | Package installation (apt-get/apk) | `build.packages` | Root |
   | After FROM, before packages | `build.inject.after_from` | Root |
   | After packages | `build.inject.after_packages` | Root |
   | "Permission denied" as root | `build.instructions.root_run` | Root |
   | "Permission denied" as user | `build.instructions.user_run` | User |
   | npm/pip install | `build.instructions.user_run` | User |
   | COPY failed | `build.instructions.copy` | User |
   | Claude Code install | Internal (network issue?) | User |

2. **Package not found**: The package name may differ between Alpine and Debian:
   - Check which base image: `build.image` in `.clawker.yaml`
   - Alpine uses `apk` — package names like `build-base` not `build-essential`
   - Debian uses `apt-get` — package names like `build-essential` not `build-base`
   - Research the correct package name for the base image's OS

3. **Network error during build**: The build runs outside the firewall
   (it needs to pull packages). But if using a custom registry or proxy,
   ensure network access is available during build.

4. **COPY file not found**: `build.instructions.copy` paths are relative to
   the build context (project root). Verify the source file exists at the
   specified path.

5. **Rebuild from scratch**:
   ```bash
   clawker build --no-cache
   ```

---

## Credentials not working

User reports SSH, GPG, or git HTTPS failures inside the container.

### SSH not working

1. **Is SSH agent running on host?**
   ```bash
   ssh-add -l
   ```
   If "Could not open a connection to your authentication agent", start it:
   ```bash
   eval "$(ssh-agent -s)" && ssh-add
   ```

2. **Is SSH forwarding enabled?**
   ```yaml
   security:
     git_credentials:
       forward_ssh: true  # default
   ```

3. **Is the SSH host in the firewall?**
   ```yaml
   security:
     firewall:
       rules:
         - dst: github.com
           proto: ssh
           port: 22
           action: allow
   ```

### GPG not working

1. **Is GPG forwarding enabled?**
   ```yaml
   security:
     git_credentials:
       forward_gpg: true  # default
   ```

2. **Does the host have GPG keys?**
   ```bash
   gpg --list-keys
   ```

3. **GPG needs the public key exported**: The container needs `pubring.kbx`
   as a file (not directory). This is handled automatically by clawker's
   socket bridge, but if it's not working, check the bridge status.

### Git HTTPS not working

1. **Is HTTPS forwarding enabled?**
   ```yaml
   security:
     git_credentials:
       forward_https: true  # default
   ```

2. **Is the host proxy running?** Git HTTPS goes through clawker's host
   proxy daemon (port 18374).
   ```bash
   clawker firewall status  # shows host proxy status too
   ```

3. **Is the git host in the firewall?**
   ```yaml
   security:
     firewall:
       add_domains:
         - github.com
   ```

---

## MCP server won't start

User set up an MCP server but it's not working inside the container.

1. **Did post_init run?** Check for the marker file inside the container:
   ```bash
   # From inside the container:
   ls -la ~/.claude/post-initialized
   ```
   If the marker exists, post_init already ran. If the MCP package wasn't
   installed correctly, delete the marker and restart:
   ```bash
   rm ~/.claude/post-initialized
   ```
   Then restart the container.

2. **Is the npm package installed?**
   ```bash
   # Inside the container:
   npx -y @modelcontextprotocol/server-<name> --help
   ```

3. **Are firewall rules in place?** If the MCP calls external APIs, those
   domains must be in the firewall allowlist. See `mcp-recipes.md` for
   per-MCP requirements.

4. **Is the MCP configured in Claude Code?** The MCP server entry needs to
   be in the container's Claude Code settings. Check:
   ```bash
   cat ~/.claude/settings.json  # inside container
   ```

5. **Are environment variables set?** Many MCPs need API keys. Check that
   `agent.env` has the required variables and they're not empty.

---

## Config not taking effect

User changed `.clawker.yaml` but the change doesn't seem to apply.

1. **Config layering precedence**: A closer file wins over a farther one:
   ```
   ./.clawker.yaml              (CWD — highest priority)
   ./.clawker.local.yaml        (local override)
   ../.clawker.yaml             (parent directory)
   ...                          (walk up to project root)
   ~/.config/clawker/settings.yaml  (user-level — lowest priority)
   ```

2. **Check which file is active**:
   ```bash
   clawker settings edit  # shows merged config with provenance
   ```

3. **Build-time vs runtime**: Changes to `build.*` require a rebuild:
   ```bash
   clawker build --no-cache
   ```
   Changes to `agent.*` and `security.firewall.*` take effect on next
   container creation (no rebuild needed for firewall rule changes if
   using runtime `clawker firewall add`).

4. **Local override hiding changes**: Check if `.clawker.local.yaml` exists
   and overrides the field you changed.

---

## Container won't start

User reports the container fails to start or immediately exits.

1. **Check container logs**:
   ```bash
   clawker container logs <container-name>
   ```

2. **Check if the image exists**:
   ```bash
   clawker image list
   ```

3. **Port conflicts**: If the firewall or host proxy can't bind ports,
   check for conflicts:
   ```bash
   lsof -i :18374  # host proxy port
   ```

4. **Docker Desktop running?**
   ```bash
   docker info
   ```

5. **Volume permissions**: If config or history volumes have wrong ownership,
   container init may fail. Recreate volumes:
   ```bash
   clawker volume list
   clawker volume remove <volume-name>
   ```
