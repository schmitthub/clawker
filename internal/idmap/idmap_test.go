package idmap_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/idmap"
)

// The bigdaddy numbers are the live-probe golden values: user openclaw is
// uid 1003 gid 1004 with subuid/subgid base 296608, and the proven mapping
// is 1003→297610 and 1004→297611 (base + n - 1, the documented rootless
// formula for container ids ≥ 1).
const (
	bigdaddyUID  = 1003
	bigdaddyGID  = 1004
	bigdaddyKUID = 297610
	bigdaddyKGID = 297611
)

const bigdaddySubIDs = "openclaw:296608:65536\n"

func TestComputeMapping_BigdaddyGolden(t *testing.T) {
	t.Parallel()

	m, err := idmap.ComputeMapping(idmap.MappingInputs{
		OwnerUID: bigdaddyUID,
		OwnerGID: bigdaddyGID,
		UserName: "openclaw",
		UserUID:  bigdaddyUID,
		Subuid:   bigdaddySubIDs,
		Subgid:   bigdaddySubIDs,
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(bigdaddyUID), m.FromUID)
	assert.Equal(t, uint32(bigdaddyKUID), m.ToUID)
	assert.Equal(t, uint32(bigdaddyGID), m.FromGID)
	assert.Equal(t, uint32(bigdaddyKGID), m.ToGID)
}

// Subordinate files may key rows by numeric uid instead of the user name;
// both spellings identify the same ranges.
func TestComputeMapping_NumericSubIDKey(t *testing.T) {
	t.Parallel()

	m, err := idmap.ComputeMapping(idmap.MappingInputs{
		OwnerUID: bigdaddyUID,
		OwnerGID: bigdaddyGID,
		UserName: "openclaw",
		UserUID:  bigdaddyUID,
		Subuid:   "1003:296608:65536\n",
		Subgid:   "1003:296608:65536\n",
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(bigdaddyKUID), m.ToUID)
	assert.Equal(t, uint32(bigdaddyKGID), m.ToGID)
}

// Multiple ranges concatenate in file order: container id n lands at offset
// n-1 within the concatenation, exactly as rootlesskit walks them.
func TestComputeMapping_MultipleRanges(t *testing.T) {
	t.Parallel()

	// Offset for uid 1003 is 1002; the first range holds offsets 0-999,
	// so the target sits 2 into the second range.
	subids := "dev:100000:1000\ndev:500000:65536\n"

	m, err := idmap.ComputeMapping(idmap.MappingInputs{
		OwnerUID: bigdaddyUID,
		OwnerGID: bigdaddyGID,
		UserName: "dev",
		UserUID:  bigdaddyUID,
		Subuid:   subids,
		Subgid:   subids,
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(500002), m.ToUID)
}

// TestComputeMapping_LastIDInRange pins the exact range end: a 65536-count
// range holds container ids 1..65536, so 65536 maps to the range's final
// subordinate id. Paired with the one-past case in the error table, this
// brackets the offset < count comparison from both sides.
func TestComputeMapping_LastIDInRange(t *testing.T) {
	t.Parallel()

	m, err := idmap.ComputeMapping(idmap.MappingInputs{
		OwnerUID: 65536,
		OwnerGID: bigdaddyGID,
		UserName: "openclaw",
		UserUID:  bigdaddyUID,
		Subuid:   bigdaddySubIDs,
		Subgid:   bigdaddySubIDs,
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(296608+65535), m.ToUID)
}

func TestComputeMapping_Errors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      idmap.MappingInputs
		wantErr string
	}{
		{
			name: "root-owned workspace is refused",
			in: idmap.MappingInputs{
				OwnerUID: 0, OwnerGID: 0,
				UserName: "openclaw", UserUID: bigdaddyUID,
				Subuid: bigdaddySubIDs, Subgid: bigdaddySubIDs,
			},
			wantErr: "root",
		},
		{
			name: "no subuid entry for the user",
			in: idmap.MappingInputs{
				OwnerUID: bigdaddyUID, OwnerGID: bigdaddyGID,
				UserName: "openclaw", UserUID: bigdaddyUID,
				Subuid: "otheruser:100000:65536\n", Subgid: bigdaddySubIDs,
			},
			wantErr: "no subordinate",
		},
		{
			name: "owner uid beyond the subordinate ranges",
			in: idmap.MappingInputs{
				OwnerUID: 70000, OwnerGID: bigdaddyGID,
				UserName: "openclaw", UserUID: bigdaddyUID,
				Subuid: bigdaddySubIDs, Subgid: bigdaddySubIDs,
			},
			wantErr: "outside",
		},
		{
			// gid 0 gets its own refusal: without it, the n-1 offset
			// underflows and errors with range-walk debris instead of an
			// answer that names the problem.
			name: "root-group-owned workspace is refused",
			in: idmap.MappingInputs{
				OwnerUID: bigdaddyUID, OwnerGID: 0,
				UserName: "openclaw", UserUID: bigdaddyUID,
				Subuid: bigdaddySubIDs, Subgid: bigdaddySubIDs,
			},
			wantErr: "root-group-owned",
		},
		{
			// The exact first-invalid id: a 65536-count range holds
			// container ids 1..65536 (offsets 0..65535); 65537 is one past
			// it. An off-by-one to <= would map it one past the delegated
			// range — into someone else's subordinate block.
			name: "owner uid one past the range end",
			in: idmap.MappingInputs{
				OwnerUID: 65537, OwnerGID: bigdaddyGID,
				UserName: "openclaw", UserUID: bigdaddyUID,
				Subuid: bigdaddySubIDs, Subgid: bigdaddySubIDs,
			},
			wantErr: "outside",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := idmap.ComputeMapping(tc.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// Malformed lines and comments are skipped, not fatal — the file is
// system-owned and one stray line must not break every clawker create.
func TestComputeMapping_SkipsMalformedLines(t *testing.T) {
	t.Parallel()

	subids := "# comment\n\nbroken line\nopenclaw:296608:65536\n"

	m, err := idmap.ComputeMapping(idmap.MappingInputs{
		OwnerUID: bigdaddyUID,
		OwnerGID: bigdaddyGID,
		UserName: "openclaw",
		UserUID:  bigdaddyUID,
		Subuid:   subids,
		Subgid:   subids,
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(bigdaddyKUID), m.ToUID)
}

func TestViewPath(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	a := idmap.ViewPath(base, "/home/dev/projects/clawker")
	b := idmap.ViewPath(base, "/home/dev/other/clawker")

	assert.True(t, strings.HasPrefix(a, base+string(filepath.Separator)))
	assert.Contains(t, filepath.Base(a), "clawker", "view dir should stay human-readable")
	assert.NotEqual(t, a, b, "same basename, different path must yield distinct views")
	assert.Equal(t, a, idmap.ViewPath(base, "/home/dev/projects/clawker"), "must be deterministic")
}

// testMount builds the three fields the rewrite reads.
//
//nolint:exhaustruct // moby wire type: only the fields the rewrite touches matter
func testMount(typ mount.Type, src, tgt string) mount.Mount {
	return mount.Mount{Type: typ, Source: src, Target: tgt}
}

func TestRewriteMounts(t *testing.T) {
	t.Parallel()

	root := "/home/dev/proj"
	view := "/home/dev/.local/share/clawker/idmap/proj-abc123"

	in := []mount.Mount{
		testMount(mount.TypeBind, root, "/home/clawker/proj"),
		testMount(mount.TypeBind, root+"/home/.openclaw", "/home/clawker/.openclaw"),
		testMount(mount.TypeBind, "/somewhere/else", "/other"),
		testMount(mount.TypeBind, root+"ects", "/lookalike"), // prefix trap: /home/dev/projects
		testMount(mount.TypeVolume, "clawker-config", "/config"),
	}

	out, n := idmap.RewriteMounts(in, root, view)
	assert.Equal(t, 2, n)
	assert.Equal(t, view, out[0].Source)
	assert.Equal(t, view+"/home/.openclaw", out[1].Source)
	assert.Equal(t, "/somewhere/else", out[2].Source, "sources outside the root stay untouched")
	assert.Equal(t, root+"ects", out[3].Source, "sibling with shared prefix stays untouched")
	assert.Equal(t, "clawker-config", out[4].Source, "non-bind mounts stay untouched")
	assert.Equal(t, root, in[0].Source, "input slice must not be mutated")
}

func TestRewriteBinds(t *testing.T) {
	t.Parallel()

	root := "/home/dev/proj"
	view := "/view"

	in := []string{
		root + ":/w",
		root + "/sub:/s:ro",
		"/elsewhere:/e",
		"named-volume:/v",
	}

	out, n := idmap.RewriteBinds(in, root, view)
	assert.Equal(t, 2, n)
	assert.Equal(t, "/view:/w", out[0])
	assert.Equal(t, "/view/sub:/s:ro", out[1])
	assert.Equal(t, "/elsewhere:/e", out[2])
	assert.Equal(t, "named-volume:/v", out[3])
}

func TestIDPairRoundTrip(t *testing.T) {
	t.Parallel()

	s := idmap.FormatIDPair(bigdaddyUID, bigdaddyKUID)
	assert.Equal(t, "1003:297610", s)

	from, to, err := idmap.ParseIDPair(s)
	require.NoError(t, err)
	assert.Equal(t, uint32(bigdaddyUID), from)
	assert.Equal(t, uint32(bigdaddyKUID), to)

	_, _, err = idmap.ParseIDPair("nonsense")
	require.Error(t, err)
	_, _, err = idmap.ParseIDPair("1:2:3")
	require.Error(t, err)
}
