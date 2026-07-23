package bpftest

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
)

// privilegedTestsEnv gates the suite: BPF_PROG_TEST_RUN needs CAP_BPF (root
// in practice) plus a kernel ≥ 5.14 for BPF_PROG_TYPE_SYSCALL, neither of
// which the dev container has (CapEff = 0). `make test-bpf` sets it.
const privilegedTestsEnv = "PRIVILEGED_TESTS"

// The bpf2go-generated structs carry blank HostLayout/padding fields, so
// they cannot be constructed as exhaustive composite literals — the helpers
// below build them by field assignment instead.

func dnsEntryVal(identity, expire uint32, source uint8) testprogsDnsEntry {
	var e testprogsDnsEntry
	e.Identity = identity
	e.ExpireTs = expire
	e.Source = source
	return e
}

func routeKeyVal(identity uint32, port uint16, l4 uint8) testprogsRouteKey {
	var k testprogsRouteKey
	k.Identity = identity
	k.DstPort = port
	k.L4Proto = l4
	return k
}

func routeValVal(envoyPort uint16) testprogsRouteVal {
	var v testprogsRouteVal
	v.EnvoyPort = envoyPort
	return v
}

func flowKeyVal(cookie uint64, backendIP uint32, backendPort uint16) testprogsUdpFlowKey {
	var k testprogsUdpFlowKey
	k.Cookie = cookie
	k.BackendIp = backendIP
	k.BackendPort = backendPort
	return k
}

// loadHarness loads the wrapper programs plus the REAL production maps from
// common.h into the kernel — unpinned, so the suite never touches
// /sys/fs/bpf or a live clawker install — and returns the loaded objects.
func loadHarness(t *testing.T) *testprogsObjects {
	t.Helper()
	if os.Getenv(privilegedTestsEnv) == "" {
		t.Skipf(
			"set %s=1 (and run with CAP_BPF/root on a ≥5.14 kernel) to run BPF prog-run tests — use `make test-bpf`",
			privilegedTestsEnv)
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Fatalf("removing memlock rlimit: %v", err)
	}
	spec, specErr := loadTestprogs()
	if specErr != nil {
		t.Fatalf("loading testprogs spec: %v", specErr)
	}
	// The production maps declare LIBBPF_PIN_BY_NAME; strip it so the test
	// collection lives and dies with the test process.
	for _, ms := range spec.Maps {
		ms.Pinning = ebpf.PinNone
	}
	var objs testprogsObjects
	if loadErr := spec.LoadAndAssign(&objs, nil); loadErr != nil {
		t.Fatalf("loading testprogs collection: %v", loadErr)
	}
	t.Cleanup(func() {
		if closeErr := objs.Close(); closeErr != nil {
			t.Logf("closing testprogs collection: %v", closeErr)
		}
	})
	return &objs
}

// runScratch writes the scratch inputs, runs the wrapper program under
// BPF_PROG_TEST_RUN, and returns the scratch block with its outputs.
func runScratch(
	t *testing.T, objs *testprogsObjects, prog *ebpf.Program, in testprogsTestScratch,
) testprogsTestScratch {
	t.Helper()
	if err := objs.TestScratchMap.Put(uint32(0), in); err != nil {
		t.Fatalf("writing test_scratch: %v", err)
	}
	var opts ebpf.RunOptions
	ret, runErr := prog.Run(&opts)
	if runErr != nil {
		t.Fatalf("prog.Run: %v", runErr)
	}
	if ret != 0 {
		t.Fatalf("wrapper returned %d; want 0 (scratch map lookup failed in-kernel)", ret)
	}
	var out testprogsTestScratch
	if err := objs.TestScratchMap.Lookup(uint32(0), &out); err != nil {
		t.Fatalf("reading test_scratch: %v", err)
	}
	return out
}

func ipU32(t *testing.T, s string) uint32 {
	t.Helper()
	ip := clawkerebpf.IPToUint32(net.ParseIP(s))
	if ip == 0 {
		t.Fatalf("bad test IP %q", s)
	}
	return ip
}

