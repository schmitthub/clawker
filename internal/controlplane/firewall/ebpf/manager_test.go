package ebpf

import (
	"encoding/binary"
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"

	"github.com/schmitthub/clawker/internal/logger"
)

// TestEgressEvent_SizeMatchesABI mirrors the C-side
// _Static_assert(sizeof(struct egress_event) == 48) on the Go side. If
// bpf2go's generated struct ever drifts (toolchain upgrade, layout
// semantic change, reordered field, padding mistake), this fails before
// the netlogger reader can read a misaligned record off the ringbuf.
func TestEgressEvent_SizeMatchesABI(t *testing.T) {
	t.Parallel()
	if got := binary.Size(EgressEvent{}); got != 48 {
		t.Fatalf("EgressEvent on-wire size = %d; want 48 — C struct egress_event has drifted from the Go side", got)
	}
}

// requireBPF skips a test if the kernel/container lacks the perms needed
// to create in-memory BPF maps. Used for tests that need real ebpf.Map
// handles in m.objs. CI on a privileged Linux runner never skips; dev
// containers without CAP_BPF skip silently.
func requireBPF(t *testing.T) {
	t.Helper()
	_ = rlimit.RemoveMemlock()
	m, err := ebpf.NewMap(&ebpf.MapSpec{
		Type:       ebpf.Hash,
		KeySize:    8,
		ValueSize:  8,
		MaxEntries: 1,
	})
	if err != nil {
		t.Skipf("BPF unavailable in this environment: %v", err)
	}
	m.Close()
}

// fakeBypassMap is an in-memory bypassMap used to exercise clearBypass
// without a live kernel. It lets tests stage specific error conditions
// (e.g. EPERM, EINVAL) that would otherwise require privileged BPF
// operations.
type fakeBypassMap struct {
	// entries holds the current bypass state. A key is "present" if it
	// exists in the map; values are opaque to clearBypass (the BPF fast
	// path uses uint8(1) in production, we just mirror that).
	entries map[uint64]uint8
	// forcedErr, if non-nil, is returned from Delete regardless of whether
	// the key exists. Used to simulate kernel-level failures like EPERM.
	forcedErr error
	// deleteCalls records every Delete invocation for assertions.
	deleteCalls []uint64
}

func newFakeBypassMap() *fakeBypassMap {
	return &fakeBypassMap{entries: make(map[uint64]uint8)}
}

func (f *fakeBypassMap) Put(key uint64, val uint8) {
	f.entries[key] = val
}

func (f *fakeBypassMap) Has(key uint64) bool {
	_, ok := f.entries[key]
	return ok
}

func (f *fakeBypassMap) Delete(key any) error {
	k, ok := key.(uint64)
	if !ok {
		return errors.New("fakeBypassMap: key must be uint64")
	}
	f.deleteCalls = append(f.deleteCalls, k)
	if f.forcedErr != nil {
		return f.forcedErr
	}
	if _, present := f.entries[k]; !present {
		// Mirror the real *ebpf.Map contract: deleting a missing key
		// surfaces ErrKeyNotExist so clearBypass can treat it as success.
		return ebpf.ErrKeyNotExist
	}
	delete(f.entries, k)
	return nil
}

// TestDomainHash_CaseInsensitive asserts that DomainHash normalizes via
// strings.ToLower, so the firewall route_map writer and the dnsbpf CoreDNS
// plugin agree on the same hash for the same domain regardless of the user's
// capitalization in the rule Dst. Regression guard for the mismatch where
// firewall.DomainHash lowercased but ebpf.DomainHash did not, causing BPF
// route lookups to miss for mixed-case rules like "GitHub.com".
func TestDomainHash_CaseInsensitive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		variants  []string
		mustMatch string
	}{
		{
			name:      "mixed case matches lower",
			variants:  []string{"github.com", "GitHub.com", "GITHUB.COM", "github.COM"},
			mustMatch: "github.com",
		},
		{
			name:      "wildcard zone",
			variants:  []string{".Example.COM", ".example.com"},
			mustMatch: ".example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := DomainHash(tc.mustMatch)
			for _, v := range tc.variants {
				if got := DomainHash(v); got != want {
					t.Errorf("DomainHash(%q) = %d; want %d (DomainHash(%q))",
						v, got, want, tc.mustMatch)
				}
			}
		})
	}
}

func TestValidateCgroupPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{
			name: "systemd docker scope",
			in:   "/sys/fs/cgroup/system.slice/docker-abc123.scope",
			want: "/sys/fs/cgroup/system.slice/docker-abc123.scope",
		},
		{
			name: "cgroupfs docker path",
			in:   "/sys/fs/cgroup/docker/abc123",
			want: "/sys/fs/cgroup/docker/abc123",
		},
		{
			name: "unclean but valid (double slash and dot)",
			in:   "/sys/fs/cgroup//system.slice/./docker-abc.scope",
			want: "/sys/fs/cgroup/system.slice/docker-abc.scope",
		},
		{
			name:    "empty",
			in:      "",
			wantErr: "empty",
		},
		{
			name:    "dotdot traversal from inside root",
			in:      "/sys/fs/cgroup/../etc/passwd",
			wantErr: "'..'",
		},
		{
			name:    "dotdot traversal from outside",
			in:      "../../etc/passwd",
			wantErr: "'..'",
		},
		{
			name:    "absolute path outside root",
			in:      "/etc/passwd",
			wantErr: "under /sys/fs/cgroup/",
		},
		{
			name:    "relative path",
			in:      "cgroup/foo",
			wantErr: "under /sys/fs/cgroup/",
		},
		{
			name:    "root itself without trailing slash",
			in:      "/sys/fs/cgroup",
			wantErr: "under /sys/fs/cgroup/",
		},
		{
			name:    "null byte injection",
			in:      "/sys/fs/cgroup/system.slice/x\x00y",
			wantErr: "illegal characters",
		},
		{
			name:    "newline injection",
			in:      "/sys/fs/cgroup/system.slice/x\ny",
			wantErr: "illegal characters",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateCgroupPath(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil (result=%q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClearBypass_DeletesExistingEntry is the regression test for the
// `firewall bypass --stop` re-enforcement fix (commit 6a00a212). Enable()
// must remove any lingering BypassMap entry so the BPF fast path actually
// re-enforces after a bypass is stopped. Before that commit the bypass flag
// was orphaned and the container silently kept unrestricted egress.
func TestClearBypass_DeletesExistingEntry(t *testing.T) {
	t.Parallel()
	const cgroupID uint64 = 123
	fake := newFakeBypassMap()
	fake.Put(cgroupID, 1)

	if !fake.Has(cgroupID) {
		t.Fatalf("precondition: expected bypass entry for cgroup %d", cgroupID)
	}

	if err := clearBypass(fake, cgroupID, logger.Nop()); err != nil {
		t.Fatalf("clearBypass returned error: %v", err)
	}

	if fake.Has(cgroupID) {
		t.Errorf("bypass entry for cgroup %d still present after clearBypass", cgroupID)
	}
	if len(fake.deleteCalls) != 1 || fake.deleteCalls[0] != cgroupID {
		t.Errorf("expected exactly one Delete(%d) call, got %v", cgroupID, fake.deleteCalls)
	}
}

// TestClearBypass_IgnoresMissingEntry asserts the common case (no bypass
// ever set) is a silent no-op. Enable() is called on every container start
// and must not return an error when there is nothing to clear.
func TestClearBypass_IgnoresMissingEntry(t *testing.T) {
	t.Parallel()
	const cgroupID uint64 = 456
	fake := newFakeBypassMap()

	if fake.Has(cgroupID) {
		t.Fatalf("precondition: fake bypass map should be empty")
	}

	if err := clearBypass(fake, cgroupID, logger.Nop()); err != nil {
		t.Errorf("clearBypass on missing entry returned error: %v (expected nil)", err)
	}
	if len(fake.deleteCalls) != 1 {
		t.Errorf("expected one Delete call even when key missing, got %d", len(fake.deleteCalls))
	}
}

// TestClearBypass_WrapsOtherErrors asserts that non-ErrKeyNotExist failures
// (e.g. EPERM from missing CAP_BPF, EINVAL from a corrupted map fd) surface
// as errors instead of being silently swallowed as they were in commit
// 6a00a212. Enable() currently treats the returned error as non-fatal, but
// the error must be observable so the Warn log fires and future callers can
// make a different decision.
func TestClearBypass_WrapsOtherErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
	}{
		{name: "EPERM", err: syscall.EPERM},
		{name: "EINVAL", err: syscall.EINVAL},
		{name: "ENOMEM", err: syscall.ENOMEM},
		{name: "wrapped EPERM", err: errors.Join(errors.New("ebpf: delete"), syscall.EPERM)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeBypassMap()
			fake.forcedErr = tc.err

			err := clearBypass(fake, 789, logger.Nop())
			if err == nil {
				t.Fatalf("expected error from forced %v, got nil", tc.err)
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("expected errors.Is(err, %v) to be true; got %v", tc.err, err)
			}
			// ErrKeyNotExist must not be returned for unrelated errors —
			// that would trick Enable into thinking the bypass is cleared.
			if errors.Is(err, ebpf.ErrKeyNotExist) {
				t.Errorf("forced %v must not match ErrKeyNotExist", tc.err)
			}
		})
	}
}

