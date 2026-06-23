# VCS Egress Lockdown (optional git credential-exfil hardening)

Brief, proactive hardening. Offer it when setting up or reviewing firewall
rules for a project that has git credentials forwarded (the default).

## Why

Agents run with a forwarded **GitHub token** (used by the `gh` CLI and
git-over-HTTPS). A prompt-injected agent can use that token to **push stolen
code/secrets to an attacker-controlled repo**. Most sandboxes allow/deny by
**domain only** — "allow `github.com`" lets the agent push *anywhere*. clawker
MITM-inspects HTTPS **paths**, so you can scope GitHub egress to **only the
repos the project needs**; an injected agent then can't push elsewhere even
with a valid token. (Defense-in-depth — public repos are exposed regardless.)

## How domain matching works (get this right or rules silently over/under-scope)

A rule's destination matches the request host by **exact host** or by an
explicit **wildcard**, never loosely:

- **Bare domain** (`example.com`, or any `add_domains:` entry) matches **only
  that exact host**. It does **not** match subdomains — `api.example.com` is a
  separate host and stays blocked.
- **Leading-dot domain** (`.example.com`) is the **wildcard** form: it matches
  every subdomain (`api.example.com`, `us.api.example.com`, …) **and** the bare
  apex `example.com` itself.
- Matching is on **label boundaries / suffix**, so it cannot be tricked:
  `example.com` does **not** match `example.com.evil.com`, and `.example.com`
  does **not** match `notexample.com`.
- An **exact rule always beats a wildcard** for the same host, regardless of
  order — so a wildcard can never shadow a more specific rule (e.g. a
  `.example.com` allow plus an `example.com` deny still denies the apex).
- Within a tier, **deny wins over allow**.

Use the bare form when the work needs one exact host (the common case); reach
for `.domain` only when the agent genuinely needs arbitrary subdomains.

## Scope the two obligatory HTTPS domains

`gh` and git-over-HTTPS only need `github.com` (git transport) and
`api.github.com` (the API `gh` drives). Path-scope those to your repo. Golden
example, battle-tested in clawker's own `.clawker.yaml`:

```yaml
# In: .clawker.yaml (project-level) — under security.firewall.rules
# git transport — scope github.com to your repo so `git push https://github.com/other/repo` is blocked
- action: allow
  dst: github.com
  proto: https
  port: 443
  path_default: deny
  path_rules:
    - { action: allow, path: /<owner>/<repo> }                          # clone/fetch/push for your repo only
# the REST API gh drives
- action: allow
  dst: api.github.com
  proto: https
  port: 443
  path_default: allow
  path_rules:
    - { action: allow, path: /repos/<owner>/<repo>/ }                   # your repo
    - { action: allow, path: /anthropics/claude-code/ }                   # Claude Code auto-update — ALWAYS include
    - { action: deny,  path: /gists }                                   # block gist creation (exfil channel); no trailing slash so bare POST /gists is caught
    - { action: deny,  path: /repos/ }                                  # every other repo blocked
```

- **`github.com` must be the path-scoped rule above, not a bare entry in
  `add_domains`** — `add_domains` is an unrestricted domain allow that defeats
  the scoping. A path-scoped rule on the apex domain is what blocks
  `git push https://github.com/hackgroup/evil.git`.
- A path rule's `path` is a **literal prefix** by default, and prefixes are
  open-ended: `/<owner>/<repo>` allows `/<owner>/<repo>/anything` **and**
  `/<owner>/<repo>-evil`. On a host where the path embeds a name you don't
  control (`/<owner>/...`) that open end is exploitable.