// htonsNative returns port in network byte order as the native-endian uint16
// the kernel-side bpf_htons produced (identity on big-endian hosts).
func htonsNative(port uint16) uint16 {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], port)
	return binary.NativeEndian.Uint16(buf[:])
}

const farFuture = uint32(4102444800) // 2100-01-01 — never expires within a test

func TestProgRun_LookupIdentityForIP(t *testing.T) {
	objs := loadHarness(t)
	ip := ipU32(t, "203.0.113.10")
	if err := objs.DnsCache.Put(ip, dnsEntryVal(300, farFuture, clawkerebpf.DNSSourceDNS)); err != nil {
		t.Fatalf("seeding dns_cache: %v", err)
	}

	var in testprogsTestScratch
	in.InDstIp = ip
	out := runScratch(t, objs, objs.TestLookupIdentityForIp, in)
	if out.OutIdentity != 300 {
		t.Fatalf("identity for seeded IP = %d; want 300", out.OutIdentity)
	}

	in.InDstIp = ipU32(t, "203.0.113.99")
	out = runScratch(t, objs, objs.TestLookupIdentityForIp, in)
	if out.OutIdentity != 0 {
		t.Fatalf("identity for unseeded IP = %d; want 0 (miss = no attribution, fail closed)", out.OutIdentity)
	}

	in.InDstIp = 0
	out = runScratch(t, objs, objs.TestLookupIdentityForIp, in)
	if out.OutIdentity != 0 {
		t.Fatalf("identity for dst_ip=0 = %d; want 0", out.OutIdentity)
	}
}

func TestProgRun_LookupRoute(t *testing.T) {
	objs := loadHarness(t)
	put := func(identity uint32, port uint16, l4 uint8, envoy uint16) {
		t.Helper()
		if err := objs.RouteMap.Put(routeKeyVal(identity, port, l4), routeValVal(envoy)); err != nil {
			t.Fatalf("seeding route_map: %v", err)
		}
	}
	put(300, 443, clawkerebpf.L4ProtoTCP, 15001)
	put(300, 443, clawkerebpf.L4ProtoUDP, 15002)

	lookup := func(identity uint32, port uint16, l4 uint8) uint16 {
		t.Helper()
		var in testprogsTestScratch
		in.InIdentity = identity
		in.InDstPort = port
		in.InL4Proto = l4
		return runScratch(t, objs, objs.TestLookupRoute, in).OutEnvoyPort
	}

	if got := lookup(300, 443, clawkerebpf.L4ProtoTCP); got != 15001 {
		t.Fatalf("TCP route = %d; want 15001", got)
	}
	if got := lookup(300, 443, clawkerebpf.L4ProtoUDP); got != 15002 {
		t.Fatalf("UDP route = %d; want 15002 — TCP and UDP routes for one {identity, port} must stay independent", got)
	}
	if got := lookup(300, 8443, clawkerebpf.L4ProtoTCP); got != 0 {
		t.Fatalf("route for unrouted port = %d; want 0 (miss = deny)", got)
	}
	if got := lookup(301, 443, clawkerebpf.L4ProtoTCP); got != 0 {
		t.Fatalf("route for unrouted identity = %d; want 0 (miss = deny)", got)
	}
}

// routeForDst drives the composed dns_cache → route_map chain wrapper.
func routeForDst(
	t *testing.T, objs *testprogsObjects, ip uint32, port uint16, l4 uint8,
) testprogsTestScratch {
	t.Helper()
	var in testprogsTestScratch
	in.InDstIp = ip
	in.InDstPort = port
	in.InL4Proto = l4
	return runScratch(t, objs, objs.TestRouteForDst, in)
}

