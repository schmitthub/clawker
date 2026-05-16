package firewall

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/tls"
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

	envoyContainerName   = "clawker-envoy"
	corednsContainerName = "clawker-coredns"

	// healthCheckTimeout bounds WaitForHealthy by default. Callers supply
	// deadlines via ctx when they want different behavior.
	healthCheckTimeout  = 60 * time.Second
	healthCheckInterval = 500 * time.Millisecond
)

// Stack manages the Envoy + CoreDNS container pair via Docker-outside-of-
// Docker from inside the control plane container. The CP container itself
// is created host-side by CLI bootstrap, not by Stack.
//
// Stack is not safe for concurrent EnsureRunning + Stop calls; callers
// serialize via their own mutex.
type Stack struct {
	docker      *docker.Client
	cfg         config.Config
	log         *logger.Logger
	store       *storage.Store[EgressRulesFile]
	infraIssuer InfraIssuer
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

// InfraIssuer mints short-lived mTLS client leaves for clawker infra
// services pushing telemetry to the CP-only OTLP receiver. Satisfied
// by *infracerts.Issuer. The interface keeps Stack unit-testable
// (`firewall` package owns no crypto; tests pass a tiny fake).
//
// May be nil — Stack tolerates a missing issuer by skipping the
// mTLS material bind-mounts; Envoy/CoreDNS fall back to stdout/
// filelog and the mTLS OTLP push path stays cold. This matches the
// CP-side degraded path (see cmd/clawker-cp/main.go: event=
// infra_issuer_unavailable).
type InfraIssuer interface {
	MintClient(serviceName string, ttl time.Duration) (chainPEM, keyPEM []byte, err error)
}

// NewStack returns an initialized Stack. log may be nil (a Nop logger is
// substituted); the other dependencies are required — nil docker or cfg
// produces a nil Stack that panics at first use, which is preferable to
// silent no-ops. infraIssuer may be nil — see InfraIssuer.
func NewStack(dc *docker.Client, cfg config.Config, log *logger.Logger, store *storage.Store[EgressRulesFile], infraIssuer InfraIssuer) *Stack {
	if log == nil {
		log = logger.Nop()
	}
	return &Stack{docker: dc, cfg: cfg, log: log, store: store, infraIssuer: infraIssuer}
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

// Stop removes Envoy + CoreDNS. The clawker-net network and eBPF state
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

	var errs []error
	if err := s.restart(ctx, envoyContainerName); err != nil {
		errs = append(errs, fmt.Errorf("%w: %v", ErrEnvoyRestart, err))
	}
	if err := s.restart(ctx, corednsContainerName); err != nil {
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
// Probes hit clawker-net via internal container IPs — the CP shares the
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
	deadline := start.Add(healthCheckTimeout)
	if dl, ok := ctx.Deadline(); ok {
		if time.Until(dl) <= 0 {
			return context.DeadlineExceeded
		}
		deadline = dl
	}

	httpClient := &http.Client{Timeout: 2 * time.Second}
	envoyURL := "http://" + net.JoinHostPort(netInfo.EnvoyIP, strconv.Itoa(s.cfg.EnvoyHealthPort())) + "/"
	corednsURL := "http://" + net.JoinHostPort(netInfo.CoreDNSIP, strconv.Itoa(s.cfg.CoreDNSHealthHostPort())) + s.cfg.CoreDNSHealthPath()

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

// ensureNetworkAndDiscover creates clawker-net if missing and returns the
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

	// Heal legacy/partial rule state inside a Set closure so concurrent
	// CLI writers see the normalized result atomically.
	var rules []config.EgressRule
	var healed bool
	if err := s.store.Set(func(f *EgressRulesFile) {
		var warnings []string
		rules, warnings = NormalizeAndDedup(f.Rules)
		for _, w := range warnings {
			s.log.Warn().Msg(w)
		}
		if len(rules) != len(f.Rules) {
			healed = true
		} else {
			for i, r := range f.Rules {
				n := rules[i]
				if r.Proto != n.Proto || r.Action != n.Action || r.Port != n.Port {
					healed = true
					break
				}
			}
		}
		if healed {
			f.Rules = rules
		}
	}); err != nil {
		return "", fmt.Errorf("healing rules store: %w", err)
	}
	if healed {
		if err := s.store.Write(); err != nil {
			return "", fmt.Errorf("writing healed rules: %w", err)
		}
		s.log.Info().Int("rules", len(rules)).Msg("healed legacy rules in store")
	}

	if err := RegenerateDomainCerts(rules, certDir, caCert, caKey); err != nil {
		return "", fmt.Errorf("regenerating domain certs: %w", err)
	}

	s.infraCertsReady = false
	if s.infraIssuer == nil {
		// Distinct from the mint-failure path below: this is the
		// intentionally-cold state (no monitoring stack wired, or CP-side
		// intermediate load failed at startup). Logged at Debug so the
		// operator can confirm "skipped, not broken" when diagnosing a
		// silent OTLP-cold stack — Warn would be noise on every reload.
		s.log.Debug().
			Str("event", "infra_client_certs_skipped").
			Str("component", "firewall.stack").
			Str("reason", "no_issuer").
			Msg("infra issuer not wired — OTLP mTLS push from envoy/coredns disabled (intentionally cold)")
	} else if err := s.ensureInfraClientCerts(); err != nil {
		// Degraded path: infra client cert minting failure is logged
		// but not fatal. Envoy + CoreDNS continue to start without
		// the mTLS material wired in — Envoy ALS falls back to the
		// stdout JSON sink (alsConfig returns the empty struct) and
		// the CoreDNS otel plugin sees no CLAWKER_COREDNS_OTEL_ENDPOINT
		// env var so it installs noopEmitter. Without this gate (i.e.
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

// rootCASourcePath returns the in-container path to the CLI root CA
// the CP bind-mounts. Exposed as a func so unit tests can override
// when running off-container.
var rootCASourcePath = func() string { return consts.CPCACertPath }

// ensureInfraClientCerts mints short-lived mTLS client leaves for the
// infra services that push telemetry through the CP-only OTLP
// receiver (Envoy + CoreDNS today; future hostproxy sidecars plug in
// here). Files land under FirewallOtelClientsDir:
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
// No-op when s.infraIssuer is nil (CP-side intermediate load failed
// at startup); container specs degrade by not bind-mounting the cert
// material, so receiver-side mTLS handshakes are simply never
// attempted.
func (s *Stack) ensureInfraClientCerts() error {
	if s.infraIssuer == nil {
		return nil
	}
	dir, err := consts.FirewallOtelClientsDir()
	if err != nil {
		return fmt.Errorf("resolve otel-clients dir: %w", err)
	}

	// Copy the CLI root CA (already bind-mounted RO into the CP at
	// CPCACertPath) into the otel-clients dir so sibling containers
	// can bind-mount a stable path. Containers cannot share the CP's
	// own root CA mount because mount sources must be host-FS paths,
	// and the CP's root CA mount source is the CLI auth dir which the
	// firewall stack does not know the host-FS path of.
	src := rootCASourcePath()
	caBytes, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read root CA at %s: %w", src, err)
	}
	for _, svc := range []string{"envoy", "coredns"} {
		svcDir := filepath.Join(dir, svc)
		// 0o700 on the per-service dir is the defense-in-depth the 0o644
		// key file below relies on: other local users on the host cannot
		// traverse into svcDir to read the bind-mounted key. MkdirAll
		// honors umask, so a fresh dir under a permissive host umask (or
		// one left at 0o755 by an older clawker version) needs explicit
		// tightening. Stat-first skips the chmod when the dir is already
		// at or below 0o700 — the common steady state — and avoids EPERM
		// noise on cross-account upgrades where the dir is owned by a
		// stale UID but already tight.
		if err := os.MkdirAll(svcDir, 0o700); err != nil {
			return fmt.Errorf("create %s dir: %w", svc, err)
		}
		info, statErr := os.Stat(svcDir)
		if statErr != nil {
			return fmt.Errorf("stat %s dir: %w", svc, statErr)
		}
		if info.Mode().Perm()&^0o700 != 0 {
			if err := os.Chmod(svcDir, 0o700); err != nil {
				return fmt.Errorf("tighten %s dir perms (mode=%o): %w", svc, info.Mode().Perm(), err)
			}
		}
		chainPEM, keyPEM, err := s.infraIssuer.MintClient(svc+"-otel-client", 365*24*time.Hour)
		if err != nil {
			return fmt.Errorf("mint %s leaf: %w", svc, err)
		}
		// Validate the mint output before any disk write commits:
		// a corrupted pair (mismatched cert/key, malformed PEM) caught
		// here fails the whole function so the caller leaves
		// infraCertsReady=false and Envoy/CoreDNS skip the mTLS mounts.
		// Without this round-trip, a buggy issuer could half-overwrite
		// a previously-good pair (ENOSPC mid-write, mismatched keypair)
		// and the next CoreDNS reload would refuse to start at handshake.
		if _, err := tls.X509KeyPair(chainPEM, keyPEM); err != nil {
			return fmt.Errorf("validate %s cert/key pair: %w", svc, err)
		}
		// Writes go through writeFileAtomic (tmp + rename) so a partial
		// write (ENOSPC, EINTR) leaves the prior-good file intact rather
		// than half-overwritten on disk.
		//
		// 0o644 on the key is load-bearing: CoreDNS in the upstream image
		// runs as a non-root uid, and Docker bind-mounts preserve host
		// inode perms — a stricter file mode would silently fail key load
		// at handshake. Defense-in-depth is the 0o700 svcDir above (NOT
		// the FirewallDataSubdir tree, which is 0o755 via
		// consts.subdirPathUnder); the file itself is permissive but
		// unreachable to non-root host users without traversing the
		// 0o700 directory.
		if err := writeFileAtomic(filepath.Join(svcDir, "ca.pem"), caBytes, 0o644); err != nil {
			return fmt.Errorf("write %s root CA copy: %w", svc, err)
		}
		if err := writeFileAtomic(filepath.Join(svcDir, "client.pem"), chainPEM, 0o644); err != nil {
			return fmt.Errorf("write %s cert: %w", svc, err)
		}
		if err := writeFileAtomic(filepath.Join(svcDir, "client.key"), keyPEM, 0o644); err != nil {
			return fmt.Errorf("write %s key: %w", svc, err)
		}
	}
	return nil
}

// writeFileAtomic writes data to path via tmp file + os.Rename so a
// partial write (ENOSPC, EINTR) leaves any pre-existing file intact
// rather than half-overwritten. Same-filesystem rename is atomic on
// POSIX; the .tmp lives in the same directory as path.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// alsConfig returns the Envoy access logger upstream config. When
// ensureInfraClientCerts has populated the cert material (which
// implies a wired infra issuer — infraCertsReady is only set true
// after a successful mint) AND OtelInfraPort is set, the ALS cluster
// targets the mTLS-gated receiver. Otherwise it returns the empty
// struct so Envoy falls back to the stdout JSON sink — preserves prior
// behavior when the monitoring stack pre-dates the intermediate-CA
// wiring, when no issuer is configured, or when cert minting failed.
//
// Reads s.infraCertsReady; assumed invoked from the controlplane
// ActionQueue worker goroutine so the field observes the most recent
// ensureConfigs run coherently (see Stack docstring).
func (s *Stack) alsConfig() ALSConfig {
	if !s.infraCertsReady {
		return ALSConfig{}
	}
	mon := s.cfg.SettingsStore().Read().Monitoring
	if mon.OtelInfraPort <= 0 {
		return ALSConfig{}
	}
	return ALSConfig{Port: mon.OtelInfraPort, MTLS: true}
}

func (s *Stack) envoyPorts() EnvoyPorts {
	return EnvoyPorts{
		EgressPort:  s.cfg.EnvoyEgressPort(),
		TCPPortBase: s.cfg.EnvoyTCPPortBase(),
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
	// the cert files; without it Envoy keeps shipping access logs via the
	// stdout JSON sink. Gating on infraCertsReady (not infraIssuer != nil)
	// prevents bind-mounting a partially-populated dir after a mint
	// failure.
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
			// The dnsbpf plugin updates the pinned dns_cache map at
			// /sys/fs/bpf/clawker/dns_cache in real time.
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
		if otelPort := s.cfg.SettingsStore().Read().Monitoring.OtelInfraPort; otelPort > 0 {
			// otlploggrpc.WithEndpoint takes a host:port; the plugin
			// upgrades to TLS via the client cert config it loads from
			// the bind-mounted paths below.
			env = append(env, fmt.Sprintf("CLAWKER_COREDNS_OTEL_ENDPOINT=%s:%d",
				consts.MonitoringServiceOtelCollector, otelPort))
		}
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
	}
}

// ensureContainer creates + starts the named container if missing, or
// starts an existing stopped one. Idempotent — if the container is
// already running it returns without change.
func (s *Stack) ensureContainer(ctx context.Context, name string, spec containerSpec) error {
	summary, err := s.findByName(ctx, name)
	if err != nil {
		return err
	}
	if summary != nil {
		if summary.State == container.StateRunning {
			s.log.Debug().Str("container", name).Msg("firewall container already running")
			return nil
		}
		s.log.Debug().Str("container", name).Str("state", string(summary.State)).Msg("starting existing firewall container")
		if _, err := s.docker.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: summary.ID}); err != nil {
			return fmt.Errorf("starting existing container %s: %w", name, err)
		}
		return nil
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

	createResp, err := s.docker.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name:             name,
		Config:           containerCfg,
		HostConfig:       hostCfg,
		NetworkingConfig: networkingConfig,
		ExtraLabels:      whail.Labels{{s.cfg.LabelPurpose(): s.cfg.PurposeFirewall()}},
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

func (s *Stack) restart(ctx context.Context, name string) error {
	summary, err := s.findByName(ctx, name)
	if err != nil {
		return err
	}
	if summary == nil {
		return fmt.Errorf("container %s not found", name)
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

// corednsBuildContext assembles the two-file tar archive (Dockerfile +
// coredns binary) that ImageBuild expects.
func corednsBuildContext() (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Size: int64(len(corednsDockerfile)),
		Mode: 0644,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(corednsDockerfile)); err != nil {
		return nil, err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "coredns",
		Size: int64(len(CoreDNSClawkerBinary)),
		Mode: 0755,
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