- To close it, prefix the path with `~` to make it an **RE2 regex matched
  full-string** (both ends anchored, like Envoy's `safe_regex`), e.g.
  `~/<owner>/<repo>(/.*)?` to scope to that repo and its subtree only, or
  `~/repos/(<owner>|<other>)/?` for an exact set. Because it is already
  full-string anchored, a trailing `$` is redundant (accepted), and a leading
  `^` is accepted/stripped. The trailing slash is significant (`~/x` ≠ `~/x/`),
  so match the form(s) the host serves (`/?` or `(/.*)?` accepts both).
- The engine is **RE2** — linear-time, no catastrophic backtracking, **no
  backreferences or lookaround**. It only protects you if your pattern is tight;
  an over-broad pattern is your own footgun.
- **Precedence when rules overlap:** the **longest rule string wins**, ties fall
  to declaration order (first listed). A regex's "length" is its literal char
  count, **not** how much it matches — a short broad regex can lose to a longer
  literal and vice versa. Both enforcement paths (Envoy and the host-browser
  egress gate) use this same ordering, so they stay in lockstep. To force a
  winner among overlapping rules, list it first or lengthen its string.
- An invalid path (literal not starting with `/`, literal with characters that
  can't appear in a URL path — usually a regex missing its `~`, regex that
  doesn't compile or doesn't anchor at the path root) fails the whole
  add/refresh rather than loosening the rule. Still discover any extra github.com paths the agent
  legitimately needs (release downloads, git deps) via the monitoring loop below.

> **CLI sharp edge:** regex paths must be **single-quoted** in the shell —
> `clawker firewall add api.github.com --path '~/repos/(a|b)/?' --action allow`
> — because `~/` tilde-expands and `(`, `|`, `?` are shell metacharacters.

- **`path_default`** (`allow`|`deny`) is the verdict for any path matching no
  rule. If you omit it, it's inferred from the rules: any `allow` path ⇒ default
  **deny** (allowlist mode — the secure default); rules that are all `deny` ⇒
  default **allow** (denylist mode). Set it explicitly when in doubt.
- **Always include `/anthropics/claude-code/`** — covers Claude Code's
  auto-update checks and related API calls; omitting it breaks updates inside the agent.
- **Trailing slash** scopes to sub-paths: `/repos/<owner>/<repo>/` allows
  reading code (`/contents`, `/git`, …) but not the bare repo-root endpoint —
  drop the slash if the workflow needs root metadata (e.g. `gh repo view`).
- Add more `allow` paths for git-based dependencies the project legitimately
  uses. Other GitHub hosts (`uploads.github.com`, `gist.github.com`, …) are
  separate allowlist entries — already denied by default; leave them out unless
  a workflow needs one.

## Find the paths you actually need

Don't guess every dependency path. `clawker monitor up`, run the normal
workflow, watch firewall **block events** in OpenSearch Dashboards (the
`clawker-envoy` egress stream), add the legitimately-needed paths, re-run.
See `monitoring.md`.

Add discovered paths at runtime without a config edit or restart (host-side,
hot-reloaded, persisted to the rules store):

```bash
clawker firewall add github.com --path /<owner>/<dep> --action allow
```

Each `--action allow` path keeps the domain in allowlist mode (deny everything
else). The runtime store and `.clawker.yaml` are separate — once the path set
is settled, write it back into `.clawker.yaml` so it's shared with the team and
survives a fresh rules store. After editing `.clawker.yaml`, apply it live with
`clawker firewall refresh` (re-runs the startup sync into the store without a
container restart) instead of re-typing each `clawker firewall add`. Refresh is
add/update only — a rule *deleted* from the YAML is not pruned; use
`clawker firewall remove` for that.

## Method gating — the coarse write backstop

Because HTTPS is MITM-terminated, the firewall sees the request **method**. A
path rule's optional `methods` field narrows its `action` to a set of HTTP
verbs — it is a match condition, not a separate verdict, so `action` supplies
the polarity and unlisted methods fall through to later rules / `path_default`.
Empty `methods` = all methods (the rule is method-agnostic). HTTP-family protos only
(`https`/`http`/`ws`/`wss`). `methods` is a concrete verb enum (`GET`, `POST`, …) —
**never a regex**; the `~` marker applies to `path` only.

This is the durable backstop path scoping can't be: one "deny mutating methods"
rule covers write endpoints **that don't exist yet**. GitHub writes ride
mutating verbs (`git push` = `POST .../git-receive-pack`; contents API =
`PUT|DELETE`; Git Data = `POST|PATCH`); reads are `GET`. So a method gate kills
push-to-arbitrary-repo and contents-write exfil without enumerating every write
path.

```yaml
# Make a VCS host read-only: allow GET/HEAD everywhere, deny the rest.
- action: allow
  dst: api.github.com
  proto: https
  port: 443
  path_default: deny
  path_rules:
    - { action: allow, path: /, methods: [GET, HEAD] }
```

Or deny writes on a prefix while leaving reads open (`action: deny` + the
mutating verbs; `GET` falls through):

```yaml
  path_rules:
    - { action: deny, path: /repos/, methods: [POST, PUT, PATCH, DELETE] }
```

CLI equivalent (runtime, hot-reloaded):
`clawker firewall add api.github.com --path / --action allow --methods GET,HEAD`.

Compose with path scoping: longest-prefix path still selects the route, the
method gate then allow/denies within it. There is no rule-level `methods` — a
`/` path rule is the host-wide form.

## Two gaps path rules can't cover

- **SSH git (port 22) is opaque** — nobody can path-filter it; an allowed
  `github.com:22` plus a forwarded SSH key permits pushing anywhere. Prefer
  HTTPS git for untrusted work.
- **`/graphql` is one path for all reads AND writes** (the operation is in the
  POST body), and `gh` needs it for reads — so you can't deny it.

Both gaps close at the **token**: forward a **least-privilege / read-only
fine-grained token** so writes fail at GitHub's authorization layer regardless
of the firewall. Surface this as the complementary half (GitHub-side token
hygiene), not a clawker config field.

## How to offer it

Proactively offer to scope `github.com` + `api.github.com` (and other VCS in
use) over HTTPS to the project's repo(s) plus needed dependencies, always
including the Claude Code update path, and recommend the monitoring loop to
discover remaining paths. **Suggest the YAML; never auto-apply.** Mention the
SSH and least-privilege-token caveats.
