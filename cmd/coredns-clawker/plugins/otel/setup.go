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
	defaultCACertPath     = "/etc/clawker/auth/tls/ca.pem"
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
// first call. Only successful construction is cached — a failure (e.g.
// transient cert read mid-rotation) leaves the cache empty so a later
// CoreDNS reload retries instead of permanently latching the error.
func ensureSharedEmitter() (Emitter, error) {
	sharedEmitterMu.Lock()
	defer sharedEmitterMu.Unlock()

	if sharedEmitter != nil {
		return sharedEmitter, nil
	}

	endpoint := strings.TrimSpace(os.Getenv(envEndpoint))
	if endpoint == "" {
		log.Warningf("OTEL endpoint not configured (%s unset); plugin will not export query logs", envEndpoint)
		sharedEmitter = noopEmitter{}
		return sharedEmitter, nil
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
