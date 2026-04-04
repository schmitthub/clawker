# Brainstorm: Hostproxy Egress Control

> **Status:** Completed — converted to initiative `initiative-hostproxy-egress`
> **Created:** 2026-04-04
> **Last Updated:** 2026-04-04 02:00

## Problem / Topic
The hostproxy (`internal/hostproxy/`) is an unauthenticated HTTP service on the host that containers use for OAuth flows and git credential forwarding. It has zero access control — any process in the container can exfiltrate data through it, completely bypassing the Envoy+CoreDNS firewall. Red team testing confirmed exfil of GH_TOKEN, API keys, git identity, and SSH known_hosts via the `/open/url` endpoint through an ngrok tunnel. The hostproxy should enforce the same egress rules as the firewall.

## Open Items / Questions
- Where is `egress-rules.yaml` on disk? The hostproxy needs to know the path — likely via config (`cfg.EgressRulesFilePath()` or similar)
- How are path rules structured in the YAML? Need to understand the schema to implement matching
- Should we also sanitize newlines in `formatGitCredentialInput()` as a separate fix?
- Should the error message tell the agent the domain was blocked, or be opaque?

## Decisions Made
- **Breakfix: parse egress rules in `/open/url` handler.** On every request, read `egress-rules.yaml` from disk, parse the target URL's domain, check if it's in the allow list (with path matching if path rules exist). No caching — always just-in-time read so `firewall add/remove` takes effect immediately. This is the only exfil-capable endpoint.
- **`/git/credential` is not an exfil channel.** It returns secrets to the container, but the container already has access to those secrets. Not a data-out path.
- **`/callback/register` is not an exfil channel on its own.** Data flows inbound. Only becomes dangerous in combination with `/open/url`, which is now gated.
- **The enforcement point is the hostproxy server, not the container-side scripts.** The agent runs as `claude` user and can bypass `host-open.sh` by curling the hostproxy directly. Only the host-side server can enforce.

## Conclusions / Insights
- Rules in `egress-rules.yaml` are always normalized: proto defaults to `tls`, action to `allow`, TLS port to 443. No port 0 values in practice.
- Wildcard domains use leading-dot convention (`.claude.ai`). `normalizeDomain()` strips the dot, `isWildcardDomain()` checks for it.
- No existing "does this URL match these rules?" function — firewall only generates configs, never queries at runtime. Hostproxy needs its own match function.
- `normalizeRule()` and `normalizeAndDedup()` in `rules.go` are reusable for reading the rules file.
- Path rules exist in schema (`PathRule` with `path` prefix + `action`, `path_default` on `EgressRule`) but aren't in use yet. Implementation should handle them for completeness.
- URL scheme maps to proto: `https` → `tls`, `http` → `http`. Port from URL or default (443/80).
- The rules file is at `cfg.FirewallDataSubdir()/egress-rules.yaml`, read via `storage.Store[EgressRulesFile]`.
- Rules are purely additive during a session — the file only grows. Just-in-time read ensures latest state.

## Gotchas / Risks
- The hostproxy runs on the host, not in the container — it needs to read firewall rules from the host filesystem
- If the hostproxy checks rules at request time, rule changes take effect immediately (good). But it needs to handle file watching or re-reading.
- The egress rules are purely additive and never shrink during a session — stale reads are always a subset of current rules (safe direction)
- OAuth callback flow requires opening URLs on external OAuth providers — these must be in the egress rules or OAuth breaks
- Git credential forwarding for `github.com` requires `github.com` in the egress rules — likely always present since Claude API access requires it

## Unknowns
- Does the firewall manager expose an API to query rules, or is direct file read the only option?
- What's the performance impact of checking rules on every hostproxy request?
- Are there OAuth providers that aren't in typical egress rules?

## Next Steps
- Implement URL-to-rule matching function in the hostproxy package
- Wire it into the `/open/url` handler: parse URL → check rules → block or proceed to `openBrowser()`
- Fix newline injection in `formatGitCredentialInput()` as separate patch
- Test with the adversarial harness to confirm exfil is blocked
