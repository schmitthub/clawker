# MCP Server Recipes for Clawker

Tested recipes for setting up MCP servers in clawker containers. Each recipe
includes the post_init script and required firewall rules.

## Contents

- [GitHub MCP](#github-mcp)
- [Filesystem MCP](#filesystem-mcp)
- [Fetch MCP](#fetch-mcp)
- [Memory MCP](#memory-mcp)
- [Git MCP](#git-mcp)
- [Brave Search MCP](#brave-search-mcp)
- [PostgreSQL MCP](#postgresql-mcp)
- [Sentry MCP](#sentry-mcp)
- [Custom MCP Template](#custom-mcp-template)

---

## GitHub MCP

**Package**: `@modelcontextprotocol/server-github`
**Endpoints**: `api.github.com` (HTTPS)

```yaml
agent:
  post_init: |
    npm install -g @modelcontextprotocol/server-github
  env:
    GITHUB_PERSONAL_ACCESS_TOKEN: "ghp_your_token_here"
security:
  firewall:
    add_domains:
      - api.github.com
```

The Claude Code config (`.claude/settings.json` inside the container) needs the
MCP server entry. Add it via post_init or include it in the agent's Claude config:

```json
{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_PERSONAL_ACCESS_TOKEN}"
      }
    }
  }
}
```

If the user also needs to clone repos or push over SSH, add:
```yaml
security:
  firewall:
    add_domains:
      - api.github.com
      - github.com
    rules:
      - dst: github.com
        proto: ssh
        port: 22
        action: allow
```

---

## Filesystem MCP

**Package**: `@modelcontextprotocol/server-filesystem`
**Endpoints**: None (local filesystem only)

```yaml
agent:
  post_init: |
    npm install -g @modelcontextprotocol/server-filesystem
```

No firewall rules needed — this MCP operates entirely on the local filesystem.

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
    }
  }
}
```

---

## Fetch MCP

**Package**: `@modelcontextprotocol/server-fetch`
**Endpoints**: Any URL the agent fetches (user-dependent)

```yaml
agent:
  post_init: |
    npm install -g @modelcontextprotocol/server-fetch
security:
  firewall:
    add_domains:
      # Add the specific domains the agent needs to fetch from.
      # DO NOT add a wildcard — be explicit about what's allowed.
      - example.com
      - api.example.com
```

**Warning**: The Fetch MCP can reach any domain the firewall allows. Only add
domains the agent actually needs. Adding broad access defeats the purpose of
the deny-by-default firewall.

---

## Memory MCP

**Package**: `@modelcontextprotocol/server-memory`
**Endpoints**: None (local storage)

```yaml
agent:
  post_init: |
    npm install -g @modelcontextprotocol/server-memory
```

No firewall rules needed. Memory is stored locally in the container.

---

## Git MCP

**Package**: `@modelcontextprotocol/server-git`
**Endpoints**: None for local repos; remote endpoints for push/pull

```yaml
agent:
  post_init: |
    npm install -g @modelcontextprotocol/server-git
```

For remote operations, add the git host to the firewall:
```yaml
security:
  firewall:
    add_domains:
      - github.com
    rules:
      - dst: github.com
        proto: ssh
        port: 22
        action: allow
```

---

## Brave Search MCP

**Package**: `@modelcontextprotocol/server-brave-search`
**Endpoints**: `api.search.brave.com` (HTTPS)

```yaml
agent:
  post_init: |
    npm install -g @modelcontextprotocol/server-brave-search
  env:
    BRAVE_API_KEY: "your_brave_api_key"
security:
  firewall:
    add_domains:
      - api.search.brave.com
```

---

## PostgreSQL MCP

**Package**: `@modelcontextprotocol/server-postgres`
**Endpoints**: Database host (often via host proxy for local DBs)

For a **local** PostgreSQL running on the host machine:

```yaml
agent:
  post_init: |
    npm install -g @modelcontextprotocol/server-postgres
  env:
    # The host proxy forwards connections from the container to the host.
    # Use the host proxy address, not localhost.
    POSTGRES_URL: "postgresql://user:pass@host.docker.internal:5432/dbname"
```

No firewall rules needed for local databases accessed via Docker networking.
The host proxy handles container-to-host communication.

For a **remote** PostgreSQL:
```yaml
security:
  firewall:
    rules:
      - dst: your-db-host.example.com
        proto: tcp
        port: 5432
        action: allow
```

---

## Sentry MCP

**Package**: `@sentry/mcp-server`
**Endpoints**: `sentry.io` (HTTPS, already in hardcoded allowlist)

```yaml
agent:
  post_init: |
    npm install -g @sentry/mcp-server
  env:
    SENTRY_AUTH_TOKEN: "your_sentry_token"
```

`sentry.io` is already in the hardcoded firewall allowlist — no additional
firewall rules needed.

---

## Custom MCP Template

For any MCP server not listed above, follow this pattern:

```yaml
agent:
  post_init: |
    # Install the MCP server package
    npm install -g <package-name>
    # Or for Python-based MCPs:
    # pip install <package-name>
  env:
    # Add any required API keys or config
    MCP_API_KEY: "your_key"
security:
  firewall:
    add_domains:
      # Add HTTPS endpoints the MCP server calls
      - api.service.com
    rules:
      # Add non-HTTPS endpoints if needed
      - dst: service.com
        proto: tcp
        port: 8080
        action: allow
```

**How to figure out what domains an MCP needs**:
1. Read the MCP server's README for API endpoint documentation
2. Look at the source code for HTTP client calls or base URLs
3. If unsure, temporarily enable `clawker firewall bypass` and watch the logs
   for blocked connections, then add those domains
