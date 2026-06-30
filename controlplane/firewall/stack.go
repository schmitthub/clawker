package firewall

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	mobyclient "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/pkg/whail"
)

// Stack lifecycle + image constants.
const (
	envoyImage      = "envoyproxy/envoy:distroless-v1.37.1@sha256:4d9226b9fd4d1449887de7cde785beb24b12e47d6e79021dec3c79e362609432"
	corednsImageTag = "clawker-coredns:latest"

	envoyContainerName   = consts.ContainerEnvoy
	corednsContainerName = consts.ContainerCoreDNS

	// healthCheckTimeout bounds WaitForHealthy. A ctx deadline can only
	// tighten it, never extend it. Shared with the CLI's bringup RPC deadline
	// (consts.FirewallStackBringupRPCTimeout derives from this) so the client
	// never times out before the server's real health error.
	healthCheckTimeout  = consts.FirewallStackHealthTimeout
	healthCheckInterval = 500 * time.Millisecond

	// labelInfraCertsReady encodes whether the running container was
	// created with the mTLS-OTLP bind-mount + env wired. ensureContainer
	// and Reload compare it against the current desired state to detect
	// spec drift after an infraCertsReady flip — a plain ContainerRestart
	// preserves env + mounts from create-time and would otherwise leave
	// envoy.yaml referencing /etc/envoy/otel-tls/* with no mount, or
	// CoreDNS holding CLAWKER_COREDNS_OTEL_ENDPOINT against missing certs.
	labelInfraCertsReady = "dev.clawker.firewall.infra_certs_ready"

	// labelOtelInfraPort encodes the create-time monitoring.otel_infra_port
	// value. CoreDNS receives the port via CLAWKER_COREDNS_OTEL_ENDPOINT
	// env at ContainerCreate; Docker preserves env across ContainerRestart,
	// so a setting change while certs remain ready would leave CoreDNS
	// exporting to the stale port. Including the port in drift labels
	// forces a recreate (not just a restart) when it changes.
	labelOtelInfraPort = "dev.clawker.firewall.otel_infra_port"

	// labelStackBuildSHA stamps the CP's embedded-binary hash
	// (consts.CPBinarySHA, injected by host bootstrap at CP create) on
	// both siblings. Every other staleness vector — the pinned Envoy
	// image const, the embedded coredns-clawker binary, the
	// envoy_config/coredns_config templates, the containerSpec shape —
	// is compiled into clawkercp, so a CP built from different bytes
	// MUST recreate the siblings rather than adopt ones an older build
	// created.
	labelStackBuildSHA = "dev.clawker.firewall.stack_build_sha"
)

// Stack manages the Envoy + CoreDNS container pair via Docker-outside-of-
// Docker from inside the control plane container. The CP container itself
// is created host-side by CLI bootstrap, not by Stack.
//
// Stack is not safe for concurrent EnsureRunning + Stop calls; callers
// serialize via their own mutex.
type Stack struct {
	docker    *docker.Client
	cfg       config.Config
	log       *logger.Logger
	store     *storage.Store[EgressRulesFile]
	otelCerts OtelCertProvisioner
	// infraCertsReady is set to true by ensureConfigs after a successful
	// ensureInfraClientCerts call (and reset to false on failure). Gates
	// downstream mTLS wiring (alsConfig, envoy/coredns container specs)
	// so a partial cert mint can't leave Envoy/CoreDNS pointed at
	// missing files — which would take CoreDNS startup down hard via
	// the otel plugin's tls.LoadX509KeyPair, breaking the firewall
	// plane on a telemetry failure.
	//
	// Stack methods are serialized by the controlplane ActionQueue; no
	// mutex is required.
	infraCertsReady bool
}

// OtelCertProvisioner is the firewall-package view of the trusted-lane
// mTLS material provider. The concrete implementation lives in
// `./controlplane/otelcerts` (CP-level, outside the firewall
// package — see feedback_no_layering_violations.md). Tests pass a
// tiny fake; production wiring passes `*otelcerts.Service`.
//
// May be nil — Stack tolerates a missing provisioner by skipping the
// mTLS material bind-mounts. Envoy omits the OTel access-log sink
// and the otel_collector_als cluster entirely (sender-side gate on
// als.MTLS in buildHTTPAccessLog / buildTCPAccessLog / buildClusters
// — no cross-lane emission to the untrusted otel-collector:4317
// receiver that's reserved for agent containers). CoreDNS sees no
// CLAWKER_COREDNS_OTEL_ENDPOINT and installs noopEmitter. Stdout
// logs remain available via `docker logs` for triage, but the
// OpenSearch pipeline has no filelog receiver — the trusted OTLP push
// path is the only ingestion route, and it stays cold. This matches the
// CP-side degraded path (see cmd/clawkercp/clawkercp.go: event=
// otelcerts_unavailable).
type OtelCertProvisioner interface {
	// EnsureClient mints + writes per-service mTLS client material under
	// the provisioner's destination directory, atomically. Re-runs
	// overwrite in place. Returned paths are CP-container-FS absolute
	// paths under destDir — Stack discards them and derives sibling
	// Mount.Source from consts.HostFirewallOtelCertsDir, the host-FS
	// twin of destDir via CP's firewall data bind-mount.
	EnsureClient(svc string) (certPath, keyPath, caPath string, err error)
}

// NewStack returns an initialized Stack. log may be nil (a Nop logger is
// substituted); the other dependencies are required — nil docker or cfg
// produces a nil Stack that panics at first use, which is preferable to
// silent no-ops. otelCerts may be nil — see OtelCertProvisioner.
func NewStack(
	dc *docker.Client,
	cfg config.Config,
	log *logger.Logger,
	store *storage.Store[EgressRulesFile],
	otelCerts OtelCertProvisioner,
) *Stack {
	if log == nil {
		log = logger.Nop()
	}
	return &Stack{docker: dc, cfg: cfg, log: log, store: store, otelCerts: otelCerts}
}

