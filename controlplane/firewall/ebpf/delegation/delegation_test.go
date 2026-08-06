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

// TestOwnerParams pins the delegated filesystem's ownership shape: born
// owned 0700 by the control plane through filesystem parameters, with uid
// and gid learned from the handoff socket's peer credentials — never a
// chown, never a configured value.
func TestOwnerParams(t *testing.T) {
	t.Parallel()

	values := map[string]string{}
	for _, p := range delegation.OwnerParams(1003, 1004) {
		values[p.Name] = p.Value
	}

	assert.Equal(t, map[string]string{
		"mode": "0700",
		"uid":  "1003",
		"gid":  "1004",
	}, values)
}
