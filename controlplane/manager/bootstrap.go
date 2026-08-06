package manager

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/adminclient"
	fwcp "github.com/schmitthub/clawker/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
)

// Host-side CP lifecycle constants.
const (
	// cpReady* bound /healthz polling after container start.
	cpReadyTimeout  = 60 * time.Second
	cpReadyInterval = 100 * time.Millisecond

	// healthzRequestTimeout bounds one /healthz HTTP sample.
	healthzRequestTimeout = 2 * time.Second

	// cpClockSync* gate readiness on host↔CP clock alignment, run as the
	// final readiness check after /healthz is green. All assertions are
	// minted in the host clock (the source of truth); Hydra validates its iat
	// against the CP clock with zero leeway. A lagging Docker Desktop VM clock
	// (e.g. after host sleep, before it re-syncs to the host) would otherwise
	// put a host-domain iat in the CP's future — a "token used before issued"
	// rejection. Because the bringup is the every-start precondition,
	// polling GetSystemTime until the CP clock is no longer behind the host
	// guarantees it has caught up before assertion exchange begins.
	cpClockSyncTimeout  = 30 * time.Second
	cpClockSyncInterval = 500 * time.Millisecond

	// cpStopTimeout (seconds) is the grace period before SIGKILL on Stop.
	cpStopTimeout = 30

	// sosRetryInterval paces the readiness loop's SOS checks so an
	// unhealthy CP is not pressed with a new WatchSOS stream on every
	// 100ms healthz sample.
	sosRetryInterval = 500 * time.Millisecond

	// sosCheckTimeout bounds one WatchSOS look. The CP replays a pending
	// SOS immediately on subscribe, so a short receive window is enough
	// to hear it; an idle stream is cancelled at the timeout and the
	// queue is looked at again on a later pass.
	sosCheckTimeout = 250 * time.Millisecond
)

// ensureMu serializes concurrent ensureRunning calls within a single
// process. Cross-process concurrency is guarded by Docker's
// container-name uniqueness — the "already in use" recovery path below
// catches that race and reconciles to the existing container.
var ensureMu sync.Mutex

// Test seams for the side-effecting steps of ensureRunning. Tests
// overwrite these to stub crypto (auth ensure), Docker image builds,
// and /healthz polling.
//
// `ensureAuthFn` is the load-bearing pre-step: bind mounts in
// BuildCPContainerConfig point at on-disk PEM files. `auth.EnsureAuthMaterial`
// is idempotent — safe to call on every ensureRunning invocation. Without
// it, ContainerCreate fails with a missing bind source.
//
//nolint:gochecknoglobals // test seams, overwritten and restored by bootstrap_test.go's fixture
var (
	ensureAuthFn    = auth.EnsureAuthMaterial
	ensureCPImageFn = ensureCPImage
	healthzFn       = newHealthzProbe
	clockSyncFn     = waitForCPClockSync
	probeCPTimeFn   = adminclient.ProbeCPTime
	dialSOSFn       = dialSOSWatch
)

// errCPRecoveryRetry is returned by recoverFromNameConflict when it
// has force-removed a stale peer-bootstrapped CP container and the
// caller should re-attempt ContainerCreate. Internal sentinel — never
// surfaces to operators.
var errCPRecoveryRetry = errors.New("cp container create should be retried after recovery")

// cpBinaryHash returns the SHA-256 hash of the embedded clawkercp +
// ebpf-manager binaries. The full hex form is stamped onto the image
// and container as consts.LabelCPBinarySHA; the short prefix is folded
// into the image tag for human-readable `docker images` output.
func cpBinaryHash() (full, short string) {
	h := sha256.New()
	h.Write(ClawkerCPBinary)
	h.Write(EBPFManagerBinary)
	sum := h.Sum(nil)
	full = hex.EncodeToString(sum)
	short = full[:16]
	return
}

// cpImageRef returns the content-derived image tag for the CP image
// (clawker-controlplane:bin-<short>). The tag changes whenever either
// embedded binary changes, so ImageInspect becomes an exact-content
// cache check.
func cpImageRef() string {
	_, short := cpBinaryHash()
	return fmt.Sprintf("%s:bin-%s", consts.CPImageRepo, short)
}