// EnsureRunning starts Envoy + CoreDNS if they are not already running.
// Idempotent: the method short-circuits per container when it finds the
// expected container already in the "running" state.
//
// Ordering is fixed: ensure network → discover → write configs/certs →
// ensure images → ensure Envoy → ensure CoreDNS → wait healthy. CoreDNS's
// dnsbpf plugin opens the pinned dns_cache map at startup; the map is
// created by the CP's eBPF manager before Stack starts, so Stack does not
// touch BPF state here.
func (s *Stack) EnsureRunning(ctx context.Context) error {
	netInfo, err := s.ensureNetworkAndDiscover(ctx)
	if err != nil {
		return err
	}

	if _, err := s.ensureConfigs(); err != nil {
		return fmt.Errorf("firewall stack: %w", err)
	}

	if err := s.ensureCorednsImage(ctx); err != nil {
		return fmt.Errorf("firewall stack: %w", err)
	}
	if err := s.ensureEnvoyImage(ctx); err != nil {
		return fmt.Errorf("firewall stack: %w", err)
	}

	if err := s.ensureContainer(ctx, envoyContainerName, s.envoyContainerSpec(netInfo)); err != nil {
		return fmt.Errorf("firewall stack: envoy: %w", err)
	}
	if err := s.ensureContainer(ctx, corednsContainerName, s.corednsContainerSpec(netInfo)); err != nil {
		return fmt.Errorf("firewall stack: coredns: %w", err)
	}

	if err := s.WaitForHealthy(ctx); err != nil {
		return fmt.Errorf("firewall stack: %w", err)
	}
	s.log.Debug().Msg("firewall stack running")
	return nil
}

// Stop removes Envoy + CoreDNS. The clawker network and eBPF state
// are intentionally left intact: agent containers may still be attached
// to the network, and BPF links are owned by the CP's ebpf.Manager.
// The control plane container is owned by host-side bootstrap.
func (s *Stack) Stop(ctx context.Context) error {
	envoyErr := s.stopAndRemove(ctx, envoyContainerName)
	corednsErr := s.stopAndRemove(ctx, corednsContainerName)
	if err := errors.Join(envoyErr, corednsErr); err != nil {
		return fmt.Errorf("firewall stack stop: %w", err)
	}
	return nil
}

// Reload regenerates configs and restarts Envoy + CoreDNS. Callers invoke
// this after rule mutations land in the store. If the stack is not
// currently running, Reload does nothing — the next EnsureRunning call
// will pick up the fresh configs.
//
// Step-level failures are wrapped with their typed sentinel
// (ErrConfigRegen, ErrStackProbe, ErrEnvoyRestart, ErrCoreDNSRestart,
// ErrStackUnhealthy) and combined via errors.Join so the Handler's RPC
// wrapper (toStatus) can attach one errdetails.ErrorInfo per failed
// step. ErrConfigRegen short-circuits the rest — restarting against
// stale configs would just thrash. Envoy/CoreDNS restart failures are
// collected independently; WaitForHealthy runs only when both restarts
// succeeded (otherwise the primary signal is the restart failure, not
// the health probe's timeout).
func (s *Stack) Reload(ctx context.Context) error {
	if _, err := s.ensureConfigs(); err != nil {
		return fmt.Errorf("%w: %v", ErrConfigRegen, err)
	}

	envoyRunning, err := s.isRunning(ctx, envoyContainerName)
	if err != nil {
		return fmt.Errorf("%w: envoy: %v", ErrStackProbe, err)
	}
	corednsRunning, err := s.isRunning(ctx, corednsContainerName)
	if err != nil {
		return fmt.Errorf("%w: coredns: %v", ErrStackProbe, err)
	}
	if !envoyRunning || !corednsRunning {
		return nil
	}

	// ensureConfigs may have flipped infraCertsReady; reloadContainer
	// recreates the container when its create-time env + mounts no
	// longer match the current Stack state (a plain ContainerRestart
	// preserves both). Specs are computed once netInfo is available.
	netInfo, err := DiscoverNetwork(ctx, s.docker, s.cfg)
	if err != nil {
		return fmt.Errorf("%w: discover network: %v", ErrStackProbe, err)
	}

	var errs []error
	if err := s.reloadContainer(ctx, envoyContainerName, s.envoyContainerSpec(netInfo)); err != nil {
		errs = append(errs, fmt.Errorf("%w: %v", ErrEnvoyRestart, err))
	}
	if err := s.reloadContainer(ctx, corednsContainerName, s.corednsContainerSpec(netInfo)); err != nil {
		errs = append(errs, fmt.Errorf("%w: %v", ErrCoreDNSRestart, err))
	}
	if len(errs) == 0 {
		if err := s.WaitForHealthy(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%w: %v", ErrStackUnhealthy, err))
		}
	}
	return errors.Join(errs...)
}