// fakeDNSMap is the dns_cache counterpart to fakeBypassMap — it holds
// uint32 keys (dns_cache is keyed by IPv4 address as uint32) and records
// every Delete invocation so deleteExpiredDNSEntries' count-vs-try
// semantics can be asserted. missing is the set of keys that return
// ErrKeyNotExist on Delete; forcedErr sets a non-ENOENT error for all
// other keys (simulating EPERM/EINVAL).
type fakeDNSMap struct {
	missing     map[uint32]bool
	forcedErr   error
	deleteCalls []uint32
}

func (f *fakeDNSMap) Delete(key any) error {
	k, ok := key.(uint32)
	if !ok {
		return errors.New("fakeDNSMap: key must be uint32")
	}
	f.deleteCalls = append(f.deleteCalls, k)
	if f.missing[k] {
		return ebpf.ErrKeyNotExist
	}
	if f.forcedErr != nil {
		return f.forcedErr
	}
	return nil
}

// TestDeleteExpiredDNSEntries_CountsOnlyRealAndENOENTSuccess is the
// regression guard for the "return value lies" bug in GarbageCollectDNS.
// The old code returned len(expired) regardless of whether the deletes
// actually succeeded, so the metric misrepresented GC effectiveness.
// The helper now counts entries that ended up cleared (including
// ErrKeyNotExist, since the end-state matches the intent) and excludes
// real failures like EPERM/EINVAL.
func TestDeleteExpiredDNSEntries_CountsOnlyRealAndENOENTSuccess(t *testing.T) {
	t.Parallel()
	// Scenario: 5 expired keys.
	//   - keys 1, 2 delete cleanly → counted
	//   - key 3 returns ErrKeyNotExist (another actor raced us) → counted
	//     (end-state is correct: the entry is gone)
	//   - keys 4, 5 return EPERM → NOT counted, logged at Debug
	fake := &fakeDNSMap{
		missing:   map[uint32]bool{3: true},
		forcedErr: nil,
	}
	// First assert the happy path (keys 1, 2, 3) yields 3 cleared.
	cleared := deleteExpiredDNSEntries(fake, []uint32{1, 2, 3}, logger.Nop())
	if cleared != 3 {
		t.Errorf("happy path: expected 3 cleared, got %d", cleared)
	}
	if len(fake.deleteCalls) != 3 {
		t.Errorf("expected 3 Delete calls, got %d", len(fake.deleteCalls))
	}

	// Now force EPERM and assert keys 4, 5 are NOT counted.
	fake.deleteCalls = nil
	fake.missing = nil
	fake.forcedErr = syscall.EPERM
	cleared = deleteExpiredDNSEntries(fake, []uint32{4, 5}, logger.Nop())
	if cleared != 0 {
		t.Errorf("EPERM path: expected 0 cleared (deletes failed), got %d", cleared)
	}
	if len(fake.deleteCalls) != 2 {
		t.Errorf("expected 2 Delete attempts, got %d", len(fake.deleteCalls))
	}
}

// TestDeleteExpiredDNSEntries_EmptyReturnsZero is the trivial but
// load-bearing case: no expired keys means zero cleared, no map
// interaction. Locks in the happy-path boundary so a future refactor
// that adds an off-by-one or initializes cleared incorrectly would
// be caught immediately.
func TestDeleteExpiredDNSEntries_EmptyReturnsZero(t *testing.T) {
	t.Parallel()
	fake := &fakeDNSMap{}
	cleared := deleteExpiredDNSEntries(fake, nil, logger.Nop())
	if cleared != 0 {
		t.Errorf("empty input: expected 0 cleared, got %d", cleared)
	}
	if len(fake.deleteCalls) != 0 {
		t.Errorf("empty input must not invoke Delete; got %d calls", len(fake.deleteCalls))
	}
}

