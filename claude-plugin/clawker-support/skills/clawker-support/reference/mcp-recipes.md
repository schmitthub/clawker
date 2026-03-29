# Setting Up MCP Servers in Clawker

This reference teaches you the methodology for wiring any MCP server into a
clawker container. Every MCP is different — research first, then apply the
framework below.

## Critical: MCP Discovery — Use Clawker Config, Not Host

When investigating what MCP servers a project uses, **look at the clawker
config**, not the host's Claude Code config files. Clawker manages its own
MCP setup independently — the container's MCP servers are registered via
`agent.post_init` in the project's clawker config, not in the host's Claude
Code config directory.

**Where to look for existing MCP declarations:**
Look for `claude mcp add` commands in `agent.post_init` across the project's
clawker config layers (local overrides, project config, user-level defaults).
Fetch `https://docs.clawker.dev/configuration` for the current schema to
understand all available agent config fields.

**Do NOT:**
- Search the host's Claude Code config for MCP server lists
- Assume the host's MCP servers should be replicated inside the container

The host and container are isolated environments. The user may have different
MCP servers on their host vs in their clawker containers, and that's by design.

**When the user asks about MCPs, ask clarifying questions:**
- "Are you asking about MCP servers inside your clawker container, or on your host?"
- "I see [these MCPs] declared in your clawker config. Are you adding a new one
  or modifying an existing one?"

## The Framework

Every MCP setup in clawker has three parts:

1. **Install dependencies** — at build-time (see SKILL.md Step 4 for the
   decision framework on where each dependency goes)
2. **Register with Claude Code** — via `claude mcp add` in `agent.post_init`
   (must be runtime — needs the `claude` CLI and initialized config volume)
3. **Network** — add firewall rules for any external endpoints the MCP calls

Only MCP registration belongs in `post_init`. Everything else — package
installs, runtimes, tools — belongs at build-time. See SKILL.md Step 4.

## How to Research an MCP

When a user asks about an MCP server, **always research it first.** Do not
assume install methods, flags, or syntax. Every MCP has its own setup
requirements, and these change over time.

1. **Fetch Claude Code's MCP documentation** — Get the current docs from
   `https://code.claude.com/docs/en/mcp` for the up-to-date `claude mcp add`
   syntax, flags, scope options, and transport types. **Never rely on memorized
   flag syntax — always fetch.**

2. **Find the MCP provider's documentation** — Search for its GitHub repo,
   npm/PyPI page, or official docs. Read the README thoroughly. Look for:
   - How to install it (package manager, binary download, etc.)
   - What runtime dependencies it needs
   - Transport type (stdio vs HTTP/SSE)
   - How the provider recommends registering it with Claude Code

3. **Identify ALL dependencies** — An MCP may need more than just its own
   package:
   - Runtime environments (Node.js, Python, etc.)
   - System libraries (C extensions, SSL, etc.)
   - CLI tools it shells out to

4. **Find external endpoints** — Read the MCP's docs or source code for:
   - API base URLs it calls (these need firewall rules)
   - Package registries it downloads from at runtime (if any)
   - Auth requirements (API keys, tokens)

5. **Check auth mechanism** — Does it need API keys, auth headers, OAuth
   tokens? Research the specific mechanism from the provider's docs.

## Wiring It Into Clawker Config

### Classify each dependency

After researching the MCP, you'll have a list of things to install. Classify
each one using SKILL.md Step 4's decision framework. Fetch the current config
schema from `https://docs.clawker.dev/configuration` to get the exact field
names and types for each section.

### Why registration must be in post_init (but installation should not)

`claude mcp add` writes to Claude Code's config. It requires:

1. **The `claude` CLI binary** — installed late in the Dockerfile (after user
   build instructions)
2. **The initialized config volume** — the entrypoint seeds the config on
   first boot. `claude mcp add` writes to this config, so it can only run
   after initialization.

That's why `claude mcp add` goes in `post_init`.

But **installing the MCP's dependencies** does NOT need the `claude` binary or
the config volume. Dependencies go at build-time so they're baked into the
image and never re-downloaded when a new container starts.

### Registration

Fetch the current `claude mcp add` syntax from
`https://code.claude.com/docs/en/mcp` — do not rely on memorized flags or
examples. Cross-reference with the MCP provider's own documentation for the
correct command and arguments.

**Scope:** Claude Code supports different scopes for MCP registration. Fetch
the docs to see the current options. **Default to the most local scope** unless
the user explicitly wants broader scope. Ask the user before recommending a
scope that would affect multiple projects — explain the inheritance implications.

The `claude mcp add` command in `agent.post_init` should go in the
**project-level** clawker config, not in user-level config. A project-specific
MCP declared at user level would be set up in every project's container.

### Environment Variables

If the MCP needs API keys or other secrets, fetch the current config schema
from `https://docs.clawker.dev/configuration` for the exact field names and
patterns for passing environment variables to containers. Prefer mechanisms
that keep secrets out of the config file (forwarding from host env, dotenv
files in `.gitignore`).

See `reference/known-issues.md` for current env-related caveats.

### Firewall Rules

**Every external endpoint the MCP calls must be in the firewall allowlist.**
The firewall is deny-by-default.

Fetch `https://docs.clawker.dev/configuration` for the current firewall config
syntax. Research the specific domains the MCP needs from its documentation.

**How to find what domains an MCP needs:**
- Read its docs for API endpoint information
- Look at source code for HTTP client base URLs
- If stuck: use the firewall bypass command temporarily and watch for blocked
  connection errors, then add those domains

## Common Mistake: Putting installations in post_init

`post_init` runs once per config volume, NOT once per image. A marker file on
the config volume prevents re-execution — so `post_init` will NOT re-run when
creating a new container if the same config volume is reused. It only re-runs
when the config volume is new or the marker is deleted. This means package
installs in `post_init` waste time on every fresh volume and require network
access at startup.

**Only MCP registration belongs in post_init.** All dependency installation
belongs at build-time. See SKILL.md Step 4 for the decision framework.

If you see an existing config with package installs in `post_init`, recommend
moving them to the appropriate build-time config section. The registration
line stays in `post_init`.

## Troubleshooting

1. **"MCP not found"** — Did post_init run? Check for the marker file inside
   the container. If the marker exists, post_init already ran on this config
   volume and won't re-run. To force re-execution (e.g., after changing
   `post_init`), either delete the marker file or remove the config volume
   so a fresh one is created. If the dependency itself is missing, it needs
   to be added at build-time and the image rebuilt.

2. **"Connection refused" or timeout** — The MCP's endpoint is probably blocked
   by the firewall. Research what domains it needs and add them.

3. **Auth errors** — Check that the required environment variables are set
   inside the container. Verify the config is passing them through correctly.

4. **MCP registered but not working** — The registration command in post_init
   may have failed silently. Try running it manually inside the container to
   see the error. Also verify the MCP's dependencies are actually installed.
