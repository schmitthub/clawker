# Claude Code

How clawker integrates with Claude Code inside agent containers. Today that
means **authentication** — sharing the host login with containers and why that
sharing has limits. (Other Claude Code topics get their own section here as they
come up.)

## Authentication

Read this section when a user reports authentication prompts (`/login`) inside
containers, or asks how `use_host_auth` works.

`use_host_auth` (under `agent.claude_code`, default enabled) is the only knob.
Fetch `https://docs.clawker.dev/configuration` for the current field path and
schema — everything below is the stable model, not field syntax.

### The credential model

Claude Code authenticates with an OAuth credential pair:

- an **access token** — short-lived, used for every request, carries an expiry
- a **refresh token** — long-lived, used to mint a new access token (and a
  newly rotated refresh token) when the access token expires

On the **host**, Claude Code keeps this pair in the OS keychain (with a file
fallback). When the access token expires, Claude Code silently refreshes it
using the refresh token and writes the rotated pair back to the keychain. The
user never sees a prompt as long as the refresh token is still valid. A prompt
(`/login`) appears only when the refresh token itself is expired or revoked.

### How clawker shares it with a container

When `use_host_auth` is enabled, clawker reads the host's stored Claude
credentials **once, at container create time**, and writes them into the
container's config **named volume** (mounted at `~/.claude`, as
`.credentials.json`). Three properties follow from this, and they explain almost
every support question:

1. **Create-only snapshot.** Injection happens when the container is *created*,
   never on `start` or `restart`. A restarted container keeps whatever
   credentials its volume already holds. Re-running `clawker` against an
   existing container does not re-read the host.

2. **The volume self-heals.** Claude Code on Linux stores credentials in an OS
   Secret Service (libsecret / gnome-keyring over D-Bus) **when one is present**,
   and falls back to the plaintext `.credentials.json` otherwise. Clawker's base
   image ships no Secret Service, so Claude Code reads and writes
   `.credentials.json` in the config volume directly. The first time the
   in-container access token expires, Claude Code refreshes it against the OAuth
   endpoint (allowlisted by default) and writes the rotated pair *back into the
   volume*. Because the volume persists across restarts, that refreshed refresh
   token sticks — the container keeps itself logged in on its own from then on.

   This file fallback is *why* persistence works. If a user bakes a Secret
   Service backend (e.g. `gnome-keyring` + `dbus`) into a custom image, Claude
   Code may store refreshed credentials in the keyring instead — which does not
   live under `~/.claude`, so it would not survive container recreation and
   could defeat the self-heal. For shared-host-auth to behave as described, let
   Claude Code use the file.

3. **The two sides never sync — by design.** clawker plants a byte-for-byte copy
   of the credential at create time and nothing more. From then on the host and
   the container refresh independently, and clawker propagates nothing between
   them. That is deliberate on three counts: an agent must never write to the
   host keychain (a hard security boundary); a single host login can back many
   containers that each refresh their own volume on their own schedule, so there
   is no coherent "sync" to perform; and Anthropic's terms reserve OAuth
   credential handling — refresh, rotation, any mutation — to first-party Claude
   Code. clawker only seeds the initial copy; it never takes part in a token
   exchange. This one-way model is the root of the `/login` confusion below.

An expired *access* token at create time is harmless: clawker injects it
anyway, because the refresh token (not the access token) is what lets Claude
Code recover, and it refreshes on first use. Only the **refresh token's**
validity matters for whether a fresh container starts authenticated.

### Troubleshooting

#### Repeated `/login` in new containers despite `use_host_auth` enabled

**Symptom.** Every new agent container prompts for `/login` even though
`agent.claude_code.use_host_auth` is on.

**Most likely cause.** The host's **refresh token was already expired or
revoked** when the container was created. The snapshot clawker copied in could
not be refreshed, so Claude Code falls back to an interactive login. Nothing is
misconfigured — the shared credential was simply stale at copy time.

This is reasoned about from the model above, not diagnosed from inside the
container: the credential blob doesn't expose the refresh token's expiry, and
the sandbox can't reach the host keychain.

**For the container that already prompted: nothing more to do.** Once the user
completes `/login` inside it, Claude Code writes the fresh, rotated credentials
into that container's config volume. The volume persists across restarts and
keeps refreshing itself, so the user won't be prompted again in that container.
They do **not** need to recreate or restart it.

**To stop *future* new containers from prompting: re-authenticate on the host.**
Have the user start a Claude Code session on the **host** and log in (or run
any host Claude Code command that triggers a refresh). That writes a fresh
refresh token into the host keychain, which new containers will snapshot at
create time. Caveat: only containers created *after* the host re-auth benefit —
ones created earlier keep their own (now self-healed) volume credentials.

**If it persists for brand-new containers created after a host re-login**, rule
out the simpler causes:

- `use_host_auth` is actually disabled for this project/agent (it defaults on,
  but a config may set it off). Confirm against the live config schema.
- The container was created with `agent.claude_code.config.strategy: fresh`,
  which skips copying host settings and plugins (clean slate). Credential
  injection is controlled separately by `use_host_auth`; if both are disabled,
  the container starts with no credentials and no host config.
- The user was never authenticated on the host at all — in that case container
  creation fails with an explicit "no credentials found" error rather than a
  silent `/login` prompt, so this is a different symptom.
