package firewall

// cgroup.go — helpers that translate between a Docker container identity
// and the BPF-attachable cgroup path that eBPF operations use. Callers
// detect the driver once at init (DetectCgroupDriver), cache it, and
// resolve paths internally — no external caller supplies a cgroup path.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
)

// DetectCgroupDriver returns the Docker daemon's cgroup driver (typically
// "systemd" on native Linux, "cgroupfs" on Docker Desktop). The value is
// stable for the daemon's lifetime; callers cache it at init. Errors
// propagate rather than defaulting — a silent default would produce
// ENOENT at eBPF attach time.
func DetectCgroupDriver(ctx context.Context, dc *docker.Client) (string, error) {
	info, err := dc.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", fmt.Errorf("querying Docker cgroup driver: %w", err)
	}
	return info.Info.CgroupDriver, nil
}

// cgroupDriverSystemd is Docker's systemd cgroup-driver name as reported by
// Info.CgroupDriver; anything else is treated as the cgroupfs layout.
const cgroupDriverSystemd = "systemd"

// containerCgroupName returns the directory name Docker gives a container's
// cgroup under its per-daemon parent: a systemd scope unit, or the bare
// container ID under cgroupfs.
func containerCgroupName(cgroupDriver, containerID string) string {
	if cgroupDriver == cgroupDriverSystemd {
		return "docker-" + containerID + ".scope"
	}
	return containerID
}

// EBPFCgroupPath returns the conventional ROOTFUL cgroup v2 path for a Docker
// container: system.slice for the systemd driver, the docker directory for
// cgroupfs. It is the fast path only — a rootless daemon parks its containers
// under the user slice at a uid-dependent depth, which the resolver finds by
// discovery (cgroupPathResolver) instead of layout assumptions.
func EBPFCgroupPath(cgroupDriver, containerID string) string {
	return conventionalCgroupPath(consts.SysFSCgroupPath, cgroupDriver, containerID)
}

// conventionalCgroupPath is EBPFCgroupPath under an explicit hierarchy root.
func conventionalCgroupPath(root, cgroupDriver, containerID string) string {
	if cgroupDriver == cgroupDriverSystemd {
		return filepath.Join(root, "system.slice", containerCgroupName(cgroupDriver, containerID))
	}
	return filepath.Join(root, "docker", containerID)
}

// cgroupPathResolver resolves a container's BPF-attachable cgroup path.
// The conventional rootful layout is tried first; when the daemon parks its
// containers elsewhere (rootless Docker: user.slice/user-<uid>.slice/…, a
// uid-dependent depth no constant can express), the resolver walks the
// hierarchy once for the target container's own directory and caches the
// parent — every container of one daemon shares it, and keying the walk on
// the target's ID keeps a box running several daemons unambiguous.
type cgroupPathResolver struct {
	driver string
	root   string // consts.SysFSCgroupPath in production; a temp dir in tests

	mu     sync.Mutex
	parent string // discovered scope parent, "" until the first walk
}

// path returns the cgroup directory for containerID, or an error when no
// such directory exists anywhere under root — Docker says the container is
// alive, so a missing cgroup is a real fault to surface, never a path to
// fabricate.
func (r *cgroupPathResolver) path(containerID string) (string, error) {
	name := containerCgroupName(r.driver, containerID)
	if p := conventionalCgroupPath(r.root, r.driver, containerID); dirExists(p) {
		return p, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.parent != "" {
		if p := filepath.Join(r.parent, name); dirExists(p) {
			return p, nil
		}
	}
	parent, err := discoverCgroupParent(r.root, name)
	if err != nil {
		return "", err
	}
	r.parent = parent
	return filepath.Join(parent, name), nil
}

// discoverCgroupParent walks root for a directory named name and returns
// that directory's parent. Walk errors on individual entries are skipped —
// permission holes are expected in the host hierarchy — but a name that
// never appears is an error.
func discoverCgroupParent(root, name string) (string, error) {
	var found string
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // unreadable subtrees are expected; the target lives elsewhere
		}
		if d.Name() == name {
			found = filepath.Dir(p)
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("scanning %s for %s: %w", root, name, walkErr)
	}
	if found == "" {
		return "", fmt.Errorf("no cgroup directory named %s under %s", name, root)
	}
	return found, nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// ResolveContainerID normalizes a container reference (name, short ID,
// or canonical long ID) to the 64-char lowercase hex long ID that
// EBPFCgroupPath expects. Canonical inputs skip the Docker round-trip.
func ResolveContainerID(ctx context.Context, dc *docker.Client, ref string) (string, error) {
	if isCanonicalContainerID(ref) {
		return ref, nil
	}
	info, err := dc.ContainerInspect(ctx, ref, client.ContainerInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("resolving container %q: %w", ref, err)
	}
	return info.Container.ID, nil
}

// IsCanonicalContainerID reports whether s matches Docker's on-the-wire
// container ID format: exactly 64 lowercase hex characters. Exported so
// the host-side resolver factory in cmd/clawkercp can apply the same
// validation without re-implementing the predicate.
func IsCanonicalContainerID(s string) bool { return isCanonicalContainerID(s) }

// NewContainerResolver builds a ContainerResolver backed by a live Docker
// client and a fixed cgroup driver (detected once via DetectCgroupDriver).
// Cgroup paths come from the shared cgroupPathResolver: conventional rootful
// layout first, filesystem discovery (cached parent) for everything else.
//
// It honors the ContainerResolver contract precisely: a Docker NotFound is
// reported as (_, "", false, nil) — a nil error with exists=false — so the
// caller can tell "container is gone" from "we couldn't talk to Docker". When
// the missing reference is itself a canonical container ID, that ID is echoed
// back as the first return value so callers retain the identity even though
// Docker no longer knows it. Any other Docker API failure surfaces as err.
func NewContainerResolver(dc *docker.Client, cgroupDriver string) ContainerResolver {
	return newContainerResolverAt(dc, cgroupDriver, consts.SysFSCgroupPath)
}

// newContainerResolverAt is NewContainerResolver under an explicit hierarchy
// root, so tests can stage a fake cgroup tree.
func newContainerResolverAt(dc *docker.Client, cgroupDriver, root string) ContainerResolver {
	paths := &cgroupPathResolver{driver: cgroupDriver, root: root, mu: sync.Mutex{}, parent: ""}
	return func(ctx context.Context, ref string) (string, string, bool, error) {
		cid, err := ResolveContainerID(ctx, dc, ref)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				canonical := ""
				if IsCanonicalContainerID(ref) {
					canonical = ref
				}
				return canonical, "", false, nil
			}
			return "", "", false, err
		}
		cgroupPath, err := paths.path(cid)
		if err != nil {
			return "", "", false, fmt.Errorf("locating cgroup for container %s: %w", cid, err)
		}
		return cid, cgroupPath, true, nil
	}
}

func isCanonicalContainerID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