// WaitForHealthy polls Envoy and CoreDNS health endpoints until both
// return HTTP 200 or the context deadline expires. On deadline expiry the
// error wraps one or both of ErrEnvoyUnhealthy/ErrCoreDNSUnhealthy.
//
// Probes hit the clawker network via internal container IPs — the CP shares the
// network, so host port forwarding is not required.
func (s *Stack) WaitForHealthy(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	netInfo, err := DiscoverNetwork(ctx, s.docker, s.cfg)
	if err != nil {
		return fmt.Errorf("discover network for health probes: %w", err)
	}

	start := time.Now()
	// The ctx deadline only ever tightens the budget (min of the two): the
	// queued bringup closure carries a whole-bringup deadline, and letting it
	// replace the health budget would defer health failures to the outer
	// deadline — surfacing them as a generic DeadlineExceeded instead of the
	// typed ErrEnvoyUnhealthy/ErrCoreDNSUnhealthy.
	deadline := start.Add(healthCheckTimeout)
	if dl, ok := ctx.Deadline(); ok {
		if time.Until(dl) <= 0 {
			return context.DeadlineExceeded
		}
		if dl.Before(deadline) {
			deadline = dl
		}
	}

	httpClient := &http.Client{Timeout: 2 * time.Second}
	envoyURL := "http://" + net.JoinHostPort(netInfo.EnvoyIP, strconv.Itoa(s.cfg.EnvoyHealthPort())) + "/"
	corednsURL := "http://" + net.JoinHostPort(
		netInfo.CoreDNSIP,
		strconv.Itoa(s.cfg.CoreDNSHealthHostPort()),
	) + s.cfg.CoreDNSHealthPath()

	var envoyReady, corednsReady bool
	var envoyErr, corednsErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !envoyReady {
			envoyReady, envoyErr = probeHealth(ctx, httpClient, envoyURL)
		}
		if !corednsReady {
			corednsReady, corednsErr = probeHealth(ctx, httpClient, corednsURL)
		}
		if envoyReady && corednsReady {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(healthCheckInterval):
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	var joined error
	if !envoyReady {
		if envoyErr != nil {
			joined = errors.Join(joined, fmt.Errorf("%w: last probe: %v", ErrEnvoyUnhealthy, envoyErr))
		} else {
			joined = errors.Join(joined, ErrEnvoyUnhealthy)
		}
	}
	if !corednsReady {
		if corednsErr != nil {
			joined = errors.Join(joined, fmt.Errorf("%w: last probe: %v", ErrCoreDNSUnhealthy, corednsErr))
		} else {
			joined = errors.Join(joined, ErrCoreDNSUnhealthy)
		}
	}
	return &HealthTimeoutError{Timeout: time.Since(start), Err: joined}
}

// Status reports the current health of the stack plus rule count and
// network topology. Docker API errors propagate — callers distinguish
// "stack down" from "Docker unreachable".
func (s *Stack) Status(ctx context.Context) (*Status, error) {
	rules, _ := NormalizeAndDedup(s.store.Read().Rules)

	envoyRunning, err := s.isRunning(ctx, envoyContainerName)
	if err != nil {
		return nil, fmt.Errorf("firewall stack status: %w", err)
	}
	corednsRunning, err := s.isRunning(ctx, corednsContainerName)
	if err != nil {
		return nil, fmt.Errorf("firewall stack status: %w", err)
	}

	st := &Status{
		Running:       envoyRunning && corednsRunning,
		EnvoyHealth:   envoyRunning,
		CoreDNSHealth: corednsRunning,
		RuleCount:     len(rules),
	}

	// A missing network is legitimate pre-bring-up state — leave IP/ID
	// fields empty rather than failing the Status call. Any discovery
	// error (missing network, whail DockerError wrapping cerrdefs, or a
	// genuine Docker outage) is logged at Warn and the network fields
	// stay empty; Status callers already distinguish "stack down" from
	// "Docker unreachable" via the isRunning results above.
	if netInfo, err := DiscoverNetwork(ctx, s.docker, s.cfg); err == nil {
		st.EnvoyIP = netInfo.EnvoyIP
		st.CoreDNSIP = netInfo.CoreDNSIP
		st.NetworkID = netInfo.NetworkID
	} else {
		s.log.Warn().Err(err).Msg("firewall stack status: network discovery failed, reporting without topology")
	}
	return st, nil
}

// EnvoyIP, CoreDNSIP, NetworkID, and CIDR return the current network
// topology. They re-discover on every call; they are intended for
// display paths where the cost is negligible. On failure they return ""
// rather than an error — accessors never panic or block callers.
func (s *Stack) EnvoyIP() string   { return s.discoverOrEmpty().EnvoyIP }
func (s *Stack) CoreDNSIP() string { return s.discoverOrEmpty().CoreDNSIP }
func (s *Stack) NetworkID() string { return s.discoverOrEmpty().NetworkID }
func (s *Stack) CIDR() string      { return s.discoverOrEmpty().CIDR }

// NetworkInfo returns the current firewall network topology (Envoy/CoreDNS
// IPs, CIDR, gateway, network ID). Unlike the string accessors above it
// surfaces discovery errors — callers on the enforcement path (FirewallEnable)
// must fail loudly when topology is unknown rather than silently enrolling
// with zero values.
func (s *Stack) NetworkInfo(ctx context.Context) (*NetworkInfo, error) {
	return DiscoverNetwork(ctx, s.docker, s.cfg)
}

func (s *Stack) discoverOrEmpty() NetworkInfo {
	netInfo, err := DiscoverNetwork(context.Background(), s.docker, s.cfg)
	if err != nil || netInfo == nil {
		return NetworkInfo{}
	}
	return *netInfo
}

// --- Internal helpers ---

// ensureNetworkAndDiscover creates the clawker network if missing and returns the
// discovered topology. The CLI is the primary owner of network creation;
// the defensive guard here protects against a stale CP image that starts
// before bootstrap has run EnsureNetwork host-side.
func (s *Stack) ensureNetworkAndDiscover(ctx context.Context) (*NetworkInfo, error) {
	if _, err := s.docker.EnsureNetwork(ctx, whail.EnsureNetworkOptions{
		Name: s.cfg.ClawkerNetwork(),
	}); err != nil {
		return nil, fmt.Errorf("ensure clawker-net: %w", err)
	}
	netInfo, err := DiscoverNetwork(ctx, s.docker, s.cfg)
	if err != nil {
		return nil, fmt.Errorf("discover clawker-net: %w", err)
	}
	s.log.Debug().
		Str("envoy_ip", netInfo.EnvoyIP).
		Str("coredns_ip", netInfo.CoreDNSIP).
		Str("cidr", netInfo.CIDR).
		Msg("firewall stack network discovered")
	return netInfo, nil
}

// ensureConfigs writes envoy.yaml, Corefile, and TLS certs from current
// rules store state. Returns the firewall data dir where those files
// live so container specs can bind-mount them.
func (s *Stack) ensureConfigs() (string, error) {
	dataDir, err := consts.FirewallDataSubdir()
	if err != nil {
		return "", fmt.Errorf("resolving firewall data dir: %w", err)
	}
	certDir, err := consts.FirewallCertSubdir()
	if err != nil {
		return "", fmt.Errorf("resolving firewall cert dir: %w", err)
	}

	caCert, caKey, err := EnsureCA(certDir)
	if err != nil {
		return "", fmt.Errorf("ensuring CA: %w", err)
	}

	// Heal legacy/partial rule state: normalize the stored rules and re-save
	// when the canonical form differs. The read-modify-write is serialized via a
	// store transaction so it can't interleave with a concurrent handler rule
	// write and lose an update.
	var rules []config.EgressRule
	if err = s.store.Txn(func(tx *storage.Tx[EgressRulesFile]) error {
		var stored []config.EgressRule
		if _, gErr := tx.Get(rulesField, &stored); gErr != nil {
			return fmt.Errorf("reading rules store: %w", gErr)
		}
		var warnings []string
		rules, warnings = NormalizeAndDedup(stored)
		// Log warnings here (not after the Txn) so they survive a heal-write
		// failure below.
		for _, w := range warnings {
			s.log.Warn().Msg(w)
		}
		healed := false
		if len(rules) != len(stored) {
			healed = true
		} else {
			for i, r := range stored {
				n := rules[i]
				if r.Proto != n.Proto || r.Action != n.Action || r.Port != n.Port {
					healed = true
					break
				}
			}
		}
		if healed {
			if sErr := s.store.Set(rulesField, rules); sErr != nil {
				return fmt.Errorf("healing rules store: %w", sErr)
			}
			if wErr := s.store.Write(); wErr != nil {
				return fmt.Errorf("writing healed rules: %w", wErr)
			}
			s.log.Info().Int("rules", len(rules)).Msg("healed legacy rules in store")
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("syncing rules store: %w", err)
	}

	if err := RegenerateDomainCerts(rules, certDir, caCert, caKey); err != nil {
		return "", fmt.Errorf("regenerating domain certs: %w", err)
	}

	s.infraCertsReady = false
	if s.otelCerts == nil {
		// Distinct from the mint-failure path below: this is the
		// intentionally-cold state (no monitoring stack wired, or CP-side
		// intermediate load failed at startup). Logged at Info — the
		// operator running `clawker monitor up` and then noticing no
		// DNS/Envoy access logs in OpenSearch needs a discoverable
		// signal that the cold state is by design; Debug would bury
		// it under the default log level and force them to chase the
		// mTLS material on disk. Reload-on-rule-change cadence is low
		// enough that this is not noisy.
		s.log.Info().
			Str("event", "infra_client_certs_skipped").
			Str("component", "firewall.stack").
			Str("reason", "no_provisioner").
			Msg("otelcerts provisioner not wired — OTLP mTLS push from envoy/coredns disabled (intentionally cold)")
	} else if err := s.ensureInfraClientCerts(); err != nil {
		// Degraded path: infra client cert minting failure is logged
		// but not fatal. Envoy + CoreDNS continue to start without
		// the mTLS material wired in — Envoy omits the OTel access
		// log sink + otel_collector_als cluster entirely (alsConfig
		// returns the empty struct, and the sender-side gate in
		// buildHTTPAccessLog / buildTCPAccessLog / buildClusters
		// drops everything OTel-related). The CoreDNS otel plugin
		// sees no CLAWKER_COREDNS_OTEL_ENDPOINT env var so it
		// installs noopEmitter. Stdout sinks remain wired for
		// `docker logs` triage, but the OpenSearch ingestion path
		// stays cold (no filelog receiver). Infra services must
		// never cross into the untrusted otel-collector:4317 lane
		// reserved for agent containers. Without this gate (i.e.
		// wiring the bind-mounts when s.infraIssuer != nil regardless
		// of mint success), CoreDNS's otel plugin would
		// tls.LoadX509KeyPair missing/partial files at setup time,
		// return plugin.Error, and take the CoreDNS container down —
		// breaking the firewall plane on a telemetry failure.
		s.log.Warn().Err(err).
			Str("event", "infra_client_certs_unavailable").
			Str("component", "firewall.stack").
			Msg("infra client cert minting failed — OTLP mTLS push from envoy/coredns disabled")
	} else {
		s.infraCertsReady = true
	}

	envoyYAML, warnings, err := GenerateEnvoyConfig(rules, s.envoyPorts(), s.alsConfig())
	if err != nil {
		return "", fmt.Errorf("generating envoy config: %w", err)
	}
	for _, w := range warnings {
		s.log.Warn().Str("component", "envoy").Msg(w)
	}
	envoyPath, err := consts.EnvoyConfigPath()
	if err != nil {
		return "", fmt.Errorf("resolving envoy config path: %w", err)
	}
	if err := os.WriteFile(envoyPath, envoyYAML, 0o644); err != nil {
		return "", fmt.Errorf("writing envoy.yaml: %w", err)
	}

	corefile, err := GenerateCorefile(rules, s.cfg.CoreDNSHealthHostPort())
	if err != nil {
		return "", fmt.Errorf("generating Corefile: %w", err)
	}
	corefilePath, err := consts.CorefilePath()
	if err != nil {
		return "", fmt.Errorf("resolving Corefile path: %w", err)
	}
	if err := os.WriteFile(corefilePath, corefile, 0o644); err != nil {
		return "", fmt.Errorf("writing Corefile: %w", err)
	}
	return dataDir, nil
}

// ensureInfraClientCerts mints short-lived mTLS client leaves for the
// infra services that push telemetry through the CP-only OTLP
// receiver (Envoy + CoreDNS today; future hostproxy sidecars plug in
// here). Files land under OtelClientsDir:
//
//	<dir>/envoy/client.pem  (leaf + intermediate chain)
//	<dir>/envoy/client.key
//	<dir>/envoy/ca.pem      (CLI root CA, copied from CPCACertPath)
//	<dir>/coredns/client.pem
//	<dir>/coredns/client.key
//	<dir>/coredns/ca.pem
//
// All leaves are 1-year TTL — same shape as the per-domain MITM
// certs. EnsureRunning re-runs through ensureConfigs which re-runs
// this, so container restarts naturally re-issue fresh certs without
// a renewal goroutine.
//
// No-op when s.otelCerts is nil (CP-side intermediate load failed
// at startup); container specs degrade by not bind-mounting the cert
// material, so receiver-side mTLS handshakes are simply never
// attempted.
//
// The mint + atomic-write + pair-check work lives in
// `./controlplane/otelcerts` — this method only dispatches per
// sibling service and propagates the first error. File perms, layout,
// and the CLI-root ca.pem copy are owned by the provisioner.
func (s *Stack) ensureInfraClientCerts() error {
	if s.otelCerts == nil {
		return nil
	}
	for _, svc := range []string{"envoy", "coredns"} {
		if _, _, _, err := s.otelCerts.EnsureClient(svc); err != nil {
			return fmt.Errorf("provision %s otel client material: %w", svc, err)
		}
	}
	return nil
}

// alsConfig returns the Envoy access logger upstream config. When
// ensureInfraClientCerts has populated the cert material (which
// implies a wired infra issuer — infraCertsReady is only set true
// after a successful mint) the ALS cluster targets the mTLS-gated
// trusted receiver. Otherwise it returns the empty struct; in that
// degraded mode the OTel access-log sink + otel_collector_als
// cluster are omitted entirely (gated at the sender in
// buildHTTPAccessLog / buildTCPAccessLog / buildClusters), and Envoy
// keeps only the stdout JSON sink for `docker logs clawker-envoy`
// triage. Infra services must never cross into the untrusted
// otel-collector:4317 lane reserved for agent containers — degraded
// telemetry is silent at the trusted receiver, never spoofable noise
// on the untrusted one.
//
// Reads s.infraCertsReady; assumed invoked from the controlplane
// ActionQueue worker goroutine so the field observes the most recent
// ensureConfigs run coherently (see Stack docstring).
func (s *Stack) alsConfig() ALSConfig {
	if !s.infraCertsReady {
		return ALSConfig{}
	}
	return ALSConfig{Port: int(s.cfg.SettingsStore().Read().Monitoring.OtelInfraPort), MTLS: true}
}

func (s *Stack) envoyPorts() EnvoyPorts {
	return EnvoyPorts{
		EgressPort:  s.cfg.EnvoyEgressPort(),
		TCPPortBase: s.cfg.EnvoyTCPPortBase(),
		UDPPortBase: s.cfg.EnvoyUDPPortBase(),
		HealthPort:  s.cfg.EnvoyHealthPort(),
	}
}

// containerSpec captures the subset of container creation inputs Stack
// actually varies between Envoy and CoreDNS — everything else (managed
// label, network attachment) is derived from config + netInfo inside
// ensureContainer. Kept internal to Stack.
type containerSpec struct {
	image        string
	staticIP     string
	networkID    string
	cmd          []string
	env          []string
	mounts       []mount.Mount
	portBindings network.PortMap
	capAdd       []string
	// labels carries the state-encoding labels that distinguish drift-
	// significant create-time options (today: labelInfraCertsReady).
	// Compared by ensureContainer / reloadContainer to decide whether
	// a running container needs to be recreated rather than restarted.
	labels map[string]string
}

// envoyContainerSpec composes the Envoy container spec for the current
// stack state. Reads s.infraCertsReady; callers must invoke from the
// controlplane ActionQueue worker goroutine (the same single goroutine
// that mutates the flag inside ensureConfigs) so the spec observes the
// flag coherently with the most recent mint result.
func (s *Stack) envoyContainerSpec(netInfo *NetworkInfo) containerSpec {
	// Bind Mount.Source values must be host-FS paths — the Docker daemon
	// resolves them on the host, not inside the CP container where the
	// writer accessors (consts.EnvoyConfigPath / consts.FirewallCertSubdir)
	// resolve to. consts.HostEnvoyConfigPath / consts.HostFirewallCertSubdir
	// are derived from the CLAWKER_HOST_*_DIR env vars the CLI injects at
	// CP container creation.
	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   consts.HostEnvoyConfigPath,
			Target:   "/etc/envoy/envoy.yaml",
			ReadOnly: true,
		},
		{
			Type:     mount.TypeBind,
			Source:   consts.HostFirewallCertSubdir,
			Target:   "/etc/envoy/certs",
			ReadOnly: true,
		},
	}
	// mTLS client material for the OTLP/gRPC access logger. Only attached
	// when the infra issuer is wired AND ensureInfraClientCerts populated
	// the cert files; without it Envoy emits no OTLP at all (alsConfig +
	// buildHTTPAccessLog / buildTCPAccessLog gate on infraCertsReady →
	// als.MTLS, so the OTel sink + cluster are dropped end-to-end). The
	// stdout JSON sink remains for `docker logs` triage. Gating on
	// infraCertsReady (not infraIssuer != nil) prevents bind-mounting a
	// partially-populated dir after a mint failure.
	//
	// Drift-on-Reload: the bind-mount set is part of the container's
	// immutable create-time config, so flipping infraCertsReady on a
	// later Reload would diverge from the running container. driftLabels
	// stamps labelInfraCertsReady at create time; specMatchesContainer
	// detects the mismatch and reloadContainer falls through to
	// ensureContainer, which stop+remove+recreates with the new mount.
	if s.infraCertsReady {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   filepath.Join(consts.HostFirewallOtelCertsDir, "envoy"),
			Target:   "/etc/envoy/otel-tls",
			ReadOnly: true,
		})
	}
	return containerSpec{
		image:     envoyImage,
		staticIP:  netInfo.EnvoyIP,
		networkID: netInfo.NetworkID,
		mounts:    mounts,
		// Publish the dedicated health listener — NOT the TLS egress port.
		// Publishing TLS would install NAT rules that masquerade the source
		// IP of DNAT'd inter-container traffic.
		portBindings: network.PortMap{
			network.MustParsePort(fmt.Sprintf("%d/tcp", s.cfg.EnvoyHealthPort())): {
				{HostPort: strconv.Itoa(s.cfg.EnvoyHealthHostPort())},
			},
		},
		labels: s.driftLabels(),
	}
}

