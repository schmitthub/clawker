package ebpf

import (
	"time"

	"github.com/rs/zerolog"
)

// EBPFContainerEnrolled is published on the firewall's enroll topic after
// a successful FirewallEnable enrolls a container into the BPF
// container_map. The CgroupID and ContainerID together identify the
// enrolled (cgroup, container) binding for downstream consumers.
//
// Producer: firewall.Handler.FirewallEnable — emits AFTER container_map
// write + program attach succeed. FirewallInit's re-enrollment sweep
// at CP startup hits the same code path, so a single subscription
// hydrates both runtime and startup-time consumers without a separate
// backfill.
//
// Consumer: netlogger.Service uses it to drive its cgroup_id →
// {container_id, agent, project} LabelCache; it does ONE
// docker.ContainerInspect on each enroll to resolve the labels. The
// eviction half is the existing dockerevents.DockerEvent — netlogger
// filters for container/{die,destroy} and evicts the cache entry by
// container_id. No EBPFContainerRemoved event is added; the die signal
// is already on the bus and duplicating it would be redundant.
type EBPFContainerEnrolled struct {
	CgroupID    uint64
	ContainerID string
	At          time.Time
}

func (e EBPFContainerEnrolled) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Uint64("cgroup_id", e.CgroupID)
}
