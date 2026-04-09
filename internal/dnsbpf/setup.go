package dnsbpf

import (
	"fmt"
	"sync"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
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
	// Parse the dnsbpf block — currently takes no arguments.
	for c.Next() {
		if c.NextArg() {
			return plugin.Error(pluginName, c.ArgErr())
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
		return plugin.Error(pluginName, fmt.Errorf(
			"cannot open BPF dns_cache map at %s: %w (is the eBPF manager running?)",
			DefaultPinPath, sharedMapErr))
	}

	// Close the BPF map FD on CoreDNS shutdown (Corefile reload).
	c.OnShutdown(func() error {
		if sm, ok := sharedMap.(*BPFMap); ok && sm != nil {
			return sm.Close()
		}
		return nil
	})

	// Capture the zone from the server block.
	zone := dnsserver.GetConfig(c).Zone

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return Handler{
			Next: next,
			Zone: zone,
			Map:  sharedMap,
		}
	})

	return nil
}
