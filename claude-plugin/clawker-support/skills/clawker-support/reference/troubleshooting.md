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
   Check if the target domain appears. Only a small set of Anthropic domains
   are hardcoded — everything else must be explicitly allowed.

3. **Is it the right protocol?** Fetch `https://docs.clawker.dev/configuration`
   for the current firewall config syntax. Different protocols and ports require
   different config field types.

4. **Quick test with bypass**: Use `clawker firewall bypass` temporarily. If
   the connection works during bypass, the issue is a missing firewall rule.

5. **Add the domain**: Either at runtime via `clawker firewall add` (immediate
   but doesn't persist to config file) or persistently by adding it to the
   project's `.clawker.yaml`. Fetch the current config schema for the exact
   syntax.

6. **DNS resolution**: If the domain resolves to multiple IPs or uses CDN,
   clawker's CoreDNS handles this. Check `clawker firewall status` if DNS
   issues are suspected.

---

## Build failed

User reports errors during `clawker build` or first `clawker run` (which triggers a build).

1. **Check for user-level config conflicts FIRST**: This is the #1 hidden cause
   of build failures. User-level config (`~/.config/clawker/clawker.yaml`) is
   merged into every project. If user-level config has build-related fields
   written for a different distro than the project's base image, the build will
   fail with confusing errors.
   ```bash
   cat ~/.config/clawker/clawker.yaml
   ```
   Look for:
   - Distro-specific package names that don't match the project's base image
   - Package manager commands targeting the wrong distro
   - Shell commands assuming tools or behaviors not present on the base image
   - Any build config at user level that isn't universally distro-agnostic

   **If found**: Move the offending entries to the project-level `.clawker.yaml`
   where they belong, or remove them from user-level config entirely.

2. **Identify which layer failed**: The build output shows which Dockerfile
   step failed. Read the Dockerfile template (`reference/Dockerfile.tmpl`) to
   map the failing step to the config section that produced it. Look at
   execution order and root vs user context.

3. **Package not found**: Different base images use different package managers
   with different package names. Check the project's base image, then research
   the correct package name for that distro — do not guess.

4. **Network error during build**: The build runs outside the firewall
   (it needs to pull packages). But if using a custom registry or proxy,
   ensure network access is available during build.

5. **COPY file not found**: `build.instructions.copy` paths are relative to
   the build context (project root). Verify the source file exists at the
   specified path.

6. **Rebuild from scratch**:
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
   If the agent isn't running, the user needs to start it and add their keys.

2. **Is SSH forwarding enabled in config?** Fetch `https://docs.clawker.dev/configuration`
   and check the current credential forwarding fields. Verify the relevant
   setting is enabled in the project's clawker config.

3. **Is the SSH host in the firewall?** SSH requires protocol-specific firewall
   rules (not just domain allowlisting). Fetch the config schema for the
   correct syntax.

### GPG not working

1. **Is GPG forwarding enabled in config?** Fetch the current config schema and
   check the credential forwarding fields.

2. **Does the host have GPG keys?**
   ```bash
   gpg --list-keys
   ```

3. **GPG key availability**: The container needs the public key available.
   This is handled automatically by clawker's socket bridge, but if it's not
   working, check the bridge status.

### Git HTTPS not working

1. **Is HTTPS forwarding enabled in config?** Fetch the current config schema
   and check the credential forwarding fields.

2. **Is the host proxy running?** Git HTTPS goes through clawker's host proxy.
   ```bash
   clawker firewall status
   ```

3. **Is the git host in the firewall?** The domain needs to be in the firewall
   allowlist. Fetch the config schema for the correct syntax to add it.

---

## MCP server won't start

User set up an MCP server but it's not working inside the container.

1. **Did post_init run?** Check for the post-init marker file inside the
   container. If the marker exists but the MCP wasn't registered, delete the
   marker and restart the container to re-run post_init. If the MCP's
   dependencies are missing, they need to be added at build-time and the
   image rebuilt — see `mcp-recipes.md`.

2. **Are the MCP's dependencies installed?** Check using the MCP provider's
   own documentation for how to verify its installation. If missing, add
   them at build-time (see SKILL.md Step 4) and rebuild.

3. **Are firewall rules in place?** If the MCP calls external APIs, those
   domains must be in the firewall allowlist. Research what domains the MCP
   needs from its documentation.

4. **Is the MCP registered in Claude Code?** Check the container's Claude
   Code config to verify the MCP entry exists.

5. **Are environment variables set?** Many MCPs need API keys. Check that
   the required variables are set inside the container and not empty.

---

## Config not taking effect

User changed `.clawker.yaml` but the change doesn't seem to apply.

1. **Config layering precedence**: Clawker uses walk-up file discovery —
   closer files win over farther ones. Local overrides win over project
   config, which wins over parent dirs, which win over user-level defaults.
   Fetch `https://docs.clawker.dev/configuration` for the current merge
   behavior and precedence details.

2. **Check which file is active**: Use `clawker settings edit` or
   `clawker project edit` to see the merged config with provenance
   (which file each value comes from).

3. **Build-time vs runtime**: Build-related config changes require a rebuild
   (`clawker build --no-cache`). Agent and firewall config changes take
   effect on next container creation. Fetch the current schema to check
   which fields are build-time vs runtime.

4. **Local override hiding changes**: Check if a local override file exists
   and shadows the field you changed.

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