// corednsContainerSpec composes the CoreDNS container spec for the
// current stack state. Reads s.infraCertsReady; callers must invoke
// from the controlplane ActionQueue worker goroutine (see
// envoyContainerSpec doc) so the spec observes the flag coherently
// with the most recent mint result.
func (s *Stack) corednsContainerSpec(netInfo *NetworkInfo) containerSpec {
	healthPort := consts.CoreDNSHealthHostPort
	// Mount.Source is a host-FS path; consts.HostCorefilePath is derived
	// from the CLAWKER_HOST_DATA_DIR env var the CLI injects into the CP
	// container at creation.
	//
	// CoreDNS log shipping: the custom `otel` plugin (cmd/coredns-clawker
	// /plugins/otel) emits one OTLP log record per query over mTLS to
	// the CP-only receiver at otel-collector:OtelInfraPort. CLAWKER_COREDNS_
	// OTEL_ENDPOINT in container env (set by container_env below) wires
	// the plugin to the receiver; the cert + key + CA bind-mount at
	// /etc/clawker/auth/coredns/ matches the plugin's hardcoded
	// defaults (see plugins/otel/setup.go). Gated on infraCertsReady so
	// a mint failure does NOT wire the env var or mount — without the
	// env var, the plugin installs noopEmitter and CoreDNS starts
	// cleanly; with the env var pointing at missing files, the plugin
	// returns plugin.Error and CoreDNS refuses to start (taking the
	// firewall plane down on a telemetry failure).
	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   consts.HostCorefilePath,
			Target:   "/etc/coredns/Corefile",
			ReadOnly: true,
		},
		{
			// The dnsbpf plugin updates the pinned dns_cache map
			// under the clawker BPF pin path in real time.
			Type:   mount.TypeBind,
			Source: "/sys/fs/bpf",
			Target: "/sys/fs/bpf",
		},
	}
	var env []string
	if s.infraCertsReady {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   filepath.Join(consts.HostFirewallOtelCertsDir, "coredns"),
			Target:   "/etc/clawker/auth/coredns",
			ReadOnly: true,
		})
		// otlploggrpc.WithEndpoint takes a host:port; the plugin upgrades
		// to TLS via the client cert config it loads from the bind-mounted
		// paths below.
		env = append(env, fmt.Sprintf(consts.EnvCoreDNSOtelEndpoint+"=%s:%d",
			consts.MonitoringServiceOtelCollector,
			s.cfg.SettingsStore().Read().Monitoring.OtelInfraPort))
	}
	return containerSpec{
		image:     corednsImageTag,
		staticIP:  netInfo.CoreDNSIP,
		networkID: netInfo.NetworkID,
		cmd:       []string{"-conf", "/etc/coredns/Corefile"},
		mounts:    mounts,
		env:       env,
		portBindings: network.PortMap{
			network.MustParsePort(fmt.Sprintf("%d/tcp", healthPort)): {
				{HostPort: strconv.Itoa(healthPort)},
			},
		},
		// CAP_BPF: bpf(BPF_OBJ_GET) to open the pinned dns_cache map.
		// CAP_SYS_ADMIN: bpf(BPF_MAP_UPDATE_ELEM) on kernels <5.19 where
		// CAP_BPF alone is insufficient for map writes.
		capAdd: []string{"BPF", "SYS_ADMIN"},
		labels: s.driftLabels(),
	}
}

