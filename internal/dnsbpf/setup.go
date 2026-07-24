package dnsbpf

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	clawkerebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
)

const pluginName = "dnsbpf"

func init() { plugin.Register(pluginName, setup) }

// sharedMap is the singleton BPF map writer shared across all plugin instances
// (one per Corefile zone). Opened lazily on first use.
var (
	sharedMap     MapWriter
	sharedMapOnce sync.Once
	sharedMapErr  error
)

func setup(c *caddy.Controller) error {
	// Parse the dnsbpf block: exactly one argument, the zone's
	// CP-allocated route identity (non-zero u32). The Corefile generator
	// (controlplane/firewall) writes it; a zone whose dst holds no
	// identity gets no dnsbpf directive at all.
	var identity clawkerebpf.RouteIdentity
	for i := 0; c.Next(); i++ {
		if i > 0 {
			// One zone = one identity: a second directive occurrence would
			// silently last-win and stamp the zone's dns_cache writes with
			// the wrong route identity, aliasing another domain's route.
			return fmt.Errorf("plugin/%s: %w", pluginName, plugin.ErrOnce)
		}
		if !c.NextArg() {
			return fmt.Errorf("plugin/%s: %w", pluginName, c.ArgErr())
		}
		id, err := strconv.ParseUint(c.Val(), 10, 32)
		identity = clawkerebpf.RouteIdentity(id)
		if err != nil || identity.IsNone() {
			return fmt.Errorf("plugin/%s: invalid route identity %q: must be a non-zero u32", pluginName, c.Val())
		}
		if c.NextArg() {
			return fmt.Errorf("plugin/%s: %w", pluginName, c.ArgErr())
		}
	}

	// Open the shared BPF map once across all zones.
	sharedMapOnce.Do(func() {
		var bm *BPFMap
		bm, sharedMapErr = OpenBPFMap(DefaultPinPath)
		if sharedMapErr == nil {
			sharedMap = bm
		}
	})

	// BPF map is required — this plugin's entire purpose is writing to it.
	if sharedMapErr != nil {
		return fmt.Errorf("plugin/%s: cannot open BPF dns_cache map at %s: %w (is the eBPF manager running?)",
			pluginName, DefaultPinPath, sharedMapErr)
	}

	// NOTE: no OnShutdown handler to close the BPF map. The pinned map FD is
	// valid for the lifetime of the process. CoreDNS's reload plugin tears down
	// and rebuilds server blocks without restarting the process — closing the FD
	// here would invalidate it permanently because sync.Once won't re-execute.

	// Capture the zone from the server block.
	zone := dnsserver.GetConfig(c).Zone

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return Handler{
			Next:     next,
			Zone:     zone,
			Identity: identity,
			Map:      sharedMap,
		}
	})

	return nil
}
