// ebpf-manager is the entrypoint binary for the clawker eBPF manager container.
//
// It manages BPF programs and maps for per-container traffic routing.
// Programs are loaded once (init), then commands operate on pinned maps.
//
// Usage:
//
//	ebpf-manager init                                    Load + pin BPF programs and maps
//	ebpf-manager enable  <cgroupPath> <configJSON>       Populate maps + attach programs to cgroup
//	ebpf-manager disable <cgroupPath>                    Clear maps + detach programs from cgroup
//	ebpf-manager bypass  <cgroupPath>                    Set bypass flag (unrestricted egress)
//	ebpf-manager unbypass <cgroupPath>                   Clear bypass flag
//	ebpf-manager dns-update <ip> <domainHash> <ttl>      Update DNS cache entry
//	ebpf-manager gc-dns                                  Remove expired DNS cache entries
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"

	clawkerebpf "github.com/schmitthub/clawker/internal/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
)

// enableArgs is the JSON payload for the enable command.
type enableArgs struct {
	EnvoyIP       string              `json:"envoy_ip"`
	CoreDNSIP     string              `json:"coredns_ip"`
	GatewayIP     string              `json:"gateway_ip"`
	CIDR          string              `json:"cidr"`
	HostProxyIP   string              `json:"host_proxy_ip"`
	HostProxyPort uint16              `json:"host_proxy_port"`
	EgressPort    uint16              `json:"egress_port"`
	Routes        []clawkerebpf.Route `json:"routes"`
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
	case "dns-update":
		requireArgs(5) // dns-update <ip> <domainHash> <ttl>
		runDNSUpdate(log, os.Args[2], os.Args[3], os.Args[4])
	case "gc-dns":
		runGCDNS(log)
	case "dump":
		requireArgs(3) // dump <cgroupPath>
		runDump(log, os.Args[2])
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

	if err := mgr.Enable(cgroupID, cgroupPath, cfg, args.Routes); err != nil {
		fatal("enable", err)
	}

	// Pre-populate dns_cache for route domains so per-domain TCP routing
	// works immediately (before CoreDNS plugin writes dynamic entries).
	dnsSeeded := 0
	for _, r := range args.Routes {
		if r.Domain == "" {
			continue
		}
		addrs, err := net.LookupHost(r.Domain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: dns_cache seed: %s: %v\n", r.Domain, err)
			continue
		}
		for _, a := range addrs {
			ip := net.ParseIP(a)
			if ip == nil || ip.To4() == nil {
				continue
			}
			// 300s TTL — CoreDNS dynamic entries will refresh before expiry.
			if err := mgr.UpdateDNSCache(clawkerebpf.IPToUint32(ip), r.DomainHash, 300); err != nil {
				fmt.Fprintf(os.Stderr, "warning: dns_cache seed: %s (%s): %v\n", r.Domain, a, err)
			} else {
				dnsSeeded++
			}
		}
	}

	fmt.Printf("enabled cgroup_id=%d routes=%d dns_seeded=%d\n", cgroupID, len(args.Routes), dnsSeeded)
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

	if err := mgr.Disable(cgroupID); err != nil {
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

	if err := mgr.Bypass(cgroupID); err != nil {
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

	if err := mgr.Unbypass(cgroupID); err != nil {
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

	removed := mgr.GarbageCollectDNS()
	fmt.Printf("gc-dns: removed %d expired entries\n", removed)
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
  enable  <cgroupPath> <configJSON>       Populate maps + attach programs
  disable <cgroupPath>                    Clear maps + detach programs
  bypass  <cgroupPath>                    Set bypass flag
  unbypass <cgroupPath>                   Clear bypass flag
  dns-update <ip> <domainHash> <ttl>      Update DNS cache entry
  gc-dns                                  Remove expired DNS cache entries`)
}

func fatal(cmd string, err error) {
	fmt.Fprintf(os.Stderr, "ebpf-manager %s: %v\n", cmd, err)
	os.Exit(1)
}
