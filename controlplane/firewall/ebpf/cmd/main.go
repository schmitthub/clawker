// ebpf-manager is a break-glass CLI packaged inside the clawker-controlplane container image.
//
// It manages BPF programs and maps for per-container traffic routing.
// Programs are loaded once (init), then commands operate on pinned maps.
//
// Usage:
//
//	ebpf-manager init                                    Load + pin BPF programs and maps
//	ebpf-manager enable  <cgroupPath> <configJSON>       Add to container_map + attach programs to cgroup
//	ebpf-manager disable <cgroupPath>                    Remove from container_map + detach programs
//	ebpf-manager sync-routes <routesJSON>                Replace global route_map with new routes
//	ebpf-manager bypass  <cgroupPath>                    Set bypass flag (unrestricted egress)
//	ebpf-manager unbypass <cgroupPath>                   Clear bypass flag
//	ebpf-manager dns-update <ip> <domainHash> <ttl>      Update DNS cache entry
//	ebpf-manager gc-dns                                  Remove expired DNS cache entries
//	ebpf-manager dump <cgroupPath>                       Inspect container_map for one cgroup
//	ebpf-manager dump-routes [--json]                    Dump global route_map (every {domain_hash, dst_port, l4_proto} → envoy_port)
//	ebpf-manager dump-containers [--json]                Dump container_map (every cgroup → BPF container_config)
//	ebpf-manager dump-bypass [--json]                    Dump bypass_map (every cgroup → bypass flag)
//	ebpf-manager dump-dns [--json]                       Dump dns_cache (every IP → {domain_hash, expire_ts})
//	ebpf-manager resolve <hostname>                      Resolve hostname to IPv4 from CP netns
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
)