// TestCgroupID_RejectsMaliciousPath asserts CgroupID refuses to open a file
// whose path does not live under /sys/fs/cgroup/. This is the end-to-end
// counterpart to TestValidateCgroupPath: the validator is called from inside
// CgroupID before os.Open, so adversarial inputs (absolute paths outside
// the cgroup root, relative paths, `..` traversals, control characters)
// must never reach the filesystem. Regression guard for the CodeQL
// go/path-injection sanitizer chain.
func TestCgroupID_RejectsMaliciousPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{
			name:    "absolute path outside cgroup root",
			path:    "/etc/passwd",
			wantErr: "under /sys/fs/cgroup/",
		},
		{
			name:    "absolute path in proc",
			path:    "/proc/self/environ",
			wantErr: "under /sys/fs/cgroup/",
		},
		{
			name:    "relative path",
			path:    "cgroup/foo",
			wantErr: "under /sys/fs/cgroup/",
		},
		{
			name:    "dotdot traversal from inside root",
			path:    "/sys/fs/cgroup/../etc/shadow",
			wantErr: "'..'",
		},
		{
			name:    "dotdot escape sequence",
			path:    "../../etc/passwd",
			wantErr: "'..'",
		},
		{
			name:    "empty path",
			path:    "",
			wantErr: "empty",
		},
		{
			name:    "null byte injection",
			path:    "/sys/fs/cgroup/evil\x00/etc/passwd",
			wantErr: "illegal characters",
		},
		{
			name:    "newline injection",
			path:    "/sys/fs/cgroup/evil\netc",
			wantErr: "illegal characters",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, err := CgroupID(tc.path)
			if err == nil {
				t.Fatalf("CgroupID(%q) returned id=%d with no error; expected rejection", tc.path, id)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("CgroupID(%q) error = %v; want containing %q", tc.path, err, tc.wantErr)
			}
			// Defense-in-depth: the validator must short-circuit BEFORE
			// os.Open. An "opening cgroup:" prefix indicates CgroupID made
			// it past validation, which would defeat the sanitizer.
			if strings.Contains(err.Error(), "opening cgroup:") {
				t.Errorf("CgroupID(%q) reached os.Open before rejection: %v", tc.path, err)
			}
		})
	}
}

// TestManager_FlushAll_NilObjsNoOp asserts FlushAll on a Manager with
// zero-value objs neither panics nor returns an error. The drain code
// for every per-cgroup map gates on `m.objs.X != nil` so that the
// CP-startup degraded path (Load failed, manager pointer still wired)
// can call FlushAll during shutdown without compounding the failure.
func TestManager_FlushAll_NilObjsNoOp(t *testing.T) {
	t.Parallel()
	m := NewManager(logger.Nop())
	if err := m.FlushAll(); err != nil {
		t.Errorf("FlushAll on nil objs = %v; want nil", err)
	}
}

// TestManager_FlushAll_DrainsRatelimitMaps verifies the new ratelimit_state
// + ratelimit_drops drain logic added in Task 1. Constructs the two maps
// directly (no Load → no pinning → no privileges beyond CAP_BPF) and
// wires them into a Manager, populates entries, then asserts FlushAll
// leaves both maps empty. CP-restart determinism — token buckets must
// not carry across.
func TestManager_FlushAll_DrainsRatelimitMaps(t *testing.T) {
	t.Parallel()
	requireBPF(t)

	state, err := ebpf.NewMap(&ebpf.MapSpec{
		Type:       ebpf.LRUHash,
		KeySize:    8,
		ValueSize:  16, // matches clawkerRatelimitStateVal (2× uint64)
		MaxEntries: 8,
	})
	if err != nil {
		t.Fatalf("create ratelimit_state map: %v", err)
	}
	defer state.Close()

	drops, err := ebpf.NewMap(&ebpf.MapSpec{
		Type:       ebpf.Hash,
		KeySize:    8,
		ValueSize:  8,
		MaxEntries: 8,
	})
	if err != nil {
		t.Fatalf("create ratelimit_drops map: %v", err)
	}
	defer drops.Close()

	m := NewManager(logger.Nop())
	m.objs.RatelimitState = state
	m.objs.RatelimitDrops = drops

	for _, cg := range []uint64{1, 2, 3} {
		if err := state.Update(cg, clawkerRatelimitStateVal{Tokens: 7}, ebpf.UpdateAny); err != nil {
			t.Fatalf("seed ratelimit_state[%d]: %v", cg, err)
		}
		if err := drops.Update(cg, uint64(11), ebpf.UpdateAny); err != nil {
			t.Fatalf("seed ratelimit_drops[%d]: %v", cg, err)
		}
	}

	if err := m.FlushAll(); err != nil {
		t.Fatalf("FlushAll: %v", err)
	}

	assertMapEmpty(t, "ratelimit_state", state, func() (any, any) {
		var k uint64
		var v clawkerRatelimitStateVal
		return &k, &v
	})
	assertMapEmpty(t, "ratelimit_drops", drops, func() (any, any) {
		var k, v uint64
		return &k, &v
	})
}

// assertMapEmpty fails the test if the given BPF map still has any
// entries. Iteration after FlushAll should be a no-op walk.
func assertMapEmpty(t *testing.T, name string, m *ebpf.Map, allocKV func() (any, any)) {
	t.Helper()
	k, v := allocKV()
	iter := m.Iterate()
	for iter.Next(k, v) {
		t.Errorf("%s still has entries after FlushAll", name)
		return
	}
	if err := iter.Err(); err != nil {
		t.Errorf("%s iterate after FlushAll: %v", name, err)
	}
}
