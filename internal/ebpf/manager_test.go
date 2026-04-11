package ebpf

import (
	"strings"
	"testing"
)

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
