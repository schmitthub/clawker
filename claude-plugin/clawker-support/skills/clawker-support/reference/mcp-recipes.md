# Setting Up MCP Servers in Clawker

This reference teaches you the methodology for wiring any MCP server into a
clawker container. Every MCP is different — research first, then apply the
framework below.

## The Framework

Every MCP setup in clawker has three parts:

1. **Install dependencies** — at build-time (see SKILL.md Step 4 for the
   decision framework on where each dependency goes)
2. **Register with Claude Code** — via `claude mcp add` in `agent.post_init`
   (must be runtime — needs the `claude` CLI and initialized config volume)
3. **Network** — add firewall rules for any external endpoints the MCP calls

Only `claude mcp add` belongs in `post_init`. Everything else — package
installs, runtimes, tools — belongs at build-time. See SKILL.md Step 4.

## How to Research an MCP

When a user asks about an MCP server, **always research it first.** Do not
assume install methods — every MCP has its own setup requirements.

1. **Find the MCP's documentation** — Search for its GitHub repo, npm/PyPI
   page, or official docs. Read the README thoroughly. Look for:
   - How to install it (npm, pip, uv, cargo, binary download, etc.)
   - What runtime dependencies it needs (Node.js, Python, uv, etc.)
   - Transport type (stdio vs HTTP/SSE)
   - How to register it with Claude Code (`claude mcp add` invocation)

2. **Identify the transport** — This determines how you register it:
   - **stdio**: Runs as a subprocess. Use `claude mcp add -s <scope> <name> -- <command> <args>`
   - **HTTP/SSE**: Remote server. Use `claude mcp add -s <scope> -t http <name> <url>`

3. **Identify ALL dependencies** — An MCP may need more than just its own
   package. Examples:
   - A Python MCP might need `uv` or `pip` plus specific language servers
   - A Node.js MCP might need a specific Node version
   - An MCP might need system libraries (C extensions, SSL, etc.)
   - An MCP might need CLI tools it shells out to

4. **Find external endpoints** — Read the MCP's README or source code for:
   - API base URLs it calls (these need firewall rules)
   - Package registries it downloads from at runtime (if any)
   - Auth requirements (API keys → `agent.env` or `agent.from_env`)

5. **Check auth mechanism** — Does it need:
   - An API key in env vars? → `agent.env` or `agent.from_env`
   - An auth header? → `--header` flag on `claude mcp add`
   - OAuth or token file? → May need additional setup in post_init

## Wiring It Into Clawker Config

### Classify each dependency

After researching the MCP, you'll have a list of things to install. Classify
each one using SKILL.md Step 4's decision framework:

- **OS packages** (git, build-essential, language runtimes from apt/apk) →
  `build.packages`
- **System-level tools needing root** (adding repos, modifying `/etc`) →
  `build.instructions.root_run`
- **User-level tools** (npm packages, pip packages, cargo tools, uv, etc.) →
  `build.instructions.user_run`
- **MCP registration** (`claude mcp add`) →
  `agent.post_init`
- **API keys and env vars** →
  `agent.env` / `agent.from_env` / `agent.env_file`

### Why registration must be in post_init (but installation should not)

`claude mcp add` writes to Claude Code's config. It requires:

1. **The `claude` CLI binary** — installed late in the Dockerfile (after `user_run`)
2. **The initialized config volume** — the entrypoint seeds `~/.claude` from
   `~/.claude-init/` on first boot. `claude mcp add` writes to this config, so
   it can only run after initialization.

That's why `claude mcp add` goes in `post_init`.

But **installing the MCP's dependencies** does NOT need the `claude` binary or
the config volume. Dependencies go at build-time so they're baked into the
image and never re-downloaded when a new container starts.

### Registration: `claude mcp add`

Follow the MCP's own documentation for the exact `claude mcp add` invocation.
The MCP's README or Claude Code docs will tell you the correct command and
arguments. Put it in `agent.post_init`:

```yaml
agent:
  post_init: |
    claude mcp add -s <scope> <name> -- <command> <args>
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
      - api.example.com
```

For non-HTTPS endpoints:
```yaml
security:
  firewall:
    rules:
      - dst: example.com
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

**Only `claude mcp add` belongs in post_init.** All dependency installation
belongs at build-time. See SKILL.md Step 4 for the full decision framework,
priority order, and anti-pattern table.

If you see an existing config with package installs in `post_init`, recommend
moving them to the appropriate build-time config section. The `claude mcp add`
line stays in `post_init`.

## Debugging MCP Issues

1. **"MCP not found"** — Did post_init run? Check the marker:
   ```bash
   # Inside the container:
   ls ~/.claude/post-initialized
   ```
   If it exists but the MCP wasn't registered, delete the marker and restart.

2. **"Connection refused" or timeout** — The MCP's endpoint is probably blocked
   by the firewall. Check what domains it needs and add them.

3. **"Unauthorized" or auth errors** — Check that the API key env var is set:
   ```bash
   # Inside the container:
   echo $FOO_API_KEY
   ```
   If empty, verify `agent.env` or `agent.from_env` in the config.

4. **MCP registered but not working** — The `claude mcp add` command in
   post_init may have failed silently. Try running it manually inside the
   container to see the error. Also verify the MCP's dependencies are
   actually installed — check using the MCP's own documentation for how
   to verify its installation.