// driftLabels returns the drift-significant labels for the current
// Stack state — applied at ContainerCreate and re-read by
// ensureContainer / reloadContainer to detect cases where a plain
// ContainerRestart would leave create-time env/mounts out of sync
// with the freshly-generated envoy.yaml / Corefile.
func (s *Stack) driftLabels() map[string]string {
	return map[string]string{
		labelInfraCertsReady: strconv.FormatBool(s.infraCertsReady),
		labelOtelInfraPort:   strconv.Itoa(int(s.cfg.SettingsStore().Read().Monitoring.OtelInfraPort)),
		labelStackBuildSHA:   consts.CPBinarySHA,
	}
}

// specMatchesContainer reports whether the running container's
// drift-significant labels match the desired spec. Missing labels on
// the container side are treated as mismatch — an older clawker
// version may have created the container before labelInfraCertsReady
// existed, and forcing a recreate brings it under the new gate.
func specMatchesContainer(actual map[string]string, want map[string]string) bool {
	for k, v := range want {
		if actual[k] != v {
			return false
		}
	}
	return true
}

// ensureContainer creates + starts the named container if missing, or
// starts an existing stopped one. Idempotent — if the container is
// already running AND its drift-significant labels match the desired
// spec it returns without change.
//
// Spec drift triggers a stop + remove + recreate. The drift axes are
// the labels stamped by driftLabels: each captures create-time state
// (the mTLS-OTLP bind-mount/env shape, the OTLP infra port, the CP
// build hash whose embedded binaries/templates produced the sibling)
// that a plain ContainerRestart cannot refresh — recreating is the
// only way to push a new create-time layout into the live container.
func (s *Stack) ensureContainer(ctx context.Context, name string, spec containerSpec) error {
	summary, err := s.findByName(ctx, name)
	if err != nil {
		return err
	}
	if summary != nil {
		if specMatchesContainer(summary.Labels, spec.labels) {
			if summary.State == container.StateRunning {
				s.log.Debug().Str("container", name).Msg("firewall container already running")
				return nil
			}
			s.log.Debug().
				Str("container", name).
				Str("state", string(summary.State)).
				Msg("starting existing firewall container")
			if _, err = s.docker.ContainerStart(
				ctx,
				whail.ContainerStartOptions{
					ContainerStartOptions: whail.SDKContainerStartOptions{
						CheckpointID:  "",
						CheckpointDir: "",
					},
					ContainerID:   summary.ID,
					EnsureNetwork: nil,
				},
			); err != nil {
				return fmt.Errorf("starting existing container %s: %w", name, err)
			}
			return nil
		}
		s.log.Info().
			Str("event", "firewall_container_spec_drift").
			Str("component", "firewall.stack").
			Str("container", name).
			Str("desired_infra_certs_ready", spec.labels[labelInfraCertsReady]).
			Str("running_infra_certs_ready", summary.Labels[labelInfraCertsReady]).
			Str("desired_otel_infra_port", spec.labels[labelOtelInfraPort]).
			Str("running_otel_infra_port", summary.Labels[labelOtelInfraPort]).
			Str("desired_stack_build_sha", spec.labels[labelStackBuildSHA]).
			Str("running_stack_build_sha", summary.Labels[labelStackBuildSHA]).
			Msg("recreating firewall container — desired spec diverges from running container")
		if err := s.stopAndRemove(ctx, name); err != nil {
			return fmt.Errorf("recreating %s on spec drift: %w", name, err)
		}
		// fall through to create below
	}

	ip, _ := netip.ParseAddr(spec.staticIP)
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			s.cfg.ClawkerNetwork(): {
				NetworkID:  spec.networkID,
				IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: ip},
			},
		},
	}
	containerCfg := &container.Config{Image: spec.image, Cmd: spec.cmd, Env: spec.env}
	hostCfg := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Mounts:        spec.mounts,
		PortBindings:  spec.portBindings,
		CapAdd:        spec.capAdd,
	}

	extraLabels := whail.Labels{{s.cfg.LabelPurpose(): s.cfg.PurposeFirewall()}}
	if len(spec.labels) > 0 {
		extraLabels = append(extraLabels, spec.labels)
	}
	createResp, err := s.docker.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name:             name,
		Config:           containerCfg,
		HostConfig:       hostCfg,
		NetworkingConfig: networkingConfig,
		ExtraLabels:      extraLabels,
	})
	if err != nil {
		// Another process may have created the container between
		// findByName and ContainerCreate. Treat "already in use" as
		// "already present" and recover by starting whatever landed.
		if strings.Contains(err.Error(), "is already in use") {
			if recovered, recErr := s.findByName(ctx, name); recErr == nil && recovered != nil {
				if recovered.State == container.StateRunning {
					return nil
				}
				_, startErr := s.docker.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: recovered.ID})
				return startErr
			}
		}
		return fmt.Errorf("creating container %s: %w", name, err)
	}
	if _, err := s.docker.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: createResp.ID}); err != nil {
		return fmt.Errorf("starting container %s: %w", name, err)
	}
	return nil
}

