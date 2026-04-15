package firewall

import (
	"path/filepath"
	"strings"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// overrideHostPathsForTest points consts.Host* package vars at the
// testenv-backed firewall data dir so Stack.envoyContainerSpec +
// Stack.corednsContainerSpec resolve bind Mount.Source values the test
// can assert against. Inside production the CLI injects CLAWKER_HOST_*
// env vars at CP-container creation; in unit tests the package vars
// have already been initialized (empty) by the time testenv sets the
// env, so we override directly and restore via t.Cleanup.
func overrideHostPathsForTest(t *testing.T, dataDir string) {
	t.Helper()
	origData := consts.HostDataDir
	origFwData := consts.HostFirewallDataSubdir
	origFwCert := consts.HostFirewallCertSubdir
	origEnvoy := consts.HostEnvoyConfigPath
	origCore := consts.HostCorefilePath

	consts.HostDataDir = dataDir
	consts.HostFirewallDataSubdir = filepath.Join(dataDir, "firewall")
	consts.HostFirewallCertSubdir = filepath.Join(consts.HostFirewallDataSubdir, "certs")
	consts.HostEnvoyConfigPath = filepath.Join(consts.HostFirewallDataSubdir, consts.EnvoyConfigFile)
	consts.HostCorefilePath = filepath.Join(consts.HostFirewallDataSubdir, consts.Corefile)

	t.Cleanup(func() {
		consts.HostDataDir = origData
		consts.HostFirewallDataSubdir = origFwData
		consts.HostFirewallCertSubdir = origFwCert
		consts.HostEnvoyConfigPath = origEnvoy
		consts.HostCorefilePath = origCore
	})
}

// TestContainerSpecs_FirewallDataMountsAreReadOnly covers INV-B2-011:
// Envoy and CoreDNS must mount anything rooted under
// cfg.FirewallDataSubdir() (envoy.yaml, Corefile, cert dir) as
// read-only. Only the CP holds an RW bind on firewall data; a
// compromised proxy must not be able to rewrite rules or certs.
//
// Unrelated mounts (e.g. /sys/fs/bpf on CoreDNS, which the dnsbpf
// plugin legitimately writes to) are out of scope for this invariant
// and are explicitly skipped.
func TestContainerSpecs_FirewallDataMountsAreReadOnly(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dataDir, err := cfg.FirewallDataSubdir()
	if err != nil {
		t.Fatalf("FirewallDataSubdir: %v", err)
	}
	certDir, err := cfg.FirewallCertSubdir()
	if err != nil {
		t.Fatalf("FirewallCertSubdir: %v", err)
	}
	// Point the CP-injected host paths at the testenv dir so the spec
	// builder resolves to a matching data dir for isFirewallData.
	overrideHostPathsForTest(t, consts.DataDir())

	s := NewStack(nil, cfg, logger.Nop(), nil)
	netInfo := &NetworkInfo{NetworkID: "net-test", EnvoyIP: "172.20.0.2", CoreDNSIP: "172.20.0.3"}

	// isFirewallData returns true for any mount source rooted under
	// the firewall data subdir (matches envoy.yaml, Corefile,
	// certs/). filepath.Rel returns a relative path that does not
	// begin with ".." when src is inside dataDir — this handles
	// the certs dir too since it is a nested subpath.
	isFirewallData := func(src string) bool {
		rel, err := filepath.Rel(dataDir, src)
		if err != nil {
			return false
		}
		return !strings.HasPrefix(rel, "..")
	}

	t.Run("envoy", func(t *testing.T) {
		spec := s.envoyContainerSpec(netInfo)
		firewallMounts := 0
		for _, m := range spec.mounts {
			if !isFirewallData(m.Source) {
				continue
			}
			firewallMounts++
			if !m.ReadOnly {
				t.Errorf("envoy mount %s → %s is RW, want ReadOnly=true", m.Source, m.Target)
			}
		}
		// Sanity: envoy spec must actually mount firewall data
		// (envoy.yaml + certDir). If the spec changes and no
		// firewall-data mounts are present, the invariant is
		// trivially satisfied — force the test to fail instead.
		if firewallMounts < 2 {
			t.Errorf("envoy spec has %d firewall-data mounts, want at least 2 (envoy.yaml + %s)\n Got: %+v", firewallMounts, certDir, spec.mounts)
		}
	})

	t.Run("coredns", func(t *testing.T) {
		spec := s.corednsContainerSpec(netInfo)
		firewallMounts := 0
		for _, m := range spec.mounts {
			if !isFirewallData(m.Source) {
				continue
			}
			firewallMounts++
			if !m.ReadOnly {
				t.Errorf("coredns mount %s → %s is RW, want ReadOnly=true", m.Source, m.Target)
			}
		}
		if firewallMounts < 1 {
			t.Errorf("coredns spec has %d firewall-data mounts, want at least 1 (Corefile)\n Got: %+v", firewallMounts, spec.mounts)
		}
	})
}
