package delegation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/controlplane/firewall/ebpf/delegation"
)

// TestMasks pins the two mask decisions that are invisible from clawker's own
// BPF source and would look like mistakes to a future reader: array is
// present although the object never declares one, and kprobe is absent
// although cilium/ebpf probes with it.
func TestMasks(t *testing.T) {
	t.Parallel()

	assert.Contains(t, delegation.Maps, "array",
		"array is required: cilium/ebpf's object-name probe creates an ARRAY map, "+
			"and refusing it makes the library drop names from every map and program")
	assert.NotContains(t, delegation.Progs, "kprobe",
		"kprobe is a tracing grant and the real object loads without it")
	assert.NotContains(t, delegation.Progs, "socket_filter",
		"socket_filter buys richer verifier output, not a working load")
}

// TestParams pins that every mask travels. Both halves of the privilege
// boundary iterate Params rather than naming the four parameters, so a mask
// dropped here is a delegation silently narrower than the object needs — the
// load fails much later with a permission error naming nothing.
func TestParams(t *testing.T) {
	t.Parallel()

	values := map[string]string{}
	for _, p := range delegation.Params() {
		values[p.Name] = p.Value
	}

	assert.Equal(t, map[string]string{
		"delegate_cmds":    delegation.Cmds,
		"delegate_maps":    delegation.Maps,
		"delegate_progs":   delegation.Progs,
		"delegate_attachs": delegation.Attachs,
	}, values)
}

// TestMountOptions pins the pin filesystem's ownership shape. The control
// plane mounts it in-process when it can and the elevated helper mounts it
// when it cannot; a divergence would give the two deployments differently
// owned filesystems for the same path.
func TestMountOptions(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "mode=0700,uid=1003,gid=1004", delegation.MountOptions(1003, 1004))
}
