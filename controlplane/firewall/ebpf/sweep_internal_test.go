package ebpf

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cilium/ebpf"

	"github.com/schmitthub/clawker/internal/logger"
)

// The sweep must never remove a pin it cannot read: the last pin of a link
// is the link, and losing it detaches a live agent's enforcement.
func TestCleanupStaleLinks_UnreadablePinKeptAndLive(t *testing.T) {
	t.Parallel()
	m := NewManager(logger.Nop())
	m.pinPath = t.TempDir()
	for _, name := range []string{"link_connect4_42", "link_connect4_nope", "container_map"} {
		if err := os.WriteFile(filepath.Join(m.pinPath, name), []byte("garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	live, err := m.cleanupStaleLinks()
	if err != nil {
		t.Fatalf("cleanupStaleLinks: %v", err)
	}
	if _, ok := live[42]; !ok || len(live) != 1 {
		t.Fatalf("live = %v; want exactly {42}: an unreadable pin counts as live, a malformed name has no ID", live)
	}
	for _, name := range []string{"link_connect4_42", "link_connect4_nope", "container_map"} {
		if _, statErr := os.Stat(filepath.Join(m.pinPath, name)); statErr != nil {
			t.Errorf("%s removed by the sweep: %v", name, statErr)
		}
	}
}

func TestCleanupStaleLinks_MissingPinDirIsUnknown(t *testing.T) {
	t.Parallel()
	m := NewManager(logger.Nop())
	m.pinPath = filepath.Join(t.TempDir(), "absent")
	live, err := m.cleanupStaleLinks()
	if err == nil || live != nil {
		t.Fatalf("cleanupStaleLinks = (%v, %v); want (nil, error): the live set is unknown", live, err)
	}
}

// requirePrivileged gates the real-link sweep test: Load pins on bpffs and
// Install attaches to cgroups, which needs root (CAP_BPF + cgroup write)
// and cgroup v2. PRIVILEGED_TESTS is the same switch make test-bpf sets.
func requirePrivileged(t *testing.T) {
	t.Helper()
	if os.Getenv("PRIVILEGED_TESTS") == "" || os.Geteuid() != 0 {
		t.Skip("set PRIVILEGED_TESTS=1 and run as root to exercise the pinned-link sweep")
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Skipf("cgroup v2 unavailable: %v", err)
	}
	if _, err := os.Stat("/sys/fs/bpf"); err != nil {
		t.Skipf("bpffs unavailable: %v", err)
	}
}

func testSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// sweepCgroups creates a parent cgroup with live and dead children under
// /sys/fs/cgroup and removes whatever is left at cleanup.
func sweepCgroups(t *testing.T, suffix string) (string, string) {
	t.Helper()
	cgRoot := filepath.Join("/sys/fs/cgroup", "clawker-sweep-"+suffix)
	livePath, deadPath := filepath.Join(cgRoot, "live"), filepath.Join(cgRoot, "dead")
	for _, p := range []string{cgRoot, livePath, deadPath} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	t.Cleanup(func() {
		for _, p := range []string{livePath, deadPath, cgRoot} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Logf("rmdir %s: %v", p, err)
			}
		}
	})
	return livePath, deadPath
}

// loadedManager loads the production programs pinned under a private bpffs
// directory and tears everything down at cleanup.
func loadedManager(t *testing.T, suffix string) *Manager {
	t.Helper()
	m := NewManager(logger.Nop())
	m.pinPath = filepath.Join("/sys/fs/bpf", "clawker-sweep-"+suffix)
	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() {
		if err := m.FlushAll(); err != nil {
			t.Logf("FlushAll: %v", err)
		}
		if err := m.Close(); err != nil {
			t.Logf("Close: %v", err)
		}
		if err := os.RemoveAll(m.pinPath); err != nil {
			t.Logf("remove pins: %v", err)
		}
	})
	return m
}

// installCgroup enrolls one cgroup with a fixed test config and returns its ID.
func installCgroup(t *testing.T, m *Manager, path string) uint64 {
	t.Helper()
	cfg, err := NewContainerConfig(ContainerConfigParams{
		EnvoyIP: "172.30.0.200", CoreDNSIP: "172.30.0.201", GatewayIP: "172.30.0.1", CIDR: "172.30.0.0/16",
		EgressPort: 10000,
		CPIP:       "172.30.0.202", CPAgentPort: 7444, CPHydraPort: 4444,
	})
	if err != nil {
		t.Fatalf("NewContainerConfig: %v", err)
	}
	id, err := CgroupID(path)
	if err != nil {
		t.Fatalf("CgroupID(%s): %v", path, err)
	}
	if installErr := m.Install(id, path, cfg); installErr != nil {
		t.Fatalf("Install(%s): %v", path, installErr)
	}
	return id
}

// sweepUntilDetached runs the sweep until the kernel has detached deadID's
// links (the detach lands after an RCU grace period) or 5s pass.
func sweepUntilDetached(t *testing.T, m *Manager, deadID uint64) map[uint64]struct{} {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		live, err := m.cleanupStaleLinks()
		if err != nil {
			t.Fatalf("cleanupStaleLinks: %v", err)
		}
		if _, dead := live[deadID]; !dead || time.Now().After(deadline) {
			return live
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestCleanupStaleLinks_Privileged_KeepsLiveDropsDead attaches the real
// programs to two cgroups, destroys one, and checks the sweep keeps the live
// agent's pins and container_map row while removing the dead one's.
func TestCleanupStaleLinks_Privileged_KeepsLiveDropsDead(t *testing.T) {
	requirePrivileged(t)
	suffix := testSuffix(t)
	livePath, deadPath := sweepCgroups(t, suffix)
	m := loadedManager(t, suffix)
	liveID := installCgroup(t, m, livePath)
	deadID := installCgroup(t, m, deadPath)

	// The kernel detaches every link on the cgroup when it is destroyed.
	if err := os.Remove(deadPath); err != nil {
		t.Fatalf("rmdir dead cgroup: %v", err)
	}
	live := sweepUntilDetached(t, m, deadID)
	if _, ok := live[liveID]; !ok || len(live) != 1 {
		t.Fatalf("live = %v; want exactly {%d}", live, liveID)
	}
	for _, a := range m.cgroupAttachments() {
		livePin := filepath.Join(m.pinPath, fmt.Sprintf("link_%s_%d", a.name, liveID))
		deadPin := filepath.Join(m.pinPath, fmt.Sprintf("link_%s_%d", a.name, deadID))
		if _, statErr := os.Stat(livePin); statErr != nil {
			t.Errorf("live pin %s gone: %v", livePin, statErr)
		}
		if _, statErr := os.Stat(deadPin); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("dead pin %s still present (err=%v)", deadPin, statErr)
		}
	}

	if err := m.FlushDead(); err != nil {
		t.Fatalf("FlushDead: %v", err)
	}
	var row clawkerContainerConfig
	if lookupErr := m.objs.ContainerMap.Lookup(liveID, &row); lookupErr != nil {
		t.Errorf("live container_map row dropped: %v", lookupErr)
	}
	if lookupErr := m.objs.ContainerMap.Lookup(deadID, &row); !errors.Is(lookupErr, ebpf.ErrKeyNotExist) {
		t.Errorf("dead container_map row kept (err=%v)", lookupErr)
	}
}
