package firewall

// drift.go — INV-B2-016 drift guard. Every direct FirewallEnable call and
// every FirewallBypass dead-man-timer restore resolves the container's
// CURRENT cgroup path via Docker before acting, so routing can never land
// on a stale cgroup_id that now belongs to a sibling container after a
// crash/restart cycle.

import (
	"context"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// dockerLookupTimeout bounds the Docker API call the drift guard makes to
// verify container state. A stuck Docker socket must not be able to wedge
// a bypass timer indefinitely.
const dockerLookupTimeout = 5 * time.Second

// resolveBypassCgroupID returns the cgroup ID the dead-man timer should
// call Enable against, consulting Docker as the source of truth.
//
//  1. Container alive per Docker → fresh cgroup ID from Docker's path.
//  2. Container gone per Docker  → stored cgroup ID, so the caller's
//     subsequent Enable still lands on the right key and clears the
//     orphan bypass_map entry via the ebpf manager's clearBypass.
//  3. Docker API unreachable     → stored cgroup ID (fail-closed).
//
// Enable is NEVER skipped — every branch returns an ID.
func resolveBypassCgroupID(
	entry *bypassEntry,
	resolve ContainerResolver,
	cgroupIDFn func(string) (uint64, error),
	log *logger.Logger,
) uint64 {
	if entry.containerID == "" {
		// Bypass validates container_id up-front; reaching this branch
		// means the proto validator regressed. Log loudly so the
		// regression is greppable, but still return the fallback so
		// enforcement is restored on the next attempt.
		log.Error().
			Uint64("fallback_cgroup_id", entry.cgroupID).
			Msg("bypass timer: empty container_id (proto validator regression?), using stored cgroup ID")
		return entry.cgroupID
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerLookupTimeout)
	defer cancel()

	_, cgroupPath, exists, err := resolve(ctx, entry.containerID)
	if err != nil {
		log.Warn().Err(err).
			Str("container_id", entry.containerID).
			Uint64("fallback_cgroup_id", entry.cgroupID).
			Msg("bypass timer: Docker API error, using stored cgroup ID")
		return entry.cgroupID
	}
	if !exists {
		log.Info().
			Str("container_id", entry.containerID).
			Uint64("fallback_cgroup_id", entry.cgroupID).
			Msg("bypass timer: container gone per Docker, clearing stale bypass entry")
		return entry.cgroupID
	}

	freshID, err := cgroupIDFn(cgroupPath)
	if err != nil {
		log.Warn().Err(err).
			Str("docker_cgroup_path", cgroupPath).
			Uint64("fallback_cgroup_id", entry.cgroupID).
			Msg("bypass timer: cgroup stat failed, using stored cgroup ID")
		return entry.cgroupID
	}
	if freshID != entry.cgroupID {
		log.Warn().
			Uint64("stored_cgroup_id", entry.cgroupID).
			Uint64("fresh_cgroup_id", freshID).
			Str("container_id", entry.containerID).
			Msg("cgroup_id drift detected, using fresh ID")
	}
	return freshID
}
