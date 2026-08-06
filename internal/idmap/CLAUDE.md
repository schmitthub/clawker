# internal/idmap

The unprivileged half of clawker's ID-mapped workspace views: mapping
arithmetic, view-path derivation, bind-source rewriting, and the mount state
check. A leaf package (stdlib + moby mount types), so the privileged helper
`cmd/idmap-mount` links it without dragging in the CLI.

## Why it exists

A rootless Docker daemon runs every container in one user namespace, fixed at
daemon start, that maps the daemon's own user to container root. A
bind-mounted workspace therefore arrives **root-owned inside the container**,
and the unprivileged clawker user cannot read or write it — 600 files are
invisible, 775 directories are unwritable.

Neither of the obvious fixes works:

- **Docker has no per-mount ID mapping.** `BindOptions` carries
  `Propagation`/`NonRecursive`/`CreateMountpoint` and nothing else;
  [moby#52061](https://github.com/moby/moby/issues/52061) proposed one in
  February 2026 and was closed unimplemented weeks later. Podman's
  `--userns=keep-id` has no Docker equivalent — podman is daemonless and
  builds a fresh user namespace per container, which one shared daemon
  structurally cannot do.
- **Chowning the tree inside the container works but is mutually exclusive.**
  Container root can chown, and the files land on disk as the subordinate IDs
  — at which point the host user is locked out of their own repository and
  every handoff costs another sweep. Acceptable for an ephemeral snapshot
  copy; disqualifying for bind mode, whose whole purpose is live two-way
  editing.

The kernel's own answer is an **ID-mapped mount**: a second view of the same
tree that translates IDs at the VFS layer. Both sides see native ownership
simultaneously, no files are modified, and the mapping dies with the mount.
Creating one needs init-namespace `CAP_SYS_ADMIN`
([`mount_setattr(2)`](https://www.man7.org/linux/man-pages/man2/mount_setattr.2.html)),
which is why the attach itself lives in a sudo one-shot.

## Surface

```go
type Mapping struct{ FromUID, ToUID, FromGID, ToGID uint32 }
type MappingInputs struct {
    OwnerUID, OwnerGID uint32   // the workspace's on-disk owner
    UserName string             // the daemon user, as /etc/subuid keys it
    UserUID  uint32             // …or as a numeric key
    Subuid, Subgid string       // raw file contents
}

func ComputeMapping(MappingInputs) (Mapping, error)
func ViewPath(base, source string) string
func RewriteMounts([]mount.Mount, root, view string) ([]mount.Mount, int)
func RewriteBinds([]string, root, view string) ([]string, int)
func FormatIDPair(from, to uint32) string
func ParseIDPair(string) (uint32, uint32, error)
func Mounted(path string) bool          // linux/other split

const SubUIDFile = "/etc/subuid"
const SubGIDFile = "/etc/subgid"
```

## The mapping formula

Rootless Docker's documented layout: container uid 0 maps to the daemon
user's own host uid, and container uid n≥1 maps to `subuid_base + n − 1`,
walking the user's `/etc/subuid` ranges in file order. `ComputeMapping`
implements exactly that, over the concatenation of all the user's ranges.

The workspace owner's uid doubles as the container-side uid: the image bakes
the clawker user with the host user's uid, so the files' owner and the
container user are the same number. Verified live (uid 1003, subuid base
296608 → 297610).

Refusals are explicit, because a wrong mapping is worse than none: a
root-owned workspace, a user with no subordinate ranges, and an id beyond the
ranges each name what went wrong. Malformed lines in the files are skipped
rather than fatal — those files are system-owned and one stray row must not
break every container create.

## Rewriting

`RewriteMounts`/`RewriteBinds` repoint bind sources at or under `root` into
`view`, returning a copy plus the count. Both compare against
`root + separator` so a sibling sharing the root's string prefix
(`…/proj` vs `…/projects`) is left alone, and both ignore anything that is
not a host bind (named volumes, tmpfs, mounts elsewhere on the filesystem).

## Who uses it

- `internal/cmd/container/shared/idmap.go` — the create-path decision: is
  this daemon rootless, does anything bind under a workspace root, is a view
  already mounted, and the rewrite itself. See that package's `CLAUDE.md`.
- `cmd/idmap-mount` — the sudo one-shot. It links this package for
  `ParseIDPair` and `Mounted` so the argument format and the state check are
  one definition shared across the privilege boundary, rather than two that
  can drift.

## Related

- `controlplane/firewall/ebpf/delegation` — the same shape for the BPF
  filesystem: a small contract package both sides of a privilege boundary
  compile against.
- `internal/cmdutil` `RunElevated` — stages an embedded helper into a fresh
  0700 directory, prompts for the sudo credential, runs it once, removes it.
