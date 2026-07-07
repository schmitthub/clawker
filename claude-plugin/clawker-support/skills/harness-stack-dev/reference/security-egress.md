# Egress Floor Design — Security Reference for Bundle Authors

A bundle's `egress:` list is the firewall floor every container running
that harness receives, composed (floor first) with the project's
`security.firewall` rules. The floor is a security commitment: whatever you
allow here, **every** user of the harness allows. Design it adversarially —
assume the agent inside the container is prompt-injected and looking for an
exfiltration channel.

## Rule 1: minimal floor — runtime needs only

The floor covers what the harness needs to **function at runtime**:
inference API, auth/token exchange, and any registries its runtime
tooling genuinely hits. It does NOT cover:

- **Install-time domains.** `docker build` runs on the host daemon's
  network, outside the agent's network namespace — the installer's
  download hosts never touch the firewall. (The codex floor deliberately
  omits the npm registry: its install is build-time and the installed CLI
  is a self-contained binary. The claude floor *includes*
  `registry.npmjs.org` — but because Claude Code's runtime hooks install
  npm packages, a genuine runtime need.)
- **Docker registry domains.** Image pulls are host-daemon operations.
- **Project-specific domains.** Those belong in the project's
  `security.firewall`, chosen by the user, not imposed by the bundle.

For every entry, be able to answer: *what runtime operation of the harness
breaks without this?* If the answer references the build, delete the entry.

## Rule 2: widen from observed traffic, never preemptively

Ship the floor you have **observed** the harness need (live traffic, or
vendor docs for flows you cannot yet exercise — and say which, in a
comment). When a user reports a blocked flow, the block event names the
exact host and path; widen to exactly that. Never add "it will probably
also need" entries. The shipped codex manifest annotates provenance per
rule (`observed live` vs `from vendor docs, not yet observed live`) —
follow that practice.

## Rule 3: path-scope hosts that serve UGC

A domain that hosts BOTH the harness backend AND publicly readable
user-generated content is an exfiltration sink and an injection source if
allowed whole. Two shipped patterns, verbatim:

**Allowlist mode** (codex — `chatgpt.com` hosts the codex backend AND
shared conversations): allow only the backend prefixes; an `allow` path
rule with no `path_default` flips the host to allowlist mode (every
unlisted path denied):

```yaml
  # chatgpt.com also hosts publicly readable UGC (shared conversations) — an
  # exfiltration sink if the whole domain is open. Both rules are therefore
  # path-scoped to the codex backend prefix; the allow rule with no
  # path_default puts the host in allowlist mode (every other path denied).
  # Widen only from observed blocked traffic, never preemptively.
  - dst: chatgpt.com        # ChatGPT-account backend
    path_rules:
      - path: /backend-api/codex/          # inference + analytics-events (observed live)
        action: allow
      - path: /backend-api/wham/usage      # usage/rate-limit check (observed live)
        action: allow
      - path: /backend-api/ps/             # mcp + plugins/installed lookups (observed live)
        action: allow
      - path: /backend-api/plugins/        # featured/curated plugin directory (observed live)
        action: allow
      - path: /backend-api/connectors/directory/  # connector directory list (observed live)
        action: allow
```

**Denylist mode** (claude — `.claude.ai` serves OAuth AND Anthropic-hosted
UGC): the host allow is required for login, so deny the documented UGC
surfaces instead; deny path rules with no `path_default` leave everything
else open:

```yaml
  # .claude.ai serves both OAuth and Anthropic-hosted UGC. The host allow is
  # required for login; the deny path rules scope out documented UGC surfaces
  # so an injected prompt can't pivot the agent into fetching
  # attacker-authored content from a trusted origin (public artifacts render
  # HTML/JS; shared chats are UGC by definition). No path_default → denylist
  # mode, keeping OAuth/login flows under / and /login intact.
  - dst: .claude.ai
    path_rules:
      - { path: /public/, action: deny }
      - { path: /share/, action: deny }
```

Choose allowlist mode when the needed surface is enumerable (an API
backend); denylist mode when the needed surface is broad (login flows) and
the dangerous surface is enumerable. Path rules apply to
https/http/ws/wss only; paths are open-ended literal prefixes.

## Rule 4: list every domain explicitly (MITM/SNI)

The firewall MITM-inspects TLS and selects per-domain filter chains by
**SNI** — so each domain must be its own entry even when domains share
IPs or a parent brand (the claude floor lists `claude.com`,
`platform.claude.com`, and `.claude.ai` separately; `api.anthropic.com`
and `mcp-proxy.anthropic.com` separately). Matching is exact-host for a
bare domain; a leading dot (`.claude.ai`, `.datadoghq.com`) is the
wildcard form covering every subdomain plus the apex. Do not assume a bare
apex covers subdomains, and do not use a wildcard where the exact host is
known — the tightest form that matches the observed traffic.

MITM also means the harness's runtime must trust the injected CA. The base
image sets `SSL_CERT_FILE`/`CURL_CA_BUNDLE`, and the node stack sets
`NODE_USE_SYSTEM_CA=1`. If the CLI you package pins certificates or reads
a private CA store, it will fail TLS against every allowed domain — that
is a bundle-level integration problem to solve (a tool-specific CA env
var in block_3), not a reason to weaken the firewall.

## WebSocket upgrades

A wss flow to a host that already has an https rule contributes only the
upgrade capability — the https rule owns the path structure. Declare it as
a bare proto entry (codex, verbatim):

```yaml
  # WebSocket upgrade intent for the same origin (streaming responses,
  # observed live at wss://chatgpt.com/backend-api/codex/responses). Not its
  # own route set: the generator absorbs ws/wss into the https rule above,
  # which owns the path structure — this entry contributes only the upgrade
  # capability on those allowed paths.
  - dst: chatgpt.com
    proto: wss
```

## No credential staging — in-container auth is the model

Host credentials are **never** copied into containers. Do not put
credential/keychain/token files in `seeds:` or `staging.copy` — even
"just the config file that happens to contain the token". The model:

- The user authenticates once **inside** the container on first run;
  browser OAuth flows are proxied to the host browser automatically. Your
  floor must therefore include the auth/token-exchange domains (claude:
  `platform.claude.com` token exchange + `.claude.ai` authorization;
  codex: `auth.openai.com`).
- The resulting token persists in the harness **config volume**, surviving
  restarts and recreates that reuse the volume.
- When staging a host config file that mixes portable settings with
  secrets, filter: `json_keys` allowlists the portable keys (the claude
  bundle stages `settings.json` with `json_keys: [enabledPlugins]` — one
  key, nothing else). When the format cannot be filtered (TOML) or the
  file embeds host-keyed state, do not stage it at all (codex's
  `config.toml`).

This posture is also why a tight floor matters: forwarded git credentials
and in-volume tokens are what a compromised agent would exfiltrate, and
the egress floor is the choke point.
