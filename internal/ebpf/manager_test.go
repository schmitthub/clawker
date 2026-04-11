package ebpf

import (
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/cilium/ebpf"

	"github.com/schmitthub/clawker/internal/logger"
)

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
