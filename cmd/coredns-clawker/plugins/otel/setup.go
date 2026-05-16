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
	sharedEmitter     Emitter
	sharedEmitterOnce sync.Once
	sharedEmitterErr  error
)

func setup(c *caddy.Controller) error {
	for c.Next() {
		if c.NextArg() {
			return plugin.Error(pluginName, c.ArgErr())
		}
	}

	sharedEmitterOnce.Do(func() {
		endpoint := strings.TrimSpace(os.Getenv(envEndpoint))
		if endpoint == "" {
			log.Warning("OTEL endpoint not configured; plugin will not export query logs")
			sharedEmitter = noopEmitter{}
			return
		}

		sharedEmitter, sharedEmitterErr = NewEmitter(Options{
			Endpoint:       endpoint,
			CACertFile:     defaultCACertPath,
			ClientCertFile: defaultClientCertPath,
			ClientKeyFile:  defaultClientKeyPath,
		})
		if sharedEmitterErr != nil {
			sharedEmitterErr = fmt.Errorf("initialize OTEL exporter: %w", sharedEmitterErr)
		}
	})

	if sharedEmitterErr != nil {
		return plugin.Error(pluginName, sharedEmitterErr)
	}

	zone := dnsserver.GetConfig(c).Zone
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return Handler{
			Next:    next,
			Zone:    zone,
			Emitter: sharedEmitter,
		}
	})

	return nil
}