// findByName returns the summary of the named managed container or nil if
// it does not exist. Using ContainerList (not ContainerInspect) avoids the
// managed-or-not-managed ambiguity of inspect — the list call already
// filters to managed resources.
func (s *Stack) findByName(ctx context.Context, name string) (*container.Summary, error) {
	filters := whail.Filters{}.Add("name", name)
	result, err := s.docker.ContainerList(ctx, whail.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing container %s: %w", name, err)
	}
	if len(result.Items) == 0 {
		return nil, nil
	}
	return &result.Items[0], nil
}

func (s *Stack) isRunning(ctx context.Context, name string) (bool, error) {
	summary, err := s.findByName(ctx, name)
	if err != nil {
		return false, err
	}
	return summary != nil && summary.State == container.StateRunning, nil
}

// stopAndRemove is a best-effort teardown: a missing container is not an
// error, but any Docker API failure propagates so the Handler can surface
// partial-shutdown state to operators.
func (s *Stack) stopAndRemove(ctx context.Context, name string) error {
	summary, err := s.findByName(ctx, name)
	if err != nil {
		return err
	}
	if summary == nil {
		s.log.Debug().Str("container", name).Msg("container not found, skipping stop")
		return nil
	}
	if summary.State == container.StateRunning {
		if _, err := s.docker.ContainerStop(ctx, summary.ID, nil); err != nil {
			s.log.Warn().Err(err).Str("container", name).Msg("failed to stop container gracefully")
		}
	}
	if _, err := s.docker.ContainerRemove(ctx, summary.ID, true); err != nil {
		return fmt.Errorf("removing container %s: %w", name, err)
	}
	s.log.Debug().Str("container", name).Msg("container removed")
	return nil
}