// cpImageDockerfile is the multi-stage build recipe for the clawkercp
// image. All base images are pinned by multi-arch manifest digest.
// clawkercp and ebpf-manager binaries are supplied from embedded bytes
// (see ClawkerCPBinary / EBPFManagerBinary) in the build tar context.
// Per-build labels are interpolated so the resulting image carries its
// content identity and OCI provenance metadata.
func cpImageDockerfile(binarySHA, version, revision, createdAt string) string {
	// LABEL syntax needs `\` and `"` escapes; %q is wrong because Docker
	// does not parse Go-style escape sequences.
	dockerLabel := func(key, value string) string {
		v := strings.ReplaceAll(value, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		return fmt.Sprintf("LABEL %s=\"%s\"\n", key, v)
	}
	labels := "" +
		dockerLabel(consts.LabelCPBinarySHA, binarySHA) +
		dockerLabel(consts.LabelImageVersion, version) +
		dockerLabel(consts.LabelImageCreated, createdAt) +
		dockerLabel(consts.LabelImageSource, "https://github.com/schmitthub/clawker")
	// Omit revision LABEL when ldflags + vcs.revision both fall back to
	// the "unknown" sentinel (typical for `go run`); OCI convention is
	// to skip provenance fields with no real value.
	if revision != "" && revision != "unknown" {
		labels += dockerLabel(consts.LabelImageRevision, revision)
	}
	return "" +
		"FROM oryd/hydra:v26.2.0@sha256:ff67c7fb5f95074fa53374d41151713554960504b340cd3f95b09e65deaea2a9 AS hydra\n" +
		"FROM oryd/oathkeeper:v26.2.0@sha256:467329abde34feefca217b7af76fff59e77fe1795a19376e9d479f33c7c198fc AS oathkeeper\n" +
		"FROM oryd/kratos:v26.2.0@sha256:2a13bb8d362c7a7ae33bd7c0f5168aee46921f15c916a06346db91c06dc76643 AS kratos\n" +
		"FROM alpine:3.21@sha256:a8560b36e8b8210634f77d9f7f9efd7ffa463e380b75e2e74aff4511df3ef88c AS musl\n" +
		"FROM gcr.io/distroless/static-debian12@sha256:20bc6c0bc4d625a22a8fde3e55f6515709b32055ef8fb9cfbddaa06d1760f838\n" +
		// Pin the CP container to UTC. clawkercp (PID 1) and the Ory
		// subprocesses it spawns (Hydra, Kratos, Oathkeeper) all inherit this
		// env, so Hydra validates JWT iat in the same UTC domain the CLI mints
		// in and our GetSystemTime reports in. JWT NumericDate is absolute
		// epoch (RFC 7519), so this is defense-in-depth — it removes any
		// dependence on the base image's default localtime.
		"ENV TZ=UTC\n" +
		"COPY --from=musl /lib/ld-musl-*.so.1 /lib/\n" +
		"COPY --from=hydra /usr/bin/hydra /usr/local/bin/hydra\n" +
		"COPY --from=oathkeeper /usr/bin/oathkeeper /usr/local/bin/oathkeeper\n" +
		"COPY --from=kratos /usr/bin/kratos /usr/local/bin/kratos\n" +
		"COPY clawkercp /usr/local/bin/clawkercp\n" +
		"COPY ebpf-manager /usr/local/bin/ebpf-manager\n" +
		labels +
		"CMD [\"/usr/local/bin/clawkercp\"]\n"
}

// EnsureOpts bundles the inputs ensureRunning needs. HostDirs is required;
// callers resolve it host-side from consts.{ConfigDir,DataDir,StateDir,
// CacheDir} before invoking. The CP container reads the host paths back
// from the CLAWKER_HOST_*_DIR env vars injected by BuildCPContainerConfig
// so it can compute sibling container bind mount sources via
// Docker-outside-of-Docker.
type EnsureOpts struct {
	Docker   *docker.Client
	Config   config.Config
	Logger   *logger.Logger
	HostDirs HostDirs
}

// ensureRunning brings the control plane up on this host. Idempotent and
// concurrency-safe. It builds the image, creates/starts
// the container, then runs the readiness gate (see cpReady): it returns nil
// only when the CP container is running, /healthz is green, AND the CP clock
// has caught up to the host. A green /healthz with the CP clock still behind
// the host returns a clock-sync error, not nil — so a container start blocks
// until the CP clock has reconverged with the host before clawkerd exchanges
// its (host-clock-minted) agent assertion. The clock-sync step's value is the
// wait: it returns only error, no offset; assertions are minted in
// the host clock with no correction.
//
// Drift gate: an existing CP container whose consts.LabelCPBinarySHA matches
// the host clawker binary's embedded clawkercp + ebpf-manager hash is adopted
// (started if stopped); any mismatch (including legacy containers that predate
// the label) is force-removed and recreated so the new mount/env spec reaches
// the running CP. Mount spec itself is not inspected — mounts derive from
// compile-time constants only, so any mount/env/cmd change implies a host
// rebuild, which changes the embedded bytes, which changes the SHA.
//
// On partial failure (container created but /healthz or the clock-sync gate
// timed out) the next call observes the running/unhealthy container and re-runs
// the readiness gate (clock sync self-heals once the VM clock re-syncs).
func ensureRunning(ctx context.Context, opts EnsureOpts) error {
	dc, cfg := opts.Docker, opts.Config
	// File logging is diagnostics: a caller that could not build a logger
	// still gets its control plane, it just gets no log lines about it.
	log := opts.Logger
	if log == nil {
		log = logger.Nop()
	}

	if err := opts.HostDirs.Validate(); err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}
	// Checked before any image or container work: the CP cannot exist
	// without bind-mounting the daemon socket, so an unmountable address
	// must fail here in milliseconds, not after an image build.
	if hostErr := validateMountableHost(cfg.DockerHost()); hostErr != nil {
		return fmt.Errorf("controlplane: %w", hostErr)
	}

	ensureMu.Lock()
	defer ensureMu.Unlock()

	if err := ensureAuthFn(); err != nil {
		return fmt.Errorf("ensure auth material: %w", err)
	}

	imageRef, err := ensureCPImageFn(ctx, dc, log)
	if err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}

	adopted, err := reconcileExistingCP(ctx, dc, log)
	if err != nil {
		return err
	}
	if adopted {
		return awaitCPReady(ctx, dc, cfg, log)
	}

	networkID, cpIP, err := placeCPOnNetwork(ctx, dc, cfg)
	if err != nil {
		return err
	}

	if createErr := createCPContainer(ctx, dc, cfg, networkID, cpIP, opts.HostDirs, imageRef, log); createErr != nil {
		return fmt.Errorf("controlplane: %w", createErr)
	}

	return awaitCPReady(ctx, dc, cfg, log)
}

// placeCPOnNetwork settles where a new CP container will live: it ensures the
// clawker network exists, reads back its topology, and derives the CP's static
// address from the gateway plus the configured last octet. The subnet check is
// the reason this is computed rather than configured — an address outside the
// network would fail at container create with a Docker error that names
// neither the setting nor the subnet.
func placeCPOnNetwork(
	ctx context.Context, dc *docker.Client, cfg config.Config,
) (string, netip.Addr, error) {
	//nolint:exhaustruct // Name is the only required field; the embedded moby NetworkCreateOptions is optional and omitted at every EnsureNetwork call site.
	if _, netErr := dc.EnsureNetwork(ctx, whail.EnsureNetworkOptions{Name: cfg.ClawkerNetwork()}); netErr != nil {
		return "", netip.Addr{}, fmt.Errorf("controlplane: ensure clawker-net: %w", netErr)
	}

	netInfo, err := fwcp.DiscoverNetwork(ctx, dc, cfg)
	if err != nil {
		return "", netip.Addr{}, fmt.Errorf("controlplane: discover clawker-net: %w", err)
	}
	cpIP, err := fwcp.ComputeStaticIP(netInfo.Gateway, cfg.CPIPLastOctet())
	if err != nil {
		return "", netip.Addr{}, fmt.Errorf("controlplane: compute cp static ip: %w", err)
	}
	if netInfo.Subnet.IsValid() && !netInfo.Subnet.Contains(cpIP) {
		return "", netip.Addr{}, fmt.Errorf(
			"controlplane: cp static IP %s is outside network subnet %s (check CPIPLastOctet setting)",
			cpIP,
			netInfo.Subnet,
		)
	}
	return netInfo.NetworkID, cpIP, nil
}

// refuseUpgradeWhileActive blocks replacing a drifted CP container while
// anything still depends on it. Returns the operator-facing error when a CP or
// any agent container is running, nil when the replacement is safe to do.
//
// Removing the supervisor out from under live agents is a worse outcome than
// asking the operator to drain first: those agents keep running filtered by a
// rule set nothing is left to update.
func refuseUpgradeWhileActive(
	ctx context.Context, dc *docker.Client, log *logger.Logger, cpRunning bool,
) error {
	//nolint:exhaustruct // a label+status filter is the whole query; the remaining fields are paging and time windows
	activeAgents, err := dc.ContainerList(ctx, client.ContainerListOptions{
		Filters: client.Filters{}.
			Add("label", consts.LabelPurpose+"="+consts.PurposeAgent).
			Add("status", "running"),
	})
	if err != nil {
		return fmt.Errorf("controlplane: list active agents: %w", err)
	}
	if !cpRunning && len(activeAgents.Items) == 0 {
		return nil
	}

	log.Error().
		Str("event", "cp_container_upgrade_blocked").
		Str("component", "manager.bootstrap").
		Str("container", consts.ContainerCP).
		Bool("cp_running", cpRunning).
		Int("active_agent_count", len(activeAgents.Items)).
		Msg("control plane upgrade blocked — active CP or agent containers present")
	return fmt.Errorf(
		"clawker was upgraded and the control plane needs to be replaced, but %d agent container(s) are still running and the existing control plane is %s.\n\nTo upgrade safely:\n  1. Stop all agents:        clawker container ls\n                             clawker container stop <name>\n  2. Shut down CP (one of):  wait — CP self-shuts-down once agents reach zero\n                             clawker controlplane down  (skip the wait)\n  3. Restart agents:         clawker run <name>\n\nIf agents fail to restart cleanly after upgrade, their embedded clawkerd may need rebuilding against the new CLI:\n  clawker build\n  clawker run <name>",
		len(activeAgents.Items),
		map[bool]string{true: "still running", false: "stopped"}[cpRunning],
	)
}