// enableArgs is the JSON payload for the enable command.
type enableArgs struct {
	EnvoyIP       string `json:"envoy_ip"`
	CoreDNSIP     string `json:"coredns_ip"`
	GatewayIP     string `json:"gateway_ip"`
	CIDR          string `json:"cidr"`
	HostProxyIP   string `json:"host_proxy_ip"`
	HostProxyPort uint16 `json:"host_proxy_port"`
	EgressPort    uint16 `json:"egress_port"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	log := logger.Nop()

	cmd := os.Args[1]
	switch cmd {
	case "init":
		runInit(log)
	case "enable":
		requireArgs(4) // enable <cgroupPath> <configJSON>
		runEnable(log, os.Args[2], os.Args[3])
	case "disable":
		requireArgs(3) // disable <cgroupPath>
		runDisable(log, os.Args[2])
	case "bypass":
		requireArgs(3) // bypass <cgroupPath>
		runBypass(log, os.Args[2])
	case "unbypass":
		requireArgs(3) // unbypass <cgroupPath>
		runUnbypass(log, os.Args[2])
	case "sync-routes":
		requireArgs(3) // sync-routes <routesJSON>
		runSyncRoutes(log, os.Args[2])
	case "dns-update":
		requireArgs(5) // dns-update <ip> <domainHash> <ttl>
		runDNSUpdate(log, os.Args[2], os.Args[3], os.Args[4])
	case "gc-dns":
		runGCDNS(log)
	case "dump":
		requireArgs(3) // dump <cgroupPath>
		runDump(log, os.Args[2])
	case "dump-routes":
		runDumpRoutes(log, hasJSONFlag(os.Args[2:]))
	case "dump-containers":
		runDumpContainers(log, hasJSONFlag(os.Args[2:]))
	case "dump-bypass":
		runDumpBypass(log, hasJSONFlag(os.Args[2:]))
	case "dump-dns":
		runDumpDNS(log, hasJSONFlag(os.Args[2:]))
	case "resolve":
		requireArgs(3) // resolve <hostname>
		runResolve(os.Args[2])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func runInit(log *logger.Logger) {
	mgr := clawkerebpf.NewManager(log)
	if err := mgr.Load(); err != nil {
		fatal("init", err)
	}
	// Don't Close() — we want maps and programs to stay pinned.
	fmt.Println("eBPF programs loaded and pinned")
}

func runEnable(log *logger.Logger, cgroupPath, configJSON string) {
	var args enableArgs
	if err := json.Unmarshal([]byte(configJSON), &args); err != nil {
		fatal("enable", fmt.Errorf("parsing config JSON: %w", err))
	}

	cfg, err := clawkerebpf.NewContainerConfig(
		args.EnvoyIP, args.CoreDNSIP, args.GatewayIP, args.CIDR,
		args.HostProxyIP, args.HostProxyPort, args.EgressPort,
	)
	if err != nil {
		fatal("enable", err)
	}

	cgroupID, err := clawkerebpf.CgroupID(cgroupPath)
	if err != nil {
		fatal("enable", fmt.Errorf("getting cgroup ID for %s: %w", cgroupPath, err))
	}

	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal("enable", err)
	}
	defer mgr.Close()

	if err := mgr.Install(cgroupID, cgroupPath, cfg); err != nil {
		fatal("enable", err)
	}

	fmt.Printf("enabled cgroup_id=%d\n", cgroupID)
}

func runSyncRoutes(log *logger.Logger, routesJSON string) {
	var routes []clawkerebpf.Route
	if err := json.Unmarshal([]byte(routesJSON), &routes); err != nil {
		fatal("sync-routes", fmt.Errorf("parsing routes JSON: %w", err))
	}

	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal("sync-routes", err)
	}
	defer mgr.Close()

	if err := mgr.SyncRoutes(routes); err != nil {
		fatal("sync-routes", err)
	}

	fmt.Printf("synced %d routes\n", len(routes))
}

func runDisable(log *logger.Logger, cgroupPath string) {
	cgroupID, err := clawkerebpf.CgroupID(cgroupPath)
	if err != nil {
		fatal("disable", fmt.Errorf("getting cgroup ID for %s: %w", cgroupPath, err))
	}

	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal("disable", err)
	}
	defer mgr.Close()

	if err := mgr.Remove(cgroupID); err != nil {
		fatal("disable", err)
	}
	fmt.Printf("disabled cgroup_id=%d\n", cgroupID)
}

func runBypass(log *logger.Logger, cgroupPath string) {
	cgroupID, err := clawkerebpf.CgroupID(cgroupPath)
	if err != nil {
		fatal("bypass", err)
	}

	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal("bypass", err)
	}
	defer mgr.Close()

	if err := mgr.Disable(cgroupID); err != nil {
		fatal("bypass", err)
	}
	fmt.Printf("bypass enabled cgroup_id=%d\n", cgroupID)
}

func runUnbypass(log *logger.Logger, cgroupPath string) {
	cgroupID, err := clawkerebpf.CgroupID(cgroupPath)
	if err != nil {
		fatal("unbypass", err)
	}

	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal("unbypass", err)
	}
	defer mgr.Close()

	if err := mgr.Enable(cgroupID); err != nil {
		fatal("unbypass", err)
	}
	fmt.Printf("bypass disabled cgroup_id=%d\n", cgroupID)
}

func runDNSUpdate(log *logger.Logger, ipStr, domainHashStr, ttlStr string) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		fatal("dns-update", fmt.Errorf("invalid IP: %s", ipStr))
	}
	domainHash, err := strconv.ParseUint(domainHashStr, 10, 32)
	if err != nil {
		fatal("dns-update", fmt.Errorf("parsing domain hash: %w", err))
	}
	ttl, err := strconv.ParseUint(ttlStr, 10, 32)
	if err != nil {
		fatal("dns-update", fmt.Errorf("parsing TTL: %w", err))
	}

	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal("dns-update", err)
	}
	defer mgr.Close()

	if err := mgr.UpdateDNSCache(clawkerebpf.IPToUint32(ip), uint32(domainHash), uint32(ttl)); err != nil {
		fatal("dns-update", err)
	}
}

func runGCDNS(log *logger.Logger) {
	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal("gc-dns", err)
	}
	defer mgr.Close()

	removed, err := mgr.GarbageCollectDNS()
	fmt.Printf("gc-dns: removed %d expired entries\n", removed)
	if err != nil {
		fatal("gc-dns", err)
	}
}

func runDump(log *logger.Logger, cgroupPath string) {
	cgroupID, err := clawkerebpf.CgroupID(cgroupPath)
	if err != nil {
		fatal("dump", fmt.Errorf("getting cgroup ID for %s: %w", cgroupPath, err))
	}

	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal("dump", err)
	}
	defer mgr.Close()

	cfg, err := mgr.LookupContainer(cgroupID)
	if err != nil {
		fmt.Printf("cgroup_id=%d: no container_map entry: %v\n", cgroupID, err)
	} else {
		fmt.Printf("cgroup_id=%d\n", cgroupID)
		fmt.Printf("  envoy_ip=%s coredns_ip=%s gateway_ip=%s\n",
			clawkerebpf.Uint32ToIP(cfg.EnvoyIp),
			clawkerebpf.Uint32ToIP(cfg.CorednsIp),
			clawkerebpf.Uint32ToIP(cfg.GatewayIp))
		fmt.Printf("  net_addr=%s net_mask=%s\n",
			clawkerebpf.Uint32ToIP(cfg.NetAddr),
			clawkerebpf.Uint32ToIP(cfg.NetMask))
		fmt.Printf("  host_proxy_ip=%s host_proxy_port=%d egress_port=%d\n",
			clawkerebpf.Uint32ToIP(cfg.HostProxyIp),
			cfg.HostProxyPort, cfg.EgressPort)
	}
}

// hasJSONFlag scans args for --json. Recognized at any position so the
// CLI is forgiving for human operators in incident response.
func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--json" || a == "-json" {
			return true
		}
	}
	return false
}

func openManagerOrFail(log *logger.Logger, cmd string) *clawkerebpf.Manager {
	mgr := clawkerebpf.NewManager(log)
	if err := mgr.OpenPinned(); err != nil {
		fatal(cmd, err)
	}
	return mgr
}

func runDumpRoutes(log *logger.Logger, asJSON bool) {
	mgr := openManagerOrFail(log, "dump-routes")
	defer mgr.Close()

	routes, err := mgr.DumpRoutes()
	if err != nil {
		fatal("dump-routes", err)
	}
	if asJSON {
		emitJSON("dump-routes", routes)
		return
	}
	if len(routes) == 0 {
		fmt.Println("route_map: empty")
		return
	}
	fmt.Printf("route_map: %d entries\n", len(routes))
	for _, r := range routes {
		fmt.Printf("  domain_hash=0x%08x dst_port=%d proto=%s -> envoy_port=%d\n",
			r.DomainHash, r.DstPort, l4ProtoLabel(r.L4Proto), r.EnvoyPort)
	}
}

// l4ProtoLabel renders a Route.L4Proto byte for the break-glass dump.
func l4ProtoLabel(p uint8) string {
	switch p {
	case clawkerebpf.L4ProtoTCP:
		return "tcp"
	case clawkerebpf.L4ProtoUDP:
		return "udp"
	default:
		return fmt.Sprintf("?(%d)", p)
	}
}

func runDumpContainers(log *logger.Logger, asJSON bool) {
	mgr := openManagerOrFail(log, "dump-containers")
	defer mgr.Close()

	entries, err := mgr.DumpContainers()
	if err != nil {
		fatal("dump-containers", err)
	}
	if asJSON {
		emitJSON("dump-containers", entries)
		return
	}
	if len(entries) == 0 {
		fmt.Println("container_map: empty")
		return
	}
	fmt.Printf("container_map: %d entries\n", len(entries))
	for _, e := range entries {
		c := e.Config
		fmt.Printf("  cgroup_id=%d\n", e.CgroupID)
		fmt.Printf("    envoy_ip=%s coredns_ip=%s gateway_ip=%s\n",
			clawkerebpf.Uint32ToIP(c.EnvoyIP),
			clawkerebpf.Uint32ToIP(c.CoreDNSIP),
			clawkerebpf.Uint32ToIP(c.GatewayIP))
		fmt.Printf("    net_addr=%s net_mask=%s\n",
			clawkerebpf.Uint32ToIP(c.NetAddr),
			clawkerebpf.Uint32ToIP(c.NetMask))
		fmt.Printf("    host_proxy_ip=%s host_proxy_port=%d egress_port=%d\n",
			clawkerebpf.Uint32ToIP(c.HostProxyIP),
			c.HostProxyPort, c.EgressPort)
	}
}

func runDumpBypass(log *logger.Logger, asJSON bool) {
	mgr := openManagerOrFail(log, "dump-bypass")
	defer mgr.Close()

	entries, err := mgr.DumpBypass()
	if err != nil {
		fatal("dump-bypass", err)
	}
	if asJSON {
		emitJSON("dump-bypass", entries)
		return
	}
	if len(entries) == 0 {
		fmt.Println("bypass_map: empty")
		return
	}
	fmt.Printf("bypass_map: %d entries\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  cgroup_id=%d bypass=%t\n", e.CgroupID, e.Bypass)
	}
}

func runDumpDNS(log *logger.Logger, asJSON bool) {
	mgr := openManagerOrFail(log, "dump-dns")
	defer mgr.Close()

	entries, err := mgr.DumpDNS()
	if err != nil {
		fatal("dump-dns", err)
	}
	if asJSON {
		emitJSON("dump-dns", entries)
		return
	}
	if len(entries) == 0 {
		fmt.Println("dns_cache: empty")
		return
	}
	fmt.Printf("dns_cache: %d entries\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  ip=%s domain_hash=0x%08x expire_ts=%d\n",
			e.IP, e.DomainHash, e.ExpireTS)
	}
}

func emitJSON(cmd string, v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatal(cmd, fmt.Errorf("encoding json: %w", err))
	}
}

func runResolve(hostname string) {
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		fatal("resolve", err)
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			fmt.Println(a)
			return
		}
	}
	fatal("resolve", fmt.Errorf("no IPv4 address for %s", hostname))
}

func requireArgs(n int) {
	if len(os.Args) < n {
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: ebpf-manager <command> [args...]

Commands:
  init                                    Load + pin BPF programs and maps
  enable  <cgroupPath> <configJSON>       Add to container_map + attach programs
  disable <cgroupPath>                    Remove from container_map + detach
  sync-routes <routesJSON>                Replace global route_map
  bypass  <cgroupPath>                    Set bypass flag
  unbypass <cgroupPath>                   Clear bypass flag
  dns-update <ip> <domainHash> <ttl>      Update DNS cache entry
  gc-dns                                  Remove expired DNS cache entries
  dump <cgroupPath>                       Inspect container_map for one cgroup
  dump-routes [--json]                    Dump global route_map
  dump-containers [--json]                Dump container_map
  dump-bypass [--json]                    Dump bypass_map
  dump-dns [--json]                       Dump dns_cache
  resolve <hostname>                      Resolve hostname to IPv4 from CP netns`)
}

func fatal(cmd string, err error) {
	fmt.Fprintf(os.Stderr, "ebpf-manager %s: %v\n", cmd, err)
	os.Exit(1)
}
