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
| Monitoring stack (OTel/OpenSearch/Prometheus, workspace, empty indices) | `reference/monitoring.md` | Troubleshooting |
| Securing git/VCS egress, can the agent push to/leak via other repos, credential-exfil hardening | `reference/firewall-security.md` | Full reference |
| `/login` prompts in containers, how in-container auth and the config volume work | `reference/claude-code.md` | Authentication › Troubleshooting |
| Didn't see "What's new" / release notes after upgrade | This file | No release notes after upgrade |
| Control plane down or unreachable | This file | Control plane down or unhealthy |
| `Token used before issued` / token fetch timeout on container start | This file | Control plane down or unhealthy |
| Agent missing from CP registry | This file | Agent appears in clawker ps but missing from CP |

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
   Check if the target domain appears. Only the selected harness's egress
   floor (the domains that harness needs to function, declared by its bundle)
   is present by default — everything else must be explicitly allowed.

   **Exact host vs subdomain:** an allowed bare domain (`example.com`, or an
   `add_domains` entry) matches **only that exact host** — a blocked
   `api.example.com` is a *separate* host even when `example.com` is allowed.
   To cover subdomains, use the leading-dot wildcard form `.example.com` (which
   also covers the apex). This is the #1 "but I allowed it" surprise.

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
   `list`, `add`, `remove`, `refresh`, `reload`, `up`, `down`, `rotate-ca`). Passing
   `--agent` to a global command will error. When in doubt, fetch
   `https://docs.clawker.dev/cli-reference/clawker_firewall` for current
   command signatures.

5. **Add the destination — scope it as narrowly as the work allows.** Either at
   runtime via `clawker firewall add` (immediate, doesn't persist to the config
   file) or persistently in the project's clawker config — then `clawker firewall
   refresh` to live-apply the YAML edit without restarting the container. For
   http/https,
   **prefer a path-scoped rule over a bare domain allow** when the agent only
   needs specific paths:
   ```bash
   clawker firewall add <host> --path <prefix> --action allow
   ```
   The allow path flips the domain to allowlist mode — only that path prefix is
   reachable, everything else on the host is denied. In YAML the equivalent is
   `path_default: deny` plus `path_rules`. A whole-domain allow over a
   credential-bearing host (GitHub, package registries, cloud APIs) is an exfil
   surface — see `reference/firewall-security.md`. Reserve bare-domain allows
   for hosts that genuinely need every path. SSH/TCP/UDP carry no path metadata —
   scope those by `--proto`/`--port`. Fetch the current config schema for exact
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

6. **Control plane down**: Container init is dispatched by the clawker
   control plane (CP) over an mTLS Session to the per-container clawkerd
   daemon. If CP is unreachable, `clawkerd` boots the listener but never
   receives the readiness signal from CP — the container hangs without
   spawning the user CMD and the HEALTHCHECK never goes green.
   ```bash
   clawker controlplane status
   clawker controlplane up         # idempotent — brings CP up if needed
   ```

---

## No release notes after upgrade

User upgraded clawker but never saw the one-time "What's new" note, or wonders
where to find what changed.

This is expected behavior, not a bug. After an upgrade, a one-time note shows on
the first *interactive* run, listing what changed since the previous version,
then never repeats for that upgrade. The changelog is fetched over the network.

The note is intentionally suppressed in any of these cases:

- **Not an interactive terminal** — for example a non-interactive shell.
  (Redirecting or piping only the command's normal output, while the terminal
  stays attached, does not suppress the note — it rides clawker's status
  messages, not the command's data output.)
- **`CI` is set** — treated as a non-interactive environment.
- **`CLAWKER_NO_NOTIFIER` is set** (any non-empty value) — the user opted
  out of both the new-version update notifier and the "What's new" note.
- The running binary is a dev build (no injected version).

There is also a non-suppression case: the first interactive run after moving
onto a clawker version that has this feature records the current version as a
baseline and shows nothing — the note appears on the next upgrade after that. A
user who upgraded *into* the feature for the first time correctly sees nothing.

If the user expected the note but it never appeared, check those suppression
conditions — most often output was not a terminal or `CLAWKER_NO_NOTIFIER`/
`CI` was set in their environment. To see the full curated changelog regardless,
point them at the `CHANGELOG.md` at the root of the clawker repository.

---

## Control plane down or unhealthy

User reports `clawker firewall *` or container commands hang, time out, or
report `connection refused` against the CP.

1. **Check CP health**:
   ```bash
   clawker controlplane status
   ```
   Reports container running state, `/healthz` result,
   and (if reachable) firewall subsystem state.

2. **Bring CP up explicitly**:
   ```bash
   clawker controlplane up
   ```
   Idempotent — no-op if CP is already healthy.

3. **CP container exists but unhealthy**: Inspect Docker logs directly.
   CP panic traces and Ory subprocess output land here, NOT in clawker's
   rotating logs.
   ```bash
   docker logs clawker-controlplane
   ```
   A CP that exits immediately with `firewall.enable` set may be failing
   its firewall bringup startup gate (the boot fails by design rather
   than running unenforced) — the log shows `firewall bringup:`
   with the underlying Envoy/CoreDNS error, and the rotating
   control-plane log records a structured `event=firewall_bringup_failed`
   line with the same cause. Fix the cause, or set
   `firewall.enable: false` in settings.yaml to run unprotected.

4. **Auth material out of sync** (CP reports TLS or OAuth2 errors): rotate
   the CLI's auth material. The CLI is the root of trust — CP loads
   bind-mounted certs and signing keys from `~/.config/clawker/`.
   ```bash
   clawker auth rotate
   ```

5. **CP clock-sync error on start** (Docker Desktop). The startup error
   contains `cp clock sync deadline exceeded` (wrapped by the caller, e.g.
   `bootstrapping services: ensuring control plane is running: ...`), often
   after the host slept/woke. The Docker Desktop LinuxKit VM clock (where
   CP runs) has drifted behind the host clock. Rather than skew-correcting
   the OAuth2 assertion's `iat`, the CLI/bootstrap **waits** for the CP
   clock to catch up to the host and **fails fast** with this error if it
   doesn't converge within the timeout — avoiding the underlying Hydra
   `Token used before issued` 500 (a future-dated assertion). Retry first
   (Docker usually re-syncs the VM clock on its own); if it persists,
   restart Docker Desktop to force the resync, then retry the `clawker`
   command.

6. **Recovery — full restart**:
   ```bash
   clawker controlplane down
   clawker controlplane up
   ```
   With `firewall.enable` set in settings.yaml (the default),
   `controlplane up` also brings the Envoy + CoreDNS firewall stack back
   up — no separate `clawker firewall up` is needed.

---

## Agent appears in `clawker ps` but missing from CP

User reports a running agent container but `clawker controlplane agents`
doesn't list it.

This means the agent's clawkerd hasn't completed the Register handshake
with CP. Either CP wasn't up when the container started, or the agent's
mTLS bootstrap material is invalid.

1. **List registered agents**:
   ```bash
   clawker controlplane agents
   ```

2. **Inspect the agent's clawkerd logs**:
   ```bash
   docker logs <container-name>
   ```
   Look for `event=register_rpc_failed`, `event=register_dial_failed`, or TLS handshake errors.

3. **Rotate auth and restart the agent**:
   ```bash
   clawker auth rotate
   clawker container restart --agent <agent-name>
   ```