// reconcileExistingCP decides what happens to a CP container that is already
// there. It reports adopted=true when that container is this clawker's own —
// its consts.LabelCPBinarySHA matches the embedded binaries AND its
// consts.LabelCPBPFFSSource matches the currently desired BPF filesystem
// source — and is now running; all the caller has left to do is wait for
// readiness. It reports adopted=false when there was nothing there, or once a
// drifted container has been removed, leaving the caller to create a fresh
// one.
//
// The binary-drift case is a host clawker that was rebuilt, or a container
// predating the label. The bpffs-source drift case is the rootless heal: a CP
// created before the delegated filesystem existed must be recreated onto it —
// restarting it would replay the same permission failure forever. Replacing
// is refused while a CP or any agent is still running: removing the
// supervisor out from under live agents is a worse outcome than telling the
// operator to drain first.
func reconcileExistingCP(ctx context.Context, dc *docker.Client, log *logger.Logger) (bool, error) {
	summary, err := findCPContainer(ctx, dc)
	if err != nil {
		return false, fmt.Errorf("controlplane: find cp: %w", err)
	}
	if summary == nil {
		return false, nil
	}

	desired, _ := cpBinaryHash()
	actual := summary.Labels[consts.LabelCPBinarySHA]
	desiredBPFFS, err := resolveBPFFSSource()
	if err != nil {
		return false, fmt.Errorf("controlplane: %w", err)
	}
	actualBPFFS := summary.Labels[consts.LabelCPBPFFSSource]
	if actual == desired && actualBPFFS == desiredBPFFS {
		if summary.State != container.StateRunning {
			//nolint:exhaustruct // ContainerID is the only field a plain start needs; the rest are checkpoint/network options
			if _, startErr := dc.ContainerStart(
				ctx, whail.ContainerStartOptions{ContainerID: summary.ID},
			); startErr != nil {
				return false, fmt.Errorf("controlplane: start existing cp: %w", startErr)
			}
		}
		return true, nil
	}

	cpRunning := summary.State == container.StateRunning
	if blockedErr := refuseUpgradeWhileActive(ctx, dc, log, cpRunning); blockedErr != nil {
		return false, blockedErr
	}

	// Force-remove regardless of State — works on stopped post-drain
	// containers and on still-running stale ones alike.
	log.Info().
		Str("event", "cp_container_spec_drift").
		Str("component", "manager.bootstrap").
		Str("container", consts.ContainerCP).
		Str("state", string(summary.State)).
		Str("desired_binary_sha256", desired).
		Str("running_binary_sha256", actual).
		Str("desired_bpffs_source", desiredBPFFS).
		Str("running_bpffs_source", actualBPFFS).
		Msg("recreating CP container — embedded binary or spec changed")
	if removeErr := stopAndRemoveCP(ctx, dc, summary.ID); removeErr != nil {
		log.Error().
			Str("event", "cp_container_force_remove_failed").
			Str("component", "manager.bootstrap").
			Str("container", consts.ContainerCP).
			Err(removeErr).
			Msg("drift detected but force-remove failed; next bringup will retry")
		return false, fmt.Errorf("controlplane: %w", removeErr)
	}
	return false, nil
}

// Stop removes the CP container. Used by `clawker controlplane down`.
// Docker sends SIGTERM to PID 1 (clawkercp), whose own shutdown path
// drains the firewall stack (Envoy + CoreDNS) and flushes per-container
// eBPF state before exiting — this call does not need to tear those down
// separately.
func Stop(ctx context.Context, dc *docker.Client) error {
	summary, err := findCPContainer(ctx, dc)
	if err != nil {
		return fmt.Errorf("controlplane stop: find cp: %w", err)
	}
	if summary == nil {
		return nil
	}
	return stopAndRemoveCP(ctx, dc, summary.ID)
}

// findCPContainer returns the managed CP container summary or nil if none
// exists. Using ContainerList (managed filter auto-injected by whail)
// avoids the inspect-managed ambiguity whose surface errors differ.
func findCPContainer(ctx context.Context, dc *docker.Client) (*container.Summary, error) {
	filters := whail.Filters{}.Add("name", consts.ContainerCP)
	result, err := dc.ContainerList(ctx, whail.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", consts.ContainerCP, err)
	}
	for i, c := range result.Items {
		for _, name := range c.Names {
			if name == "/"+consts.ContainerCP || name == consts.ContainerCP {
				return &result.Items[i], nil
			}
		}
	}
	return nil, nil
}

// CPRunning reports whether the CP container exists AND is in the running
// state. Used by CLI commands (`firewall status`, `firewall down`) that
// observe or tear down the CP without wanting to trigger the bringup's
// creation path as a side effect. Returns (false, nil) when absent; errors
// only on Docker API failures.
func CPRunning(ctx context.Context, dc *docker.Client) (bool, error) {
	summary, err := findCPContainer(ctx, dc)
	if err != nil {
		return false, err
	}
	if summary == nil {
		return false, nil
	}
	return summary.State == container.StateRunning, nil
}

// stopAndRemoveCP stops then force-removes the CP container. A missing
// container is not an error — concurrent callers may have already cleaned
// it up, and that is the end state we want anyway.
func stopAndRemoveCP(ctx context.Context, dc *docker.Client, id string) error {
	timeout := cpStopTimeout
	if _, err := dc.ContainerStop(ctx, id, &timeout); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("stopping cp container %s: %w", id, err)
	}
	if _, err := dc.ContainerRemove(ctx, id, true); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("removing cp container %s: %w", id, err)
	}
	return nil
}

