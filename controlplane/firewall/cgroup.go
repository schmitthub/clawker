package firewall

// cgroup.go — helpers that translate between a Docker container identity
// and the BPF-attachable cgroup path that eBPF operations use. Callers
// detect the driver once at init (DetectCgroupDriver), cache it, and
// resolve paths internally — no external caller supplies a cgroup path.

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

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

// EBPFCgroupPath returns the BPF-attachable cgroup v2 path for a Docker
// container. Any driver other than "systemd" uses the cgroupfs layout.
func EBPFCgroupPath(cgroupDriver, containerID string) string {
	if cgroupDriver == "systemd" {
		return "/sys/fs/cgroup/system.slice/docker-" + containerID + ".scope"
	}
	return "/sys/fs/cgroup/docker/" + containerID
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
//
// It honors the ContainerResolver contract precisely: a Docker NotFound is
// reported as (_, "", false, nil) — a nil error with exists=false — so the
// caller can tell "container is gone" from "we couldn't talk to Docker". When
// the missing reference is itself a canonical container ID, that ID is echoed
// back as the first return value so callers retain the identity even though
// Docker no longer knows it. Any other Docker API failure surfaces as err.
func NewContainerResolver(dc *docker.Client, cgroupDriver string) ContainerResolver {
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
		return cid, EBPFCgroupPath(cgroupDriver, cid), true, nil
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