// reloadContainer restarts the named container in-place when its
// drift-significant labels match the desired spec; otherwise it
// stops + removes + recreates via ensureContainer so create-time env
// and mount lists pick up the new layout. The fast restart path stays
// intact for the common case (config-file regen with no flip in
// infraCertsReady).
func (s *Stack) reloadContainer(ctx context.Context, name string, spec containerSpec) error {
	summary, err := s.findByName(ctx, name)
	if err != nil {
		return err
	}
	if summary == nil {
		return fmt.Errorf("container %s not found", name)
	}
	if !specMatchesContainer(summary.Labels, spec.labels) {
		return s.ensureContainer(ctx, name, spec)
	}
	timeout := 10
	if _, err := s.docker.ContainerRestart(ctx, summary.ID, &timeout); err != nil {
		return fmt.Errorf("restarting %s: %w", name, err)
	}
	return nil
}

// ensureEnvoyImage pulls the upstream Envoy image if it is not already
// present locally.
func (s *Stack) ensureEnvoyImage(ctx context.Context) error {
	if _, err := s.docker.ImageInspect(ctx, envoyImage); err == nil {
		return nil
	}
	s.log.Debug().Str("image", envoyImage).Msg("pulling envoy image")
	// Pulls go through the raw moby client — whail has no pull method
	// because pulls do not produce a managed resource.
	reader, err := s.docker.APIClient.ImagePull(ctx, envoyImage, mobyclient.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pulling envoy image: %w", err)
	}
	defer reader.Close()
	// Pull status is streamed as JSON frames; auth, manifest, and layer
	// failures appear in the `error` field rather than as a top-level
	// error from ImagePull.
	return drainPullStream(reader, "pulling envoy image")
}