// createCPContainer composes the full create options from
// BuildCPContainerConfig + bootstrap-computed network topology and
// dispatches ContainerCreate + ContainerStart. Handles the "already in
// use" race from concurrent bootstraps via recoverFromNameConflict; if
// recovery force-removes a peer's stale container it signals retry via
// errCPRecoveryRetry, which loops back to a fresh ContainerCreate.
// Bounded at maxCreateAttempts so a pathological repeat-conflict cannot
// spin.
func createCPContainer(ctx context.Context, dc *docker.Client, cfg config.Config, networkID string, ip netip.Addr, hostDirs HostDirs, imageRef string, log *logger.Logger) error {
	cpCfg, err := BuildCPContainerConfig(cfg, CPContainerOpts{HostDirs: hostDirs, Image: imageRef})
	if err != nil {
		return fmt.Errorf("build cp container config: %w", err)
	}

	containerCfg := &container.Config{
		Image:  cpCfg.Image,
		Labels: cpCfg.Labels,
		Env:    cpCfg.Env,
		Cmd:    cpCfg.Cmd,
	}
	hostCfg := &container.HostConfig{
		Mounts:        cpCfg.Mounts,
		PortBindings:  cpCfg.PortBindings,
		CapAdd:        cpCfg.CapAdd,
		SecurityOpt:   cpCfg.SecurityOpt,
		RestartPolicy: cpCfg.RestartPolicy,
		ExtraHosts:    cpCfg.ExtraHosts,
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			cpCfg.NetworkName: {
				NetworkID:  networkID,
				IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: ip},
			},
		},
	}

	const maxCreateAttempts = 2
	var lastErr error
	for attempt := 1; attempt <= maxCreateAttempts; attempt++ {
		createResp, createErr := dc.ContainerCreate(ctx, whail.ContainerCreateOptions{
			Name:             consts.ContainerCP,
			Config:           containerCfg,
			HostConfig:       hostCfg,
			NetworkingConfig: netCfg,
		})
		if createErr == nil {
			if _, err := dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: createResp.ID}); err != nil {
				return fmt.Errorf("starting cp container: %w", err)
			}
			return nil
		}
		lastErr = createErr
		recErr := recoverFromNameConflict(ctx, dc, createErr, imageRef, log)
		if errors.Is(recErr, errCPRecoveryRetry) {
			// Re-resolve via ensureCPImageFn so a concurrent prune that
			// removed our image (cp_recovery_our_image_vanished branch in
			// recoverFromNameConflict) is rebuilt before the next
			// ContainerCreate. Cheap on the happy path — content-derived
			// tag short-circuits on ImageInspect cache hit.
			newRef, ensureErr := ensureCPImageFn(ctx, dc, log)
			if ensureErr != nil {
				log.Error().
					Str("event", "cp_recovery_reensure_image_failed").
					Str("component", "manager.bootstrap").
					Err(ensureErr).
					Msg("re-ensuring cp image before retry failed")
				return fmt.Errorf("re-ensuring cp image before retry: %w", ensureErr)
			}
			imageRef = newRef
			containerCfg.Image = newRef
			continue
		}
		return recErr
	}
	return fmt.Errorf("creating cp container: exceeded %d attempts; last error: %w", maxCreateAttempts, lastErr)
}

// recoverFromNameConflict handles the cross-process race where another
// bootstrapper created the CP container between findCPContainer and
// ContainerCreate. Resolution: ContainerInspect the peer for an
// authoritative SHA read; on match adopt; on mismatch compare
// LabelImageCreated (with Docker image Created fallback) and let the
// newer build win. Equal timestamps tie-break to adopt-peer (favors
// stability under second-precision collisions). NotConflict errors and
// unmanaged-name squats surface unchanged.
func recoverFromNameConflict(ctx context.Context, dc *docker.Client, createErr error, imageRef string, log *logger.Logger) error {
	if !cerrdefs.IsConflict(createErr) {
		return fmt.Errorf("creating cp container: %w", createErr)
	}
	recovered, recErr := findCPContainer(ctx, dc)
	if recErr != nil {
		return fmt.Errorf("cp container name conflict (%v) and lookup failed: %w", createErr, recErr)
	}
	if recovered == nil {
		log.Error().
			Str("event", "cp_recovery_unmanaged_name_squat").
			Str("component", "manager.bootstrap").
			Str("container", consts.ContainerCP).
			Msg("cp container name held by an unmanaged container")
		return fmt.Errorf("cp container name %q in use by an unmanaged container: %w", consts.ContainerCP, createErr)
	}

	inspect, err := dc.ContainerInspect(ctx, recovered.ID, whail.ContainerInspectOptions{})
	if err != nil {
		log.Error().
			Str("event", "cp_recovery_inspect_failed").
			Str("component", "manager.bootstrap").
			Str("container_id", recovered.ID).
			Err(err).
			Msg("recovered cp container inspect failed")
		return fmt.Errorf("inspecting recovered cp container: %w", err)
	}
	resp := inspect.Container

	var actualSHA string
	if resp.Config != nil {
		actualSHA = resp.Config.Labels[consts.LabelCPBinarySHA]
	}
	desiredSHA, _ := cpBinaryHash()

	if actualSHA == desiredSHA {
		state := ""
		if resp.State != nil {
			state = string(resp.State.Status)
		}
		log.Info().
			Str("event", "cp_recovery_adopt_sha_match").
			Str("component", "manager.bootstrap").
			Str("container_id", resp.ID).
			Str("state", state).
			Str("binary_sha256", actualSHA).
			Msg("adopting concurrent peer cp container — binary SHA matches")
		return adoptRecoveredCP(ctx, dc, resp)
	}

	oursCreated, err := cpImageCreatedAt(ctx, dc, imageRef, log)
	if err != nil {
		// Our image vanished between build and recovery (concurrent
		// `docker image rm`, prune, or storage GC). Treat as recoverable:
		// createCPContainer's retry loop re-runs ensureCPImageFn on this
		// sentinel so the next ContainerCreate has something to reference.
		if cerrdefs.IsNotFound(err) {
			log.Warn().
				Str("event", "cp_recovery_our_image_vanished").
				Str("component", "manager.bootstrap").
				Str("image", imageRef).
				Err(err).
				Msg("our cp image vanished mid-recovery; retrying")
			return errCPRecoveryRetry
		}
		log.Error().
			Str("event", "cp_recovery_inspect_failed").
			Str("component", "manager.bootstrap").
			Str("image", imageRef).
			Err(err).
			Msg("our cp image inspect failed during recovery")
		return fmt.Errorf("inspecting our cp image %s: %w", imageRef, err)
	}
	theirsCreated, err := cpImageCreatedAt(ctx, dc, resp.Image, log)
	if err != nil {
		log.Error().
			Str("event", "cp_recovery_inspect_failed").
			Str("component", "manager.bootstrap").
			Str("image", resp.Image).
			Err(err).
			Msg("recovered cp image inspect failed during recovery")
		return fmt.Errorf("inspecting recovered cp image %s: %w", resp.Image, err)
	}

	logEvent := log.Info().
		Str("component", "manager.bootstrap").
		Str("our_binary_sha256", desiredSHA).
		Str("their_binary_sha256", actualSHA).
		Time("our_image_created", oursCreated).
		Time("their_image_created", theirsCreated)

	// Equal timestamps fall here too — adopt peer to avoid churn under
	// second-precision clock collisions.
	if !oursCreated.After(theirsCreated) {
		logEvent.
			Str("event", "cp_recovery_adopt_newer_peer").
			Msg("adopting concurrent peer cp container — peer image is at least as new")
		return adoptRecoveredCP(ctx, dc, resp)
	}

	logEvent.
		Str("event", "cp_recovery_replace_older_peer").
		Msg("replacing concurrent peer cp container — our image is newer")
	if err := stopAndRemoveCP(ctx, dc, resp.ID); err != nil {
		return fmt.Errorf("removing older cp container: %w", err)
	}
	return errCPRecoveryRetry
}

