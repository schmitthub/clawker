# Setting Up MCP Servers in Clawker

This reference teaches you the pattern for wiring any MCP server into a
clawker container. Don't memorize specific MCPs — research them, then
apply this pattern.

## The Pattern

Every MCP setup in clawker has three parts:

1. **Install the MCP package** — at build-time via `build.instructions.user_run`
   (baked into the image, never re-downloaded)
2. **Register with Claude Code** — via `claude mcp add` in `agent.post_init`
   (must be runtime — needs the `claude` CLI and initialized config volume)
3. **Network** — add firewall rules for any external endpoints the MCP calls

**Important:** Only `claude mcp add` must be in `post_init`. Package
installation (`npm install -g`, `pip install`, etc.) belongs at build-time.
See "Common Mistake" section below.

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

### Why registration must be in post_init (but installation should not)

`claude mcp add` writes to Claude Code's config. It requires:

1. **The `claude` CLI binary** — installed late in the Dockerfile (after `user_run`)
2. **The initialized config volume** — the entrypoint seeds `~/.claude` from
   `~/.claude-init/` on first boot. `claude mcp add` writes to this config, so
   it can only run after initialization.

That's why `claude mcp add` goes in `post_init`.

But **installing the MCP package itself** (`npm install -g`, `pip install`, etc.)
does NOT need the `claude` binary or the config volume. It should go in
`build.instructions.user_run` so it's baked into the image layer and never
re-downloaded when a new container starts.

### Build-time: Install the MCP package

Install the npm/pip package at build-time so it's baked into the image:

**Node.js MCP server:**
```yaml
build:
  instructions:
    user_run:
      - "npm install -g @some-org/mcp-server-foo"
```

**Python MCP server** (if pre-installing rather than relying on uvx):
```yaml
build:
  instructions:
    user_run:
      - "pip install some-mcp-package"
```

Pre-installing at build-time avoids download delays at container startup and
avoids needing npm/PyPI registry access at runtime (which would require
additional firewall rules).

### Runtime: Register with Claude Code

Registration goes in `agent.post_init`. This runs once per container
(marker file prevents re-runs).

**stdio MCP** (runs as a local subprocess):
```yaml
agent:
  post_init: |
    claude mcp add -s local foo -- npx -y @some-org/mcp-server-foo
```

**HTTP MCP** (connects to a remote server):
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

**Python MCP via uvx** (uvx downloads on demand; pre-install via `user_run` for faster startup):
```yaml
agent:
  post_init: |
    claude mcp add -s local foo -- uvx --from some-package foo-server start-mcp-server
```

### Complete example: stdio MCP with build-time install

Combining both parts for a typical stdio MCP server:

```yaml
build:
  instructions:
    user_run:
      - "npm install -g @some-org/mcp-server-foo"

agent:
  post_init: |
    claude mcp add -s local foo -- npx -y @some-org/mcp-server-foo

security:
  firewall:
    add_domains:
      - api.foo.com
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

## Common Mistake: Putting installations in post_init

`post_init` runs once per container, NOT once per image. Every command in
`post_init` re-executes when a new container is created from the same image.
This wastes time and requires network access at startup.

**Only `claude mcp add` belongs in post_init.** Package installations
(`npm install -g`, `pip install`, `apt-get install`, etc.) belong at
build-time. See SKILL.md Step 4 for the full decision framework, priority
order, and anti-pattern table.

If you see an existing config with `npm install -g` in `post_init`, recommend
moving it to `build.instructions.user_run`. The `claude mcp add` line stays
in `post_init`.

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
