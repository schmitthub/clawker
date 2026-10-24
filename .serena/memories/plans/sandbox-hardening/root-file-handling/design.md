# Item 5: Root-process file handling (ruled 2026-10-24)

Part of `mem:plans/sandbox-hardening/initiative` (Task 2).

## Problem

- `internal/bundler/assets/Dockerfile.base.tmpl` ~155-157 chowns `/var/run/clawker` to the agent user. `/var/run` is a symlink to `/run`, so the agent owns `/run/clawker`: the parent of the root-only `/run/clawker/bootstrap` (clawkerd leaf, key, CA, assertion) and of the ready marker. The comment at ~148 claims the unprivileged user reads and writes runtime state there; no such writer exists in the code.
- `clawkerd/spawn_unix.go` `touchReadyFile` opens `consts.ReadyMarkerPath` with `O_CREATE|O_WRONLY|O_TRUNC` and no `O_NOFOLLOW` as root.
- `internal/hostproxy/internals/cmd/clawker-socket-server/main.go` ~300-425 (`setupGPGPubkey`, `createSocketListener`) runs as root inside agent-owned `~/.ssh` and `~/.gnupg`: `MkdirAll` + `Chown(dir)`, `WriteFile` for `pubring.kbx`, `gpg.conf`, `gpg-agent.conf`, `Remove` + `net.Listen` + `Chmod` + `Chown` on the socket path. All follow symlinks; the home overlay persists across stop/start, so a planted symlink survives to the next start.

## Ruling

- `/run/clawker` is root-owned 0755. Remove `/var/run/clawker` from the chown and fix the comment. The only reader of the ready marker is the Docker `HEALTHCHECK` (`test -f /var/run/clawker/ready`), which runs as root (harness image final `USER` is root). No agent exception.
- `touchReadyFile`: add `O_NOFOLLOW`. Defense in depth once the dir is root-owned.
- socket-server: no path-string operations under the agent home. Open each target directory as a dir fd and operate relative to it.

## socket-server design

- `openUserDir(home, name, uid)`: `unix.Openat(AT_FDCWD, home, O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC)`, then `Openat(homeFd, name, O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC)`; on `ENOENT`, `Mkdirat(homeFd, name, 0700)` then reopen; `Fchownat(homeFd, name, uid, gid, AT_SYMLINK_NOFOLLOW)`. `Fstat` the fd: `Uid == uid`, mode has no group/other write. `ENOTDIR`/`ELOOP` → refuse with a clear error naming the path. Never `MkdirAll`.
- Regular files (`pubring.kbx`, `gpg.conf`, `gpg-agent.conf`): `Fstatat(dirFd, name, AT_SYMLINK_NOFOLLOW)`; if present and not a regular file → refuse; if regular → `Unlinkat(dirFd, name, 0)`; then `Openat(dirFd, name, O_WRONLY|O_CREAT|O_EXCL|O_NOFOLLOW|O_CLOEXEC, 0600)`, write, `Fchown(fd, uid, gid)`, close. `EEXIST` after the unlink = a race with the agent → refuse, do not retry.
- Unix socket: `Fstatat` nofollow; existing socket node → `Unlinkat`; anything else present → refuse. Set the process umask to `0177` around the bind so the node is created 0600 with no post-bind `Chmod` (`Fchmodat` has no nofollow flag before Linux 6.6). Bind via `net.Listen("unix", "/proc/self/fd/<dirFd>/<name>")` so resolution reuses the already-opened directory and the final component is created by `bind`, which fails with `EADDRINUSE` if anything appears there. Then `Fchownat(dirFd, name, uid, gid, AT_SYMLINK_NOFOLLOW)`.
- Keep `getTargetUserFromPath` for uid/gid; add a check that the resolved home is the directory actually opened (`Fstat` of `homeFd` owner == uid).
- Log every refusal with the path and reason; the bridge start fails closed for that socket.

## Checks

- Unit tests with a temp home: symlinked `.gnupg`, symlinked `pubring.kbx`, symlinked socket dir, FIFO at a conf path → all refused, target untouched (assert via `Lstat` on the symlink target). Happy path: files 0600 owned by the test uid (chown may be a no-op when not root; assert the call shape through a small interface only if the test cannot run as root, otherwise skip on non-root with `t.Skip` and run in CI's Docker test).
- `touchReadyFile` test: symlink at the marker path → error, target not created or truncated.
- Runtime: `stat -c '%U %a' /run/clawker` → `root 755`; GPG and SSH bridge work from the agent shell (`gpg --list-keys`, `ssh-add -l`).

## Out of scope

- Agent-owned `~/.claude` and `mount_projects` contents (user policy, documented).