// adoptRecoveredCP starts the recovered container if it isn't already
// running. Shared between the SHA-match and theirs-newer branches of
// recoverFromNameConflict.
func adoptRecoveredCP(ctx context.Context, dc *docker.Client, resp container.InspectResponse) error {
	if resp.State != nil && resp.State.Running {
		return nil
	}
	if _, err := dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID}); err != nil {
		return fmt.Errorf("starting recovered cp container: %w", err)
	}
	return nil
}

// cpImageCreatedAt returns the build-time creation timestamp for a CP
// image. Prefers the consts.LabelImageCreated LABEL we stamp in
// cpImageDockerfile (RFC3339, second precision), falling back to the
// Docker image's own Created field (RFC3339Nano, set by the daemon at
// build completion). A non-empty LABEL that fails to parse emits a
// structured warn before fallback so tampering / corruption is
// observable in the file log.
func cpImageCreatedAt(ctx context.Context, dc *docker.Client, ref string, log *logger.Logger) (time.Time, error) {
	inspect, err := dc.ImageInspect(ctx, ref)
	if err != nil {
		return time.Time{}, err
	}
	if inspect.Config != nil {
		if raw := inspect.Config.Labels[consts.LabelImageCreated]; raw != "" {
			if t, parseErr := time.Parse(time.RFC3339, raw); parseErr == nil {
				return t, nil
			} else if log != nil {
				log.Warn().
					Str("event", "cp_image_created_label_unparseable").
					Str("component", "manager.bootstrap").
					Str("image", ref).
					Str("raw", raw).
					Err(parseErr).
					Msg("cp image org.opencontainers.image.created LABEL is non-empty but unparseable; falling back to Docker Created field")
			}
		}
	}
	if inspect.Created != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, inspect.Created); parseErr == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("image %s has no parseable created timestamp", ref)
}

// ensureCPImage checks for the clawkercp image and builds it from
// embedded binaries + pinned base images when absent. Mirrors the
// Stack.ensureCorednsImage pattern so both CP and CoreDNS images have
// the same error-surfacing behavior.
func ensureCPImage(ctx context.Context, dc *docker.Client, log *logger.Logger) (string, error) {
	tag := cpImageRef()
	if _, err := dc.ImageInspect(ctx, tag); err == nil {
		return tag, nil
	} else if !cerrdefs.IsNotFound(err) {
		return "", fmt.Errorf("checking %s image: %w", tag, err)
	}

	if len(ClawkerCPBinary) == 0 {
		return "", fmt.Errorf("clawkercp binary not embedded — run 'make cp-binary' then rebuild clawker")
	}
	if len(EBPFManagerBinary) == 0 {
		return "", fmt.Errorf("ebpf-manager binary not embedded — run 'make ebpf-binary' then rebuild clawker")
	}

	full, _ := cpBinaryHash()
	createdAt := time.Now().UTC().Format(time.RFC3339)

	buildCtx, err := cpBuildContext(full, build.Version, build.Revision, createdAt)
	if err != nil {
		return "", fmt.Errorf("creating cp build context: %w", err)
	}

	log.Debug().Str("image", tag).Str("binary_sha256", full).Msg("building cp image from embedded binaries")
	resp, err := dc.ImageBuild(ctx, buildCtx, whail.ImageBuildOptions{
		Tags:           []string{tag},
		Dockerfile:     "Dockerfile",
		Remove:         true,
		ForceRemove:    true,
		SuppressOutput: true,
	})
	if err != nil {
		return "", fmt.Errorf("building cp image: %w", err)
	}
	defer resp.Body.Close()
	if err := drainBuildStream(resp.Body, fmt.Sprintf("building cp image %s", tag)); err != nil {
		return "", err
	}
	pruneStaleCPImages(ctx, dc, tag, log)
	return tag, nil
}

// pruneStaleCPImages best-effort removes locally-cached CP image tags
// that don't match the just-built keepTag, so a rebuild cycle doesn't
// accumulate one bin-<sha> image per change. Matches the bare
// `clawker-controlplane:` prefix so legacy `:latest` images from
// pre-content-derived-tag installs are also swept. Failures degrade
// (warn + continue) — a stale image leftover is not a boot blocker.
func pruneStaleCPImages(ctx context.Context, dc *docker.Client, keepTag string, log *logger.Logger) {
	images, err := dc.ImageList(ctx, whail.ImageListOptions{All: false})
	if err != nil {
		log.Warn().
			Str("event", "cp_image_prune_unavailable").
			Str("component", "manager.bootstrap").
			Err(err).
			Msg("cp image prune: list failed")
		return
	}
	prefix := consts.CPImageRepo + ":"
	for _, img := range images.Items {
		for _, tag := range img.RepoTags {
			if tag == keepTag || !strings.HasPrefix(tag, prefix) {
				continue
			}
			if _, err := dc.ImageRemove(ctx, tag, whail.ImageRemoveOptions{Force: true, PruneChildren: true}); err != nil {
				log.Warn().
					Str("event", "cp_image_prune_unavailable").
					Str("component", "manager.bootstrap").
					Str("image", tag).
					Err(err).
					Msg("cp image prune: remove failed")
			} else {
				log.Debug().Str("image", tag).Msg("cp image prune: removed stale tag")
			}
		}
	}
}

// drainBuildStream consumes the Docker build daemon's JSON progress
// stream, surfacing inline build failures that do not come back as a
// top-level ImageBuild error. Both `error` and `errorDetail.message`
// are checked; BuildKit emits the detailed form. A clean io.EOF is
// success (daemon closed the stream after the final frame). Any other
// decode error is surfaced so we do not treat a truncated stream as a
// successful build.
func drainBuildStream(r io.Reader, ctxMsg string) error {
	dec := json.NewDecoder(r)
	for {
		var msg struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("%s: decoding daemon stream: %w", ctxMsg, err)
		}
		if detail := msg.ErrorDetail.Message; detail != "" {
			return fmt.Errorf("%s: %s", ctxMsg, detail)
		}
		if msg.Error != "" {
			return fmt.Errorf("%s: %s", ctxMsg, msg.Error)
		}
	}
}

// cpBuildContext assembles the three-file tar archive (Dockerfile +
// clawkercp + ebpf-manager) that ImageBuild expects.
func cpBuildContext(binarySHA, version, revision, createdAt string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	writeFile := func(name string, contents []byte, mode int64) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(contents)), Mode: mode}); err != nil {
			return fmt.Errorf("tar header for %s: %w", name, err)
		}
		if _, err := tw.Write(contents); err != nil {
			return fmt.Errorf("tar write for %s: %w", name, err)
		}
		return nil
	}
	if err := writeFile("Dockerfile", []byte(cpImageDockerfile(binarySHA, version, revision, createdAt)), 0o644); err != nil {
		return nil, err
	}
	if err := writeFile("clawkercp", ClawkerCPBinary, 0o755); err != nil {
		return nil, err
	}
	if err := writeFile("ebpf-manager", EBPFManagerBinary, 0o755); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	return &buf, nil
}

