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