// TestProgRun_RouteForDst_NoCrossDomainAliasing drives the composed
// dns_cache → route_map chain for two destinations and asserts each resolves
// through its own route — the kernel-level regression surface of the
// hash-collision / identity-renumbering bug class the sticky allocator
// removes.
func TestProgRun_RouteForDst_NoCrossDomainAliasing(t *testing.T) {
	objs := loadHarness(t)
	ipA, ipB := ipU32(t, "203.0.113.10"), ipU32(t, "203.0.113.20")
	seed := func(ip, identity uint32, envoy uint16) {
		t.Helper()
		if err := objs.DnsCache.Put(ip, dnsEntryVal(identity, farFuture, clawkerebpf.DNSSourceDNS)); err != nil {
			t.Fatalf("seeding dns_cache: %v", err)
		}
		routePutErr := objs.RouteMap.Put(routeKeyVal(identity, 443, clawkerebpf.L4ProtoTCP), routeValVal(envoy))
		if routePutErr != nil {
			t.Fatalf("seeding route_map: %v", routePutErr)
		}
	}
	seed(ipA, 300, 15001)
	seed(ipB, 301, 15002)

	outA := routeForDst(t, objs, ipA, 443, clawkerebpf.L4ProtoTCP)
	outB := routeForDst(t, objs, ipB, 443, clawkerebpf.L4ProtoTCP)
	if outA.OutIdentity != 300 || outA.OutEnvoyPort != 15001 {
		t.Fatalf("dst A resolved {identity=%d, envoy=%d}; want {300, 15001}", outA.OutIdentity, outA.OutEnvoyPort)
	}
	if outB.OutIdentity != 301 || outB.OutEnvoyPort != 15002 {
		t.Fatalf("dst B resolved {identity=%d, envoy=%d}; want {301, 15002} — cross-domain aliasing",
			outB.OutIdentity, outB.OutEnvoyPort)
	}
}

// TestProgRun_RouteForDst_ExpiredEntryStillRoutes pins the fast-path contract
// that BPF never inspects expire_ts (or source) — expiry is enforced solely
// by userspace GC. If a kernel-side TTL check were ever added, the zombie-DNS
// sparing in GarbageCollectDNS would stop being sufficient.
func TestProgRun_RouteForDst_ExpiredEntryStillRoutes(t *testing.T) {
	objs := loadHarness(t)
	ip := ipU32(t, "203.0.113.30")
	if err := objs.DnsCache.Put(ip, dnsEntryVal(302, 1, clawkerebpf.DNSSourceDNS)); err != nil {
		t.Fatalf("seeding dns_cache: %v", err)
	}
	if err := objs.RouteMap.Put(routeKeyVal(302, 443, clawkerebpf.L4ProtoUDP), routeValVal(15003)); err != nil {
		t.Fatalf("seeding route_map: %v", err)
	}
	out := routeForDst(t, objs, ip, 443, clawkerebpf.L4ProtoUDP)
	if out.OutEnvoyPort != 15003 {
		t.Fatalf("expired entry route = %d; want 15003 — BPF fast path must ignore expire_ts", out.OutEnvoyPort)
	}
}

func TestProgRun_RecordUDPFlow(t *testing.T) {
	objs := loadHarness(t)
	envoy := ipU32(t, "172.30.0.2")
	orig := ipU32(t, "203.0.113.40")

	var in testprogsTestScratch
	in.InCookie = 77
	in.InBackendIp = envoy
	in.InBackendPort = 15002
	in.InDstIp = orig
	in.InDstPort = 8443
	_ = runScratch(t, objs, objs.TestRecordUdpFlow, in)

	var val testprogsUdpFlowVal
	if err := objs.UdpFlowMap.Lookup(flowKeyVal(77, envoy, 15002), &val); err != nil {
		t.Fatalf("udp_flow_map entry not recorded: %v", err)
	}
	if val.OrigDstIp != orig {
		t.Fatalf("orig_dst_ip = %d; want %d", val.OrigDstIp, orig)
	}
	if want := htonsNative(8443); val.OrigDstPort != want {
		t.Fatalf("orig_dst_port = %#x; want %#x (network byte order, converted by record_udp_flow)",
			val.OrigDstPort, want)
	}

	// cookie==0 means bpf_get_socket_cookie failed — record_udp_flow must
	// not write a garbage key.
	in.InCookie = 0
	_ = runScratch(t, objs, objs.TestRecordUdpFlow, in)
	if err := objs.UdpFlowMap.Lookup(flowKeyVal(0, envoy, 15002), &val); !errors.Is(err, ebpf.ErrKeyNotExist) {
		t.Fatalf("cookie=0 flow lookup err = %v; want ErrKeyNotExist (must skip recording)", err)
	}
}