// CPSOSError reports that the CP sent an SOS mid-boot on the WatchSOS
// stream: a recoverable startup failure it cannot fix alone and is alive
// waiting for the CLI's assistance on. Kind is what the assistance
// dispatches on and Message carries the CP's own description of what is
// needed — the error text for a kind no assistance handles. Typed so
// callers can discriminate it from readiness timeouts and container
// exits without string matching.
type CPSOSError struct {
	Kind    adminv1.SOSKind
	Message string
}

func (e *CPSOSError) Error() string {
	return "control plane needs assistance: " + e.Message
}

// awaitCPReady is the readiness gate: one sequential loop, run entirely
// inside the command's execution — no goroutines. Each pass takes one
// /healthz sample; while the CP is not ready it also takes one paced,
// bounded look at the CP's WatchSOS recovery queue — the CLI's window
// into a boot waiting for assistance. An SOS surfaces immediately as
// *CPSOSError, which the caller renders or acts on — assisting the CP
// means prompting a human and running something privileged, and neither
// belongs in the package that merely interfaces with it. Checks that
// find nothing leave the outcome to the readiness probe alone: ready
// breaks to the clock-sync gate, a terminal probe error (typed timeout,
// dead container) returns as-is.
//
// The SOS side holds ONE admin connection for the whole wait — one
// minted token, cached on the connection — dialed lazily on the first
// not-ready pass so an already-healthy CP costs nothing. Each check is
// a short-lived stream on that connection; the CP replays a pending SOS
// to any subscriber, so short-lived checks cannot miss one. A failed
// dial disables the checks for this wait and the readiness gate decides
// alone.
//
// Clock sync runs after /healthz is green and is a first-class
// readiness condition, not an afterthought: a start that proceeded
// while the CP clock still lagged the host would let clawkerd exchange
// an assertion whose (host-clock) iat is in the CP's future.
func awaitCPReady(ctx context.Context, dc *docker.Client, cfg config.Config, log *logger.Logger) error {
	probe := healthzFn(ctx, dc, cfg)
	watch := newLazySOSWatch(cfg, log)
	defer watch.close()

	for {
		ready, err := probe(ctx)
		if err != nil {
			return err
		}
		if ready {
			break
		}
		if sos := watch.check(ctx); sos != nil {
			return &CPSOSError{Kind: sos.GetKind(), Message: sos.GetMessage()}
		}
		if sleepErr := sleepReadyInterval(ctx); sleepErr != nil {
			return sleepErr
		}
	}
	return clockSyncFn(ctx, cfg, log)
}

// sleepReadyInterval paces the readiness loop. Cancellation surfaces as
// an error; a deadline expiry returns nil so one more probe step
// surfaces the typed timeout error with its diagnostics instead of a
// bare ctx error.
func sleepReadyInterval(ctx context.Context) error {
	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			return fmt.Errorf("await cp ready: %w", ctx.Err())
		}
	case <-time.After(cpReadyInterval):
	}
	return nil
}

// newLazySOSWatch wraps dialSOSFn so the readiness loop dials only on
// the first check — an already-healthy CP never has a not-ready pass
// and costs nothing, not even a token. A failed dial sticks: checking
// stays disabled for the rest of the wait (every later check is a
// no-op) and the readiness gate decides alone.
func newLazySOSWatch(cfg config.Config, log *logger.Logger) *sosWatch {
	var watch *sosWatch
	dialed := false
	return &sosWatch{
		check: func(ctx context.Context) *adminv1.SOS {
			if !dialed {
				dialed = true
				watch = dialSOSFn(ctx, cfg, log)
			}
			if watch == nil {
				return nil
			}
			return watch.check(ctx)
		},
		close: func() {
			if watch != nil {
				watch.close()
			}
		},
	}
}

// sosWatch is the boot-SOS check the readiness loop consults between
// healthz samples. check takes one paced, bounded look at the CP's
// recovery queue (nil = nothing to report yet); close releases the held
// admin connection. Both fields are non-nil on every value dialSOSWatch
// returns — a nil *sosWatch means the dial failed and checking is
// disabled for this wait.
type sosWatch struct {
	check func(ctx context.Context) *adminv1.SOS
	close func()
}

// dialSOSWatch establishes the one admin connection the readiness
// loop's SOS checks share. The connection is dialed ONCE and held for
// the whole wait: gRPC reconnects a broken transport by itself, and the
// connection's token source caches its bearer token after the first
// successful mint — so the whole wait costs one token, not one per
// check. Re-dialing per attempt was a real bug: each dial minted a
// fresh Hydra token, and that 2Hz mint burst destroyed Hydra's
// in-memory SQLite database.
//
// The dial itself tolerates a CP still bringing up its token endpoint
// (adminclient.Dial retries the initial mint on a bounded window); a
// dial that still fails disables SOS checking for this wait — the
// readiness gate decides alone.
func dialSOSWatch(ctx context.Context, cfg config.Config, log *logger.Logger) *sosWatch {
	cp := cfg.ControlPlaneSettings()
	adminClient, conn, err := adminclient.Dial(ctx, cp.AdminPort, cp.HydraPublicPort)
	if err != nil {
		log.Debug().
			Err(err).
			Str("component", "manager.bootstrap").
			Msg("sos check: dial failed; boot SOS checking disabled")
		return nil
	}
	var lastAttempt time.Time
	return &sosWatch{
		check: func(ctx context.Context) *adminv1.SOS {
			if time.Since(lastAttempt) < sosRetryInterval {
				return nil
			}
			lastAttempt = time.Now()
			return checkSOSOnce(ctx, adminClient, log)
		},
		close: func() {
			if cerr := conn.Close(); cerr != nil {
				log.Debug().
					Err(cerr).
					Str("component", "manager.bootstrap").
					Msg("sos check: closing connection")
			}
		},
	}
}

// checkSOSOnce opens one WatchSOS stream bounded by sosCheckTimeout and
// reports what the CP has to say right now. The CP delivers a pending
// SOS — current or earlier-published — immediately on subscribe, so the
// short window is enough to hear one; nil covers everything else (clean
// end-of-stream, timeout with nothing pending, stream open or transport
// failure) — the readiness gate owns those outcomes.
func checkSOSOnce(
	ctx context.Context,
	adminClient adminv1.AdminServiceClient,
	log *logger.Logger,
) *adminv1.SOS {
	checkCtx, cancel := context.WithTimeout(ctx, sosCheckTimeout)
	defer cancel()
	stream, err := adminClient.WatchSOS(checkCtx, &adminv1.WatchSOSRequest{})
	if err != nil {
		log.Debug().
			Err(err).
			Str("component", "manager.bootstrap").
			Msg("sos check: stream open failed")
		return nil
	}
	sos, err := stream.Recv()
	if err != nil {
		if !errors.Is(err, io.EOF) && checkCtx.Err() == nil {
			log.Debug().
				Err(err).
				Str("component", "manager.bootstrap").
				Msg("sos check: stream ended")
		}
		return nil
	}
	log.Info().
		Str("event", "cp_sos_received").
		Str("component", "manager.bootstrap").
		Str("kind", sos.GetKind().String()).
		Str("message", sos.GetMessage()).
		Msg("control plane sent an SOS during boot")
	return sos
}

