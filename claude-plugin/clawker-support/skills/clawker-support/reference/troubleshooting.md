# Clawker Troubleshooting

Entry point for diagnosing clawker issues. Start here, then follow the
routing below to the appropriate domain reference if applicable.

## Domain-specific troubleshooting

Some issue domains have their own troubleshooting sections in dedicated
reference files. Check these first if the issue matches:

| Issue domain | Reference | Section |
| --- | --- | --- |
| Build failures, config not taking effect | `reference/project-config.md` | Troubleshooting |
| MCP server setup and debugging | `reference/mcp-recipes.md` | Troubleshooting |
| Settings not taking effect | `reference/settings.md` | Troubleshooting |
| Disk space, build cache, Docker cleanup | `reference/docker-hygiene.md` | Full reference |

## Global issues

The following diagnostics cover cross-cutting concerns that don't belong
to a single domain.

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

3. **Shell profile not sourced**: If installed just now, the user needs to
   open a new terminal or source their shell profile.

4. **Wrong architecture**: On Apple Silicon, make sure the binary is arm64:
   ```bash
   file $(which clawker)
   ```

---

## Container can't reach a domain

User reports network errors, timeouts, or "connection refused" from inside
a container.

1. **Is the firewall enabled?**
   ```bash
   clawker firewall status
   ```
   If the firewall is running, all egress is deny-by-default.

2. **Is the domain in the allowlist?**
   ```bash
   clawker firewall list
   ```
   Check if the target domain appears. Only a small subset of Claude Code
   required domains are hardcoded — everything else must be explicitly allowed.

3. **Is it the right protocol?** Fetch `https://docs.clawker.dev/configuration`
   for the current firewall config syntax. Different protocols and ports require
   different config field types.

4. **Quick test with bypass**: Temporarily bypass the firewall for a specific
   agent container:
   ```bash
   clawker firewall bypass <duration> --agent <agent-name>
   ```
   If the connection works during bypass, the issue is a missing firewall rule.

   **Important: firewall command scoping.** Some firewall commands are
   per-container and require `--agent` (`bypass`, `enable`, `disable`).
   Others are global infrastructure and do NOT accept `--agent` (`status`,
   `list`, `add`, `remove`, `reload`, `up`, `down`, `rotate-ca`). Passing
   `--agent` to a global command will error. When in doubt, fetch
   `https://docs.clawker.dev/cli-reference/clawker_firewall` for current
   command signatures.

5. **Add the domain**: Either at runtime via `clawker firewall add` (immediate
   but doesn't persist to config file) or persistently by adding it to the
   project's clawker config. Fetch the current config schema for the exact
   syntax.

6. **DNS resolution**: If the domain resolves to multiple IPs or uses CDN,
   clawker's CoreDNS handles this. Check `clawker firewall status` if DNS
   issues are suspected.

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

4. **SSH connecting to the wrong host?** TCP/SSH rules capture **all** traffic
   on the configured port and redirect it to the whitelisted domain. Unlike
   TLS (which has SNI) and HTTP (which has the Host header), raw TCP and SSH
   have no protocol-level domain metadata. Resolving domains to IPs for
   per-IP routing rules is not viable — IPs change frequently for large
   services (CDN rotation, load balancer failover). Instead, eBPF creates
   one routing rule per port and Envoy resolves the domain at connection time.
   If the user has a `proto: ssh` rule for `github.com` on port 22, every
   port 22 connection goes to GitHub regardless of the intended destination.
   If multiple SSH rules exist on the same port, the first rule in the config
   wins (eBPF first-match). The user should verify with `ssh -T git@target`
   to confirm which host they reached. Only one domain per TCP/SSH port
   (tracked: github.com/schmitthub/clawker/issues/235, deferred to control plane).

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
   check for conflicts.

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
