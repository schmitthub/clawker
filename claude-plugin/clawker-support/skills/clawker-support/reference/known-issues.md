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