// waitForCPClockSync polls the public GetSystemTime RPC until the CP clock
// is no longer behind the host (the CP wall-clock at or after the host's
// now) or the timeout expires. A Docker Desktop VM clock that lagged during
// host sleep reconverges with the host once Docker re-syncs it; this loop
// gives it that window before the start proceeds, so clawkerd exchanges its
// (host-clock-minted) Hydra assertion only after the CP clock — which fosite
// validates iat against with zero leeway — has caught up. Returns nil once
// the CP has caught up, an error on timeout/non-convergence. Respects ctx
// cancellation.
func waitForCPClockSync(ctx context.Context, cfg config.Config, log *logger.Logger) error {
	adminPort := cfg.ControlPlaneSettings().AdminPort

	start := time.Now()
	deadline := start.Add(cpClockSyncTimeout)

	log.Info().
		Str("event", "cp_clock_sync").
		Str("component", "manager.clocksync").
		Msg("probing control plane clock convergence with host")

	// Track the last probe error separately from the "CP behind host" case:
	// a probe that errors (CP not yet reachable, mTLS handshake, RPC failure)
	// is a different fault than a probe that succeeds and reports a lagging
	// clock. The per-iteration line must not mislabel one as the other, and the
	// terminal timeout must carry the real cause so an operator isn't sent
	// chasing a clock problem that is actually connectivity or auth.
	var lastProbeErr error
	for {

		if err := ctx.Err(); err != nil {
			return err
		}

		hostTime := time.Now().UTC()
		cpTime, err := probeCPTimeFn(ctx, adminPort)

		// converged once the CP clock is at or after the host's now
		if err == nil && !hostTime.After(cpTime) {
			log.Info().
				Str("event", "cp_clock_converged").
				Str("component", "manager.clocksync").
				Msg(fmt.Sprintf("control plane clock caught up to host, hostTime=%s, cpTime=%s, cp_sub_delta=%s", hostTime, cpTime, cpTime.Sub(hostTime)))
			return nil // CP clock has caught up to the host
		}

		// Both branches stay at Info: early probe errors and a still-lagging
		// clock are both expected churn during cold start (the CP AdminPort may
		// not be listening yet, and the VM clock re-syncs over a few seconds).
		// The loop retries either way; only the terminal timeout is an error.
		if err != nil {
			lastProbeErr = err
			log.Info().
				Str("event", "cp_clock_probe").
				Str("component", "manager.clocksync").
				Msg(fmt.Sprintf("reprobing: control plane clock probe failed: %v", err))
		} else {
			log.Info().
				Str("event", "cp_clock_probe").
				Str("component", "manager.clocksync").
				Msg(fmt.Sprintf("reprobing: control plane clock still behind host, hostTime=%s, cpTime=%s", hostTime, cpTime))
		}

		if time.Now().After(deadline) {
			var timeoutErr error
			if lastProbeErr != nil {
				timeoutErr = fmt.Errorf("cp clock sync deadline exceeded (last probe: %w)", lastProbeErr)
			} else {
				timeoutErr = fmt.Errorf("cp clock sync deadline exceeded")
			}
			log.Error().
				Str("event", "cp_clock_sync_timeout").
				Str("component", "manager.clocksync").
				Msg(timeoutErr.Error())
			return timeoutErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cpClockSyncInterval):
		}
	}
}

// newHealthzProbe builds the readiness loop's healthz step: each call
// of the returned function takes ONE sample of
// http://127.0.0.1:<HealthPort>/healthz — ready on HTTP 200, (false,
// nil) to keep sampling, and a non-nil error when the wait is over (the
// budget expired, the container died, or the caller cancelled). The
// wait budget and the last-probe diagnostics live in the healthzProbe
// state so the loop in awaitCPReady stays a plain sequential loop.
// Separate from
// firewall.Stack.WaitForHealthy because the CP's healthz is exposed on
// a published host port, not via the clawker network.
//
// On timeout, the returned *CPHealthTimeoutError carries the last probe
// outcome (transport error, HTTP status, body snippet) so operators can
// distinguish "port never bound" from "503 because Hydra is down"
// without re-running under debug logging.
//
// Two firewall-aware behaviors:
//   - The wait budget extends by the stack-bringup bound when
//     firewall.enable is set — the CP gates SetReady on the firewall
//     stack bringup (image pull/build + container create + health wait
//     all happen before /healthz turns green).
//   - When a healthz probe fails at the transport layer, the CP
//     container's state is checked (throttled): an exited,
//     not-restarting container is a terminal startup-gate failure (the
//     CP exits code 1 by design when the firewall bringup fails) and a
//     removed container is a terminal concurrent teardown — in both
//     cases burning the rest of the budget would only delay the
//     feedback the operator needs. Transient lookup failures keep the
//     loop polling and surface on the timeout error's diagnostics.
func newHealthzProbe(ctx context.Context, dc *docker.Client, cfg config.Config) func(context.Context) (bool, error) {
	budget := cpReadyTimeout
	if cfg.FirewallEnabled() {
		budget += consts.FirewallStackBringupRPCTimeout
	}
	start := time.Now()
	deadline := start.Add(budget)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	//nolint:exhaustruct // the last* diagnostics fields start zero and fill as probes fail
	p := &healthzProbe{
		url:        fmt.Sprintf("http://"+consts.Localhost+":%d/healthz", cfg.ControlPlaneSettings().HealthPort),
		httpClient: &http.Client{Timeout: healthzRequestTimeout},
		dc:         dc,
		cfg:        cfg,
		start:      start,
		deadline:   deadline,
	}
	return p.step
}

// healthzProbe is the readiness loop's healthz step state: the wait
// budget plus the last-probe diagnostics the terminal timeout error
// carries, so operators can distinguish "port never bound" from "503
// because Hydra is down" without re-running under debug logging.
type healthzProbe struct {
	url        string
	httpClient *http.Client
	dc         *docker.Client
	cfg        config.Config
	start      time.Time
	deadline   time.Time

	lastErr        error
	lastStatus     int
	lastBody       string
	lastLookupErr  error
	lastStateCheck time.Time
}