// drainPullStream reads an image-pull JSON progress stream to completion
// and returns any inline error frame. A decode failure is treated as
// end-of-stream: the daemon signals success by the absence of an error
// frame, not by a clean EOF.
func drainPullStream(r io.Reader, ctxMsg string) error {
	dec := json.NewDecoder(r)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if decodeErr := dec.Decode(&msg); decodeErr != nil {
			//nolint:nilerr // decode failure = end-of-stream; success is signalled by absence of msg.Error, not clean EOF
			return nil
		}
		if msg.Error != "" {
			return fmt.Errorf("%s: %s", ctxMsg, msg.Error)
		}
	}
}

// ensureCorednsImage builds the custom CoreDNS image from the embedded
// coredns-clawker binary if it is not already present.
func (s *Stack) ensureCorednsImage(ctx context.Context) error {
	if _, err := s.docker.ImageInspect(ctx, corednsImageTag); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("checking %s image: %w", corednsImageTag, err)
	}

	if len(CoreDNSClawkerBinary) == 0 {
		return fmt.Errorf("%s binary not embedded — run 'make coredns-binary' then rebuild clawker", corednsImageTag)
	}

	buildCtx, err := corednsBuildContext()
	if err != nil {
		return fmt.Errorf("creating coredns build context: %w", err)
	}
	resp, err := s.docker.ImageBuild(ctx, buildCtx, whail.ImageBuildOptions{
		Tags:           []string{corednsImageTag},
		Dockerfile:     "Dockerfile",
		Remove:         true,
		ForceRemove:    true,
		SuppressOutput: true,
	})
	if err != nil {
		return fmt.Errorf("building coredns image: %w", err)
	}
	defer resp.Body.Close()

	// Build errors are reported inline in the streaming JSON response
	// rather than as a top-level error — decode and surface them.
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		// Treat any decode failure (EOF, malformed frame, truncated stream)
		// as end-of-build — we've drained as much status as the daemon will
		// give us. Build success/failure is signalled by the presence of
		// msg.Error inside a frame, not by the decoder returning io.EOF.
		if err := dec.Decode(&msg); err != nil {
			break
		}
		if msg.Error != "" {
			return fmt.Errorf("building coredns image: %s", msg.Error)
		}
	}
	return nil
}

// corednsDockerfile pins the alpine base by digest to match the
// equivalent image produced elsewhere in the codebase.
const corednsDockerfile = "FROM alpine:3.21@sha256:a8560b36e8b8210634f77d9f7f9efd7ffa463e380b75e2e74aff4511df3ef88c\n" +
	"COPY coredns /usr/local/bin/coredns\n" +
	"ENTRYPOINT [\"/usr/local/bin/coredns\"]\n"

// Tar entry permission bits for the CoreDNS build context: the Dockerfile is
// plain data, the coredns binary must be executable.
const (
	corednsTarFileMode = 0o644
	corednsTarExecMode = 0o755
)

// corednsBuildContext assembles the two-file tar archive (Dockerfile +
// coredns binary) that ImageBuild expects.
func corednsBuildContext() (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Size: int64(len(corednsDockerfile)),
		Mode: corednsTarFileMode,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(corednsDockerfile)); err != nil {
		return nil, err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "coredns",
		Size: int64(len(CoreDNSClawkerBinary)),
		Mode: corednsTarExecMode,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(CoreDNSClawkerBinary); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

// probeHealth does a single GET against url. Returns (true, nil) on HTTP
// 200, (false, err) on any other outcome (network error, non-200, ctx
// error). Caller decides whether to retry based on its own deadline.
func probeHealth(ctx context.Context, httpClient *http.Client, url string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return true, nil
}
