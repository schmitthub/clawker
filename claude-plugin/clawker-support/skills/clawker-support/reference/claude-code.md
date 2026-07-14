# Claude Code

How clawker integrates with the Claude Code harness inside agent containers.
Today that means **authentication** — how a container gets and keeps a login.
(Other Claude Code topics get their own section here as they come up.)

## Authentication

Read this section when a user reports authentication prompts (`/login`) inside
containers, or asks whether their host login is shared with containers.

**The model in one line: host credentials are never copied into containers.
The user authenticates once inside the container, and the credential persists
in the harness config volume.** There is no host-auth knob — authentication is
in-container by design. Fetch `https://docs.clawker.dev/configuration` and
`https://docs.clawker.dev/credentials` for current fields and details.

### The credential model

Claude Code authenticates with an OAuth credential pair:

- an **access token** — short-lived, used for every request, carries an expiry
- a **refresh token** — long-lived, used to mint a new access token (and a
  newly rotated refresh token) when the access token expires

Refresh tokens are single-use: each refresh mints a new refresh token and
invalidates the old one. Inside a container this is invisible — the container
is the sole holder of its own credential family, refreshes land cleanly in the
config volume, and the container keeps itself logged in indefinitely.

### How a container authenticates

1. **First run against a fresh config volume**: Claude Code has no credential
   and prompts `/login`. The OAuth browser flow is proxied to the **host
   browser** automatically (clawker's host proxy handles the callback), so the
   user completes login in their normal browser even though Claude Code runs
   in the container. Anthropic's login domains are part of the claude
   harness's default egress floor, so the flow works with the firewall on.
2. **Claude Code stores the credential in the config volume.** On Linux,
   Claude Code uses an OS Secret Service when one is present and falls back to
   a plaintext credentials file otherwise. Clawker's images ship no Secret
   Service, so the credential lands as a file under the harness config dir —
   which is a named volume. This file fallback is *why* persistence works: if
   a user bakes a Secret Service backend (e.g. `gnome-keyring` + `dbus`) into
   the image, refreshed credentials may land in the keyring instead, which
   does not survive container recreation. Let Claude Code use the file.
3. **The volume outlives the container.** Restarts, and recreates that reuse
   the same project + agent name (and thus the same config volume), stay
   logged in. When the access token expires, Claude Code silently refreshes it
   against the OAuth endpoint and writes the rotated pair back into the
   volume. No further prompts.

The host's login and the container's login are fully independent credential
families. Nothing syncs between them, in either direction — an agent can never
touch the host keychain, and host re-logins have no effect on containers.

### Troubleshooting `/login` prompts

A `/login` prompt inside a container is expected exactly when the config
volume has no valid credential:

- **Brand-new agent (fresh config volume)** — first-run login, one time. The
  browser flow opens on the host automatically; if it doesn't, the host proxy
  may be down — restarting the container re-establishes it.
- **Config volume was removed or recreated** (`clawker volume remove`, or a
  recreate under a different agent name) — the credential lived in the old
  volume; log in once in the new one.
- **Credential revoked or expired server-side** (e.g. revoked from the
  Anthropic console, or the container was stopped so long the refresh token
  lapsed) — log in once; the volume self-heals from there.

**Repeated** prompts in the *same* container/volume are not expected. Check:

- The OAuth/login domains are reachable — they're in the claude harness's
  egress floor by default, but verify with `clawker firewall list` if the
  project heavily customizes rules.
- Something is wiping the config volume between runs (e.g. scripts removing
  volumes, or the user running one-off agents under new names each time —
  each new agent name gets its own fresh volume and thus its own login).

Note: `harnesses.claude.config.strategy: fresh` skips copying host *settings
and plugins* at create time (clean-slate config). It is unrelated to
credentials — those are never copied from the host under any strategy.