// step takes one /healthz sample: (true, nil) on HTTP 200, (false, nil)
// to keep sampling, and a non-nil error when the wait is over — budget
// expired (typed *CPHealthTimeoutError), container terminally dead, or
// caller cancelled.
func (p *healthzProbe) step(ctx context.Context) (bool, error) {
	// Deadline check first so a DeadlineExceeded surfaces the typed
	// error with last-probe diagnostics rather than a bare ctx error.
	// Caller cancellation returns the ctx error below.
	if time.Now().After(p.deadline) {
		return false, newCPHealthTimeout(p.start, p.url, p.lastStatus, p.lastBody, p.lastErr, p.lastLookupErr)
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("healthz wait: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return false, fmt.Errorf("build healthz request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false, p.noteTransportFailure(ctx, err)
	}
	defer resp.Body.Close()
	p.lastStatus = resp.StatusCode
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	p.lastBody = readBodySnippet(resp.Body)
	return false, nil
}

// noteTransportFailure records a probe that failed at the transport
// layer and decides whether it is terminal: healthz unreachable may
// mean the CP container is terminally dead. A non-200 HTTP response
// proves the CP process is alive, so the container-state check runs
// only here, at most once per second to keep Docker inspect chatter
// bounded. nil means keep sampling.
func (p *healthzProbe) noteTransportFailure(ctx context.Context, probeErr error) error {
	p.lastErr = probeErr
	if time.Since(p.lastStateCheck) < time.Second {
		return nil
	}
	p.lastStateCheck = time.Now()
	terminalErr, lookupErr := cpTerminalError(ctx, p.dc)
	if lookupErr != nil {
		p.lastLookupErr = lookupErr
	}
	if terminalErr == nil {
		return nil
	}
	var exitErr *CPExitedError
	if errors.As(terminalErr, &exitErr) {
		exitErr.FirewallEnabled = p.cfg.FirewallEnabled()
	}
	return terminalErr
}

// CPExitedError reports that the CP container terminally exited during
// the readiness wait — the shape a failed pre-SetReady startup gate
// (e.g. the firewall bringup) produces by design. Typed so callers and
// tests can discriminate it from a *CPHealthTimeoutError without string
// matching. FirewallEnabled gates the firewall-bringup hint so a boot
// with the firewall disabled doesn't point operators at the wrong
// subsystem.
type CPExitedError struct {
	ExitCode        int
	FirewallEnabled bool
}

func (e *CPExitedError) Error() string {
	if e.ExitCode == 0 {
		return fmt.Sprintf(
			"control plane container %s exited cleanly (code 0) during startup — likely a concurrent shutdown (drain-to-zero or `clawker controlplane down`); re-run `clawker controlplane up`",
			consts.ContainerCP,
		)
	}
	msg := fmt.Sprintf(
		"control plane container exited (code %d) during startup — inspect `docker logs %s` for the failing startup step",
		e.ExitCode,
		consts.ContainerCP,
	)
	if e.FirewallEnabled {
		msg += ". A firewall bringup failure exits by design when the firewall is enabled in settings; fix the cause, or disable the firewall in settings.yaml to run unprotected"
	}
	return msg
}

// CPGoneError reports that the CP container disappeared during the
// readiness wait — a concurrent teardown (`clawker controlplane down`,
// manual `docker rm -f`) removed it. Terminal: every further probe
// would fail at transport and burn the remaining budget without new
// information. Typed so callers and tests can discriminate it without
// string matching.
type CPGoneError struct{}

func (e *CPGoneError) Error() string {
	return fmt.Sprintf(
		"control plane container %s no longer exists — it was removed while the readiness wait was in progress (concurrent `clawker controlplane down`?); re-run `clawker controlplane up`",
		consts.ContainerCP,
	)
}

// cpTerminalError checks the CP container and returns a terminal error
// when further healthz polling cannot succeed: *CPGoneError when the
// container no longer exists, *CPExitedError when it has exited and is
// not mid-restart. Transient Docker lookup failures come back on the
// second return — the loop keeps polling through them rather than
// aborting the readiness wait, but the caller can fold a persistent
// lookup problem into its timeout diagnostics.
func cpTerminalError(ctx context.Context, dc *docker.Client) (error, error) {
	if dc == nil {
		return nil, nil
	}
	summary, err := findCPContainer(ctx, dc)
	if err != nil {
		return nil, err
	}
	if summary == nil {
		return &CPGoneError{}, nil
	}
	if summary.State != container.StateExited {
		return nil, nil
	}
	// Exited per the list — inspect for the restart-policy flag and the
	// exit code (neither is on the list summary).
	inspect, err := dc.ContainerInspect(ctx, consts.ContainerCP, whail.ContainerInspectOptions{})
	if err != nil || inspect.Container.State == nil {
		return nil, err
	}
	st := inspect.Container.State
	if st.Status != container.StateExited || st.Restarting {
		return nil, nil
	}
	return &CPExitedError{ExitCode: st.ExitCode}, nil
}

// readBodySnippet reads up to healthzBodySnippetMax bytes from r for
// inclusion in diagnostic errors. Best-effort — read errors yield an
// empty snippet rather than propagating.
func readBodySnippet(r io.Reader) string {
	const healthzBodySnippetMax = 512
	buf, err := io.ReadAll(io.LimitReader(r, healthzBodySnippetMax))
	if err != nil {
		return ""
	}
	return string(buf)
}

// CPHealthTimeoutError is returned when /healthz does not return 200
// within cpReadyTimeout. Separate from firewall.HealthTimeoutError so
// callers can distinguish "CP never came up" from "Envoy/CoreDNS
// unhealthy" via errors.As. Carries the last observed probe outcome.
type CPHealthTimeoutError struct {
	Timeout    time.Duration
	URL        string
	LastStatus int
	LastBody   string
	Err        error
	// LookupErr carries the last CP container lookup failure observed
	// during the wait — a persistent Docker-daemon problem (permissions,
	// API version) would otherwise repeat invisibly for the whole budget
	// and leave no trace in this error.
	LookupErr error
}

func newCPHealthTimeout(start time.Time, url string, lastStatus int, lastBody string, lastErr, lastLookupErr error) *CPHealthTimeoutError {
	return &CPHealthTimeoutError{
		Timeout:    time.Since(start),
		URL:        url,
		LastStatus: lastStatus,
		LastBody:   lastBody,
		Err:        lastErr,
		LookupErr:  lastLookupErr,
	}
}

func (e *CPHealthTimeoutError) Error() string {
	msg := fmt.Sprintf("clawkercp did not become ready within %s (healthz at %s)", e.Timeout, e.URL)
	switch {
	case e.Err != nil:
		msg = fmt.Sprintf("%s; last transport error: %v", msg, e.Err)
	case e.LastStatus != 0:
		msg = fmt.Sprintf("%s; last status: HTTP %d; body: %q", msg, e.LastStatus, e.LastBody)
	}
	if e.LookupErr != nil {
		msg = fmt.Sprintf("%s; last container lookup error: %v", msg, e.LookupErr)
	}
	return msg
}

func (e *CPHealthTimeoutError) Unwrap() error { return e.Err }
