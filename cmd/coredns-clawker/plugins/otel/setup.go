package otel

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
)

const (
	pluginName  = "otel"
	envEndpoint = "CLAWKER_COREDNS_OTEL_ENDPOINT"

	defaultClientCertPath = "/etc/clawker/auth/coredns/client.pem"
	defaultClientKeyPath  = "/etc/clawker/auth/coredns/client.key"
	defaultCACertPath     = "/etc/clawker/auth/coredns/ca.pem"
)

func init() { plugin.Register(pluginName, setup) }

var (
	sharedEmitterMu sync.Mutex
	sharedEmitter   Emitter
)

func setup(c *caddy.Controller) error {
	for c.Next() {
		if c.NextArg() {
			// otel takes no Corefile arguments — reject any so a typo
			// in the Corefile fails loudly instead of silently installing
			// a misconfigured handler.
			return plugin.Error(pluginName, c.ArgErr())
		}
	}

	emitter, err := ensureSharedEmitter()
	if err != nil {
		return plugin.Error(pluginName, err)
	}

	zone := dnsserver.GetConfig(c).Zone
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return Handler{
			Next:    next,
			Zone:    zone,
			Emitter: emitter,
		}
	})

	return nil
}

// ensureSharedEmitter returns the process-wide Emitter, building it on
// first call. Only successful construction of a real otelEmitter is
// cached — a transient cert-read failure AND the "endpoint unset" path
// both leave the cache empty so a later CoreDNS reload retries (firewall
// stack may have wired CLAWKER_COREDNS_OTEL_ENDPOINT between attempts;
// caching noopEmitter would latch the degraded state until process exit).
// noopEmitter is returned per call when the endpoint is unset, but never
// stored in sharedEmitter.
func ensureSharedEmitter() (Emitter, error) {
	sharedEmitterMu.Lock()
	defer sharedEmitterMu.Unlock()

	if sharedEmitter != nil {
		return sharedEmitter, nil
	}

	endpoint := strings.TrimSpace(os.Getenv(envEndpoint))
	if endpoint == "" {
		log.Warningf("OTEL endpoint not configured (%s unset); plugin will not export query logs", envEndpoint)
		return noopEmitter{}, nil
	}

	emitter, err := NewEmitter(Options{
		Endpoint:       endpoint,
		CACertFile:     defaultCACertPath,
		ClientCertFile: defaultClientCertPath,
		ClientKeyFile:  defaultClientKeyPath,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize OTEL exporter: %w", err)
	}
	sharedEmitter = emitter
	return sharedEmitter, nil
}
