# Setting Up MCP Servers in Clawker

This reference teaches you the pattern for wiring any MCP server into a
clawker container. Don't memorize specific MCPs — research them, then
apply this pattern.

## The Pattern

Every MCP setup in clawker has three parts:

1. **Install** — via `agent.post_init` (runs once per container)
2. **Configure** — register the MCP with Claude Code via `claude mcp add` in post_init
3. **Network** — add firewall rules for any external endpoints the MCP calls

## How to Research an MCP

When a user asks about an MCP server you don't know:

1. **Find the package** — Search for the MCP server's GitHub repo or npm/PyPI page.
   Look for: package name, install command, transport type (stdio vs HTTP).

2. **Identify the transport** — This determines how you register it:
   - **stdio**: Runs as a subprocess. Use `claude mcp add -s <scope> <name> -- <command> <args>`
   - **HTTP/SSE**: Remote server. Use `claude mcp add -s <scope> -t http <name> <url>`

3. **Find external endpoints** — Read the MCP's README or source code for:
   - API base URLs it calls (these need firewall rules)
   - Auth requirements (API keys → `agent.env` or `agent.from_env`)

4. **Check auth mechanism** — Does it need:
   - An API key in env vars? → `agent.env` or `agent.from_env`
   - An auth header? → `--header` flag on `claude mcp add`
   - OAuth or token file? → May need additional setup in post_init

## Wiring It Into Clawker Config

### Why post_init (not build-time)

MCP registration uses `claude mcp add`, which requires:

1. **The `claude` CLI binary** — not available during early build steps
   (`root_run`, `user_run` before Claude install)
2. **Claude Code to be initialized** — the entrypoint script seeds config
   from `~/.claude-init/` into the `~/.claude` config volume on first boot.
   `claude mcp add` writes to this config, so it can only run after
   initialization completes.

`post_init` runs after the entrypoint init sequence — the binary is
available, Claude is initialized, and the config volume is mounted and
seeded. That's why all MCP setup goes here.

### post_init: Install + Register

All MCP setup goes in `agent.post_init`. This runs once per container
(marker file prevents re-runs).

**stdio MCP example** (runs as a local subprocess):
```yaml
agent:
  post_init: |
    npm install -g @some-org/mcp-server-foo
    claude mcp add -s local foo -- npx -y @some-org/mcp-server-foo
```

**HTTP MCP example** (connects to a remote server):
```yaml
agent:
  post_init: |
    claude mcp add -s user -t http foo https://mcp.foo.com/mcp
```

**HTTP MCP with auth header**:
```yaml
agent:
  from_env:
    - FOO_API_KEY
  post_init: |
    claude mcp add -s user --header "Authorization: Bearer $FOO_API_KEY" -t http foo https://mcp.foo.com/mcp
```

**Python-based MCP** (uses uvx instead of npx):
```yaml
agent:
  post_init: |
    claude mcp add -s local foo -- uvx --from some-package foo-server start-mcp-server
```

### Scope: `-s local` vs `-s user`

- `-s local` — Available only in the current project. Good for project-specific MCPs.
- `-s user` — Available across all projects in this container. Good for general-purpose MCPs.

### Environment Variables

If the MCP needs API keys, pass them through:

```yaml
agent:
  env:
    FOO_API_KEY: "hardcoded-value"        # Static value (stored in config)
  from_env:
    - FOO_API_KEY                          # Forwarded from host env at container creation
  env_file:
    - .env                                 # Load from a .env file
```

`from_env` and `env_file` are preferred over `agent.env` — they keep
secrets out of the config file. `from_env` pulls from the host's
environment at container creation time. `env_file` loads from a dotenv
file (which should be in `.gitignore`).

See `reference/known-issues.md` for current `env_file` caveats.

### Firewall Rules

**Every external endpoint the MCP calls must be in the firewall allowlist.**
The firewall is deny-by-default.

For HTTPS endpoints (port 443):
```yaml
security:
  firewall:
    add_domains:
      - mcp.foo.com
      - api.foo.com
```

For non-HTTPS endpoints:
```yaml
security:
  firewall:
    rules:
      - dst: foo.com
        proto: tcp
        port: 8080
        action: allow
```

**How to find what domains an MCP needs:**
- Read its README for API endpoint docs
- Look at source code for HTTP client base URLs
- If stuck: temporarily `clawker firewall bypass 5m` and watch for blocked
  connection errors, then add those domains

## Debugging MCP Issues

1. **"MCP not found"** — Did post_init run? Check the marker:
   ```bash
   # Inside the container:
   ls ~/.claude/post-initialized
   ```
   If it exists but the MCP wasn't installed, delete the marker and restart.

2. **"Connection refused" or timeout** — The MCP's endpoint is probably blocked
   by the firewall. Check what domains it needs and add them.

3. **"Unauthorized" or auth errors** — Check that the API key env var is set:
   ```bash
   # Inside the container:
   echo $FOO_API_KEY
   ```
   If empty, verify `agent.env` or `agent.from_env` in the config.

4. **MCP installed but not registered with Claude** — The `claude mcp add`
   command in post_init may have failed silently. Try running it manually
   inside the container to see the error.
