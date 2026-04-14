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
	docker *docker.Client
	cfg    config.Config
	log    *logger.Logger
	store  *storage.Store[EgressRulesFile]
}

// NewStack returns an initialized Stack. log may be nil (a Nop logger is
// substituted); the other dependencies are required — nil docker or cfg
// produces a nil Stack that panics at first use, which is preferable to
// silent no-ops.
func NewStack(dc *docker.Client, cfg config.Config, log *logger.Logger, store *storage.Store[EgressRulesFile]) *Stack {
	if log == nil {
		log = logger.Nop()
	}
	return &Stack{docker: dc, cfg: cfg, log: log, store: store}
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

	dataDir, err := s.ensureConfigs()
	if err != nil {
		return err
	}

	if err := s.ensureCorednsImage(ctx); err != nil {
		return fmt.Errorf("firewall stack: %w", err)
	}
	if err := s.ensureEnvoyImage(ctx); err != nil {
		return fmt.Errorf("firewall stack: %w", err)
	}

	if err := s.ensureContainer(ctx, envoyContainerName, s.envoyContainerSpec(netInfo, dataDir)); err != nil {
		return fmt.Errorf("firewall stack: envoy: %w", err)
	}
	if err := s.ensureContainer(ctx, corednsContainerName, s.corednsContainerSpec(netInfo, dataDir)); err != nil {
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
func (s *Stack) Reload(ctx context.Context) error {
	if _, err := s.ensureConfigs(); err != nil {
		return fmt.Errorf("firewall stack reload: %w", err)
	}

	envoyRunning, err := s.isRunning(ctx, envoyContainerName)
	if err != nil {
		return fmt.Errorf("firewall stack reload: %w", err)
	}
	corednsRunning, err := s.isRunning(ctx, corednsContainerName)
	if err != nil {
		return fmt.Errorf("firewall stack reload: %w", err)
	}
	if !envoyRunning || !corednsRunning {
		return nil
	}

	if err := s.restart(ctx, envoyContainerName); err != nil {
		return fmt.Errorf("firewall stack reload: %w", err)
	}
	if err := s.restart(ctx, corednsContainerName); err != nil {
		return fmt.Errorf("firewall stack reload: %w", err)
	}
	return s.WaitForHealthy(ctx)
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
	dataDir, err := s.cfg.FirewallDataSubdir()
	if err != nil {
		return "", fmt.Errorf("resolving firewall data dir: %w", err)
	}
	certDir, err := s.cfg.FirewallCertSubdir()
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

	envoyYAML, warnings, err := GenerateEnvoyConfig(rules, s.envoyPorts())
	if err != nil {
		return "", fmt.Errorf("generating envoy config: %w", err)
	}
	for _, w := range warnings {
		s.log.Warn().Str("component", "envoy").Msg(w)
	}
	envoyPath := filepath.Join(dataDir, "envoy.yaml")
	if err := os.WriteFile(envoyPath, envoyYAML, 0o644); err != nil {
		return "", fmt.Errorf("writing envoy.yaml: %w", err)
	}

	corefile, err := GenerateCorefile(rules, s.cfg.CoreDNSHealthHostPort())
	if err != nil {
		return "", fmt.Errorf("generating Corefile: %w", err)
	}
	corefilePath := filepath.Join(dataDir, "Corefile")
	if err := os.WriteFile(corefilePath, corefile, 0o644); err != nil {
		return "", fmt.Errorf("writing Corefile: %w", err)
	}
	return dataDir, nil
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

func (s *Stack) envoyContainerSpec(netInfo *NetworkInfo, dataDir string) containerSpec {
	certDir, _ := s.cfg.FirewallCertSubdir() // validated in ensureConfigs
	return containerSpec{
		image:     envoyImage,
		staticIP:  netInfo.EnvoyIP,
		networkID: netInfo.NetworkID,
		mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   filepath.Join(dataDir, "envoy.yaml"),
				Target:   "/etc/envoy/envoy.yaml",
				ReadOnly: true,
			},
			{
				Type:     mount.TypeBind,
				Source:   certDir,
				Target:   "/etc/envoy/certs",
				ReadOnly: true,
			},
		},
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

func (s *Stack) corednsContainerSpec(netInfo *NetworkInfo, dataDir string) containerSpec {
	healthPort := s.cfg.CoreDNSHealthHostPort()
	return containerSpec{
		image:     corednsImageTag,
		staticIP:  netInfo.CoreDNSIP,
		networkID: netInfo.NetworkID,
		cmd:       []string{"-conf", "/etc/coredns/Corefile"},
		mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   filepath.Join(dataDir, "Corefile"),
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
		},
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

	if len(CorednsClawkerBinary) == 0 {
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
		Size: int64(len(CorednsClawkerBinary)),
		Mode: 0755,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(CorednsClawkerBinary); err != nil {
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
