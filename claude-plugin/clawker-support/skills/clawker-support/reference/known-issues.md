# Known Issues

Active bugs and workarounds. Check this before deep-diving into
troubleshooting — the user's problem may already be documented here.

## env_file quoted values

`agent.env_file` may include quotes as part of the value if the `.env`
file uses quoted values. Workaround: use bare values.

```
# Safe
FOO_API_KEY=sk-abc123

# May break — quotes become part of the value
FOO_API_KEY="sk-abc123"
```

## go build VCS error in older worktree containers

In worktree containers created before the `GOFLAGS=-buildvcs=false` default,
any `go build` of a main package fails:

```
error obtaining VCS status: exit status 128
	Use -buildvcs=false to disable VCS stamping.
```

Go skips the worktree's `.git` *file*, walks up to the mounted main `.git`
directory, and trips git's `safe.directory` check on the root-owned mount
scaffold dir. Plain git inside the worktree works, which makes this confusing.

Fix: recreate the container (new worktree containers set
`GOFLAGS=-buildvcs=false` automatically). In-place workaround for an existing
container: `export GOFLAGS=-buildvcs=false` (or add it to `agent.env` and
recreate). Do **not** suggest `git config --global --add safe.directory` for
the main repo path — git at that path sees the whole tree as deleted, and a
`git add -A` there would stage mass deletions into the host's real index.

## Empty refreshToken / forced /login in older containers

Containers created with an older clawker release injected a host keychain
credential blob that was round-tripped through a typed struct, fabricating a
zero-value `organizationUuid` (`00000000-...`) the user is not a member of.
Claude Code's token refresh endpoint rejects that claim, and Claude Code is
known to **wipe its stored credential with empty data when a refresh fails**
(upstream anthropics/claude-code behavior) — the user sees an empty
`refreshToken` in `~/.claude/.credentials.json` and a forced `/login` at boot.

Current releases never copy host credentials into containers at all —
authentication happens inside the container and the credential persists in the
harness config volume — so new containers are unaffected. The poisoned blob
persists in config volumes created by affected releases, though.

Fix: run `/login` once inside the affected container — Claude Code writes a
fresh credential into the config volume, which self-heals from there.
Host-side re-authentication has no effect on containers (credentials are
independent families; nothing is copied or synced).

**Also affected-releases-only:** containers whose volumes hold a host-copied
credential can hit an *expected* one-time `/login` from refresh-token
rotation — refresh tokens are single-use, so if the host "turned in" the
shared token first, the container's copy is invalidated (`invalid_grant`).
Same fix: `/login` once; the container then owns its own credential family.
The struct-roundtrip bug above is distinguished by a zero-value
`organizationUuid` in the poisoned blob. Current auth model and `/login`
troubleshooting in `claude-code.md`.

## git push -u / upstream tracking in worktree containers

Worktree containers mask the main repo's `.git/hooks/` and `.git/config` with
read-only binds. This is a deliberate security measure (always on): anything
written to those paths from inside the container would execute on the *host*
the next time the user runs git in the main checkout. See
`https://docs.clawker.dev/worktrees#worktree-caveats`.

Consequences inside the container: `git config --local` and `git remote add`
fail loudly; `git push -u` **pushes successfully but fails to persist upstream
tracking** (easy-to-miss warning — upstream config lives in the read-only
shared `.git/config`).

Symptom after the session: the user removes the worktree, checks the branch
out in the repo root, and plain `git push` reports no upstream — even though
the branch was pushed and has an open PR. Tracking was never persisted.

Fix: one-time on the host: `git push -u origin <branch>` (or
`git branch --set-upstream-to=origin/<branch>`). Inside the container, push
with an explicit refspec: `git push origin HEAD`. Branches that already
existed on the remote at worktree creation get tracking configured host-side
automatically and are unaffected.

## --worktree on a branch already checked out

`clawker run --worktree <branch>` for a branch that is already checked out in
the main repo (or another worktree) is refused:

```
branch is already checked out in another worktree: "<branch>" is checked out at <path>
```

This mirrors native `git worktree add` — one branch cannot be checked out in two
places, or a commit in one moves the other's HEAD onto content it never checked
out. Common trigger: `git switch -c <branch>` in the repo root, then
`clawker run --worktree <branch>` on the same name.

Fix: either switch the root checkout off the branch (`git switch <other>` there),
or pass `--worktree <new-branch>` and let clawker create and own a fresh branch
(don't pre-create it in root). If a worktree commit already slid root's HEAD,
`git -C <root> switch <branch>` (or `git switch -f <branch>`) re-separates them;
no commits are lost — they live on the branch ref.

## Claude Code ignores `nvm use` / `nvm alias default` / `.nvmrc`

Images built with the node stack (declared by the claude harness, or via
project `build.stacks`) carry Node + npm on PATH and the agent's Bash tool
uses them out of the box — so this only bites once a user installs
**additional** Node versions with `nvm` and expects the agent to switch onto
one.

Claude Code selects the Node for its Bash tool when it builds its shell
snapshot, and it does **not** honor nvm's normal version-selection mechanisms:
`nvm use <ver>`, `nvm alias default`, and a project `.nvmrc` are all ignored.
It picks a version by scanning `~/.nvm/versions/node/*` directly, so an
installed-but-not-selected version can win and the agent runs a different Node
than `nvm` reports. This is an upstream Claude Code bug
([anthropics/claude-code#54135](https://github.com/anthropics/claude-code/issues/54135)),
not a clawker defect — until it's fixed, the nvm directives won't steer the
agent.

Until upstream fixes it, nvm's version directives won't steer the agent's Bash
tool. The reliable fix is to bake the Node version you need as the single
default instead of juggling versions with nvm: override the `NODE_VERSION` build
arg to the major line you require, e.g. `clawker build --build-arg NODE_VERSION=22`.
That major is then the only Node on `/usr/local`, so the agent always selects it
and the selection bug never applies. `NODE_VERSION` takes a **major** line only
(`22`, `24`, …) — the latest LTS patch of that line is resolved per build; minor
and patch pins are not supported. Only reach for `nvm` (and hit this bug) when
you genuinely need multiple Node versions in one image.
