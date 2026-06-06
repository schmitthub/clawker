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
    - { action: allow, path: /anthropics/claude-code/refs/heads/main/ } # Claude Code auto-update — ALWAYS include
    - { action: deny,  path: /gists }                                   # block gist creation (exfil channel); no trailing slash so bare POST /gists is caught
    - { action: deny,  path: /repos/ }                                  # every other repo blocked
```

- **`github.com` must be the path-scoped rule above, not a bare entry in
  `add_domains`** — `add_domains` is an unrestricted domain allow that defeats
  the scoping. A path-scoped rule on the apex domain is what blocks
  `git push https://github.com/hackgroup/evil.git`.
- Path rules are **prefix** matches today, so `/<owner>/<repo>` also matches
  `/<owner>/<repo>-evil` — keep the scope tight and discover any extra
  github.com paths the agent legitimately needs (release downloads, git deps)
  via the monitoring loop below.

- **Always include `/anthropics/claude-code/refs/heads/main/`** — Claude Code's
  auto-update version check; omitting it breaks updates inside the agent.
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
