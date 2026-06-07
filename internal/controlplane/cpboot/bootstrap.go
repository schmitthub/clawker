package cpboot

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
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/adminclient"
	fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
)

// Host-side CP lifecycle constants.
const (
	// cpReady* bound /healthz polling after container start.
	cpReadyTimeout  = 60 * time.Second
	cpReadyInterval = 100 * time.Millisecond

	// cpClockSync* gate readiness on host↔CP clock alignment, run as the
	// final readiness check after /healthz is green. Hydra validates the
	// CLI-signed agent assertion's iat against the CP clock with zero
	// leeway, so a lagging Docker Desktop VM clock (e.g. after host sleep,
	// before NTP re-syncs) would otherwise let the CLI bake an assertion
	// whose iat is in the CP's future — a "token used before issued"
	// rejection that poisons the container's bootstrap material with no way
	// to re-mint (the signing key never enters the container). Polling until
	// the offset falls within tolerance lets a freshly-woken VM clock catch
	// up before any assertion is minted.
	cpClockSkewTolerance = 2 * time.Second
	cpClockSyncTimeout   = 30 * time.Second
	cpClockSyncInterval  = 500 * time.Millisecond

	// cpStopTimeout (seconds) is the grace period before SIGKILL on Stop.
	cpStopTimeout = 30
)

// ensureMu serializes concurrent EnsureRunning calls within a single
// process. Cross-process concurrency is guarded by Docker's
// container-name uniqueness — the "already in use" recovery path below
// catches that race and reconciles to the existing container.
var ensureMu sync.Mutex

// Test seams for the side-effecting steps of EnsureRunning. Tests
// overwrite these to stub crypto (auth ensure), Docker image builds,
// and /healthz polling.
//
// `ensureAuthFn` is the load-bearing pre-step: bind mounts in
// BuildCPContainerConfig point at on-disk PEM files. `auth.EnsureAuthMaterial`
// is idempotent — safe to call on every EnsureRunning invocation. Without
// it, ContainerCreate fails with a missing bind source.
var (
	ensureAuthFn    = auth.EnsureAuthMaterial
	ensureCPImageFn = ensureCPImage
	healthzFn       = waitForCPHealthz
	clockSyncFn     = waitForCPClockSync
	probeSkewFn     = adminclient.ProbeClockSkew
)

// errCPRecoveryRetry is returned by recoverFromNameConflict when it
// has force-removed a stale peer-bootstrapped CP container and the
// caller should re-attempt ContainerCreate. Internal sentinel — never
// surfaces to operators.
var errCPRecoveryRetry = errors.New("cp container create should be retried after recovery")

// cpBinaryHash returns the SHA-256 hash of the embedded clawker-cp +
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

// cpImageDockerfile is the multi-stage build recipe for the clawker-cp
// image. All base images are pinned by multi-arch manifest digest.
// clawker-cp and ebpf-manager binaries are supplied from embedded bytes
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
		"COPY --from=musl /lib/ld-musl-*.so.1 /lib/\n" +
		"COPY --from=hydra /usr/bin/hydra /usr/local/bin/hydra\n" +
		"COPY --from=oathkeeper /usr/bin/oathkeeper /usr/local/bin/oathkeeper\n" +
		"COPY --from=kratos /usr/bin/kratos /usr/local/bin/kratos\n" +
		"COPY clawker-cp /usr/local/bin/clawker-cp\n" +
		"COPY ebpf-manager /usr/local/bin/ebpf-manager\n" +
		labels +
		"CMD [\"/usr/local/bin/clawker-cp\"]\n"
}

// EnsureRunning is the host-side entry point for bringing up the control
// plane. Idempotent and concurrency-safe. Returns nil when the CP
// container is running, /healthz is green, AND the host↔CP clock is in
// sync within cpClockSkewTolerance (see cpReady). A green /healthz with a
// clock still out of tolerance returns a clock-sync error, not nil — so
// callers can gate assertion minting on a fully-ready CP.
//
// Drift gate: an existing CP container whose consts.LabelCPBinarySHA
// matches the host clawker binary's embedded clawker-cp + ebpf-manager
// hash is adopted (started if stopped); any mismatch (including legacy
// containers that predate the label) is force-removed and recreated so
// the new mount/env spec reaches the running CP. Mount spec itself is
// not inspected — mounts derive from compile-time constants only, so
// any mount/env/cmd change implies a host rebuild, which changes the
// embedded bytes, which changes the SHA.
//
// On partial failure (container created but /healthz or the clock-sync
// gate timed out) the next call observes the running/unhealthy container
// and re-runs the readiness gate (clock sync self-heals once the VM
// clock re-syncs).
// EnsureOpts bundles the inputs EnsureRunning needs. HostDirs is required;
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

func EnsureRunning(ctx context.Context, opts EnsureOpts) error {
	if err := opts.HostDirs.Validate(); err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}

	dc := opts.Docker
	cfg := opts.Config
	log := opts.Logger
	if log == nil {
		log = logger.Nop()
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

	summary, err := findCPContainer(ctx, dc)
	if err != nil {
		return fmt.Errorf("controlplane: find cp: %w", err)
	}
	if summary != nil {
		desired, _ := cpBinaryHash()
		actual := summary.Labels[consts.LabelCPBinarySHA]
		if actual == desired {
			if summary.State != container.StateRunning {
				if _, err := dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: summary.ID}); err != nil {
					return fmt.Errorf("controlplane: start existing cp: %w", err)
				}
			}
			return cpReady(ctx, cfg)
		}

		cpRunning := summary.State == container.StateRunning

		activeAgents, err := dc.ContainerList(ctx, client.ContainerListOptions{
			Filters: client.Filters{}.
				Add("label", consts.LabelPurpose+"="+consts.PurposeAgent).
				Add("status", "running"),
		})
		if err != nil {
			return fmt.Errorf("controlplane: list active agents: %w", err)
		}

		if cpRunning || len(activeAgents.Items) > 0 {
			log.Error().
				Str("event", "cp_container_upgrade_blocked").
				Str("component", "cpboot.bootstrap").
				Str("container", consts.ContainerCP).
				Bool("cp_running", cpRunning).
				Int("active_agent_count", len(activeAgents.Items)).
				Msg("control plane upgrade blocked — active CP or agent containers present")
			return fmt.Errorf("clawker was upgraded and the control plane needs to be replaced, but %d agent container(s) are still running and the existing control plane is %s.\n\nTo upgrade safely:\n  1. Stop all agents:        clawker container ls\n                             clawker container stop <name>\n  2. Shut down CP (one of):  wait — CP self-shuts-down once agents reach zero\n                             clawker controlplane down  (skip the wait)\n  3. Restart agents:         clawker run <name>\n\nIf agents fail to restart cleanly after upgrade, their embedded clawkerd may need rebuilding against the new CLI:\n  clawker build\n  clawker run <name>",
				len(activeAgents.Items),
				map[bool]string{true: "still running", false: "stopped"}[cpRunning])
		}

		// Drift: either binary hash changed (host clawker was rebuilt)
		// or the container predates this label (legacy / orphaned).
		// Force-remove and recreate regardless of State — works on
		// stopped post-drain containers and on still-running stale ones
		// alike.
		log.Info().
			Str("event", "cp_container_spec_drift").
			Str("component", "cpboot.bootstrap").
			Str("container", consts.ContainerCP).
			Str("state", string(summary.State)).
			Str("desired_binary_sha256", desired).
			Str("running_binary_sha256", actual).
			Msg("recreating CP container — embedded binary or spec changed")
		if err := stopAndRemoveCP(ctx, dc, summary.ID); err != nil {
			log.Error().
				Str("event", "cp_container_force_remove_failed").
				Str("component", "cpboot.bootstrap").
				Str("container", consts.ContainerCP).
				Err(err).
				Msg("drift detected but force-remove failed; next EnsureRunning will retry")
			return fmt.Errorf("controlplane: %w", err)
		}
	}

	if _, err := dc.EnsureNetwork(ctx, whail.EnsureNetworkOptions{Name: cfg.ClawkerNetwork()}); err != nil {
		return fmt.Errorf("controlplane: ensure clawker-net: %w", err)
	}

	netInfo, err := fwcp.DiscoverNetwork(ctx, dc, cfg)
	if err != nil {
		return fmt.Errorf("controlplane: discover clawker-net: %w", err)
	}
	cpIP, err := fwcp.ComputeStaticIP(netInfo.Gateway, cfg.CPIPLastOctet())
	if err != nil {
		return fmt.Errorf("controlplane: compute cp static ip: %w", err)
	}
	if netInfo.Subnet.IsValid() && !netInfo.Subnet.Contains(cpIP) {
		return fmt.Errorf("controlplane: cp static IP %s is outside network subnet %s (check CPIPLastOctet setting)", cpIP, netInfo.Subnet)
	}

	if err := createCPContainer(ctx, dc, cfg, netInfo.NetworkID, cpIP, opts.HostDirs, imageRef, log); err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}

	return cpReady(ctx, cfg)
}

// Stop removes the CP container. Used by `clawker controlplane down`.
// Docker sends SIGTERM to PID 1 (clawker-cp), whose own shutdown path
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
// observe or tear down the CP without wanting to trigger EnsureRunning's
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
					Str("component", "cpboot.bootstrap").
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
			Str("component", "cpboot.bootstrap").
			Str("container", consts.ContainerCP).
			Msg("cp container name held by an unmanaged container")
		return fmt.Errorf("cp container name %q in use by an unmanaged container: %w", consts.ContainerCP, createErr)
	}

	inspect, err := dc.ContainerInspect(ctx, recovered.ID, whail.ContainerInspectOptions{})
	if err != nil {
		log.Error().
			Str("event", "cp_recovery_inspect_failed").
			Str("component", "cpboot.bootstrap").
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
			Str("component", "cpboot.bootstrap").
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
				Str("component", "cpboot.bootstrap").
				Str("image", imageRef).
				Err(err).
				Msg("our cp image vanished mid-recovery; retrying")
			return errCPRecoveryRetry
		}
		log.Error().
			Str("event", "cp_recovery_inspect_failed").
			Str("component", "cpboot.bootstrap").
			Str("image", imageRef).
			Err(err).
			Msg("our cp image inspect failed during recovery")
		return fmt.Errorf("inspecting our cp image %s: %w", imageRef, err)
	}
	theirsCreated, err := cpImageCreatedAt(ctx, dc, resp.Image, log)
	if err != nil {
		log.Error().
			Str("event", "cp_recovery_inspect_failed").
			Str("component", "cpboot.bootstrap").
			Str("image", resp.Image).
			Err(err).
			Msg("recovered cp image inspect failed during recovery")
		return fmt.Errorf("inspecting recovered cp image %s: %w", resp.Image, err)
	}

	logEvent := log.Info().
		Str("component", "cpboot.bootstrap").
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
					Str("component", "cpboot.bootstrap").
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

// ensureCPImage checks for the clawker-cp image and builds it from
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
		return "", fmt.Errorf("clawker-cp binary not embedded — run 'make cp-binary' then rebuild clawker")
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
			Str("component", "cpboot.bootstrap").
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
					Str("component", "cpboot.bootstrap").
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
// clawker-cp + ebpf-manager) that ImageBuild expects.
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
	if err := writeFile("clawker-cp", ClawkerCPBinary, 0o755); err != nil {
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

// cpReady is the composite readiness gate run before EnsureRunning
// returns success: /healthz green first, then host↔CP clock alignment.
// Both must pass — a healthy CP whose clock has drifted from the host
// would still mint poisoned agent assertions, so clock sync is a
// first-class readiness condition, not an afterthought.
func cpReady(ctx context.Context, cfg config.Config) error {
	if err := healthzFn(ctx, cfg); err != nil {
		return err
	}
	return clockSyncFn(ctx, cfg)
}

// waitForCPClockSync polls the public GetSystemTime RPC until the
// host↔CP clock offset falls within cpClockSkewTolerance or the timeout
// expires. A Docker Desktop VM clock that lagged during host sleep
// converges to real time once its NTP source re-syncs; this loop gives
// it that window before any Hydra assertion (validated against the CP
// clock with zero leeway) is minted. Respects ctx cancellation.
func waitForCPClockSync(ctx context.Context, cfg config.Config) error {
	adminPort := cfg.Settings().ControlPlane.AdminPort

	start := time.Now()
	deadline := start.Add(cpClockSyncTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	var lastSkew time.Duration
	var lastErr error
	measured := false
	for {
		if time.Now().After(deadline) {
			return newCPClockSyncTimeout(start, lastSkew, measured, lastErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		skew, err := probeSkewFn(ctx, adminPort)
		if err != nil {
			lastErr = err
		} else {
			lastSkew, measured = skew, true
			if absDuration(skew) <= cpClockSkewTolerance {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return ctx.Err()
			}
		case <-time.After(cpClockSyncInterval):
		}
	}
}

// newCPClockSyncTimeout builds the actionable error returned when the
// host↔CP clock never converges. The message names the most common
// cause (a lagging Docker Desktop VM clock after sleep) and the fix so
// an operator isn't left re-debugging an opaque bootstrap failure.
func newCPClockSyncTimeout(start time.Time, lastSkew time.Duration, measured bool, lastErr error) error {
	waited := time.Since(start).Round(time.Millisecond)
	if !measured {
		return fmt.Errorf("control plane clock-sync probe never succeeded after %s (tolerance %s): %w", waited, cpClockSkewTolerance, lastErr)
	}
	return fmt.Errorf("control plane clock not in sync with host after %s: last measured offset %s exceeds tolerance %s — the Docker VM clock is likely lagging after host sleep; wait for it to re-sync (or restart Docker Desktop), then retry", waited, lastSkew.Round(time.Millisecond), cpClockSkewTolerance)
}

// absDuration returns the magnitude of d so clock skew is compared
// regardless of direction (CP ahead of or behind the host).
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// waitForCPHealthz polls http://127.0.0.1:<HealthPort>/healthz until the
// CP reports aggregate readiness (HTTP 200) or the ctx/timeout expires.
// Separate from firewall.Stack.WaitForHealthy because the CP's healthz
// is exposed on a published host port, not via clawker-net.
//
// On timeout, the returned *CPHealthTimeoutError carries the last probe
// outcome (transport error, HTTP status, body snippet) so operators can
// distinguish "port never bound" from "503 because Hydra is down"
// without re-running under debug logging.
func waitForCPHealthz(ctx context.Context, cfg config.Config) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Settings().ControlPlane.HealthPort)
	httpClient := &http.Client{Timeout: 2 * time.Second}

	start := time.Now()
	deadline := start.Add(cpReadyTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	var lastErr error
	var lastStatus int
	var lastBody string
	for {
		// Deadline check first so a DeadlineExceeded surfaces the typed
		// error with last-probe diagnostics rather than bare ctx.Err().
		// Caller Canceled returns the bare ctx error via the select below.
		if time.Now().After(deadline) {
			return newCPHealthTimeout(start, url, lastStatus, lastBody, lastErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("build healthz request: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			lastStatus = resp.StatusCode
			if resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			lastBody = readBodySnippet(resp.Body)
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return ctx.Err()
			}
		case <-time.After(cpReadyInterval):
		}
	}
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
}

func newCPHealthTimeout(start time.Time, url string, lastStatus int, lastBody string, lastErr error) *CPHealthTimeoutError {
	return &CPHealthTimeoutError{
		Timeout:    time.Since(start),
		URL:        url,
		LastStatus: lastStatus,
		LastBody:   lastBody,
		Err:        lastErr,
	}
}

func (e *CPHealthTimeoutError) Error() string {
	msg := fmt.Sprintf("clawker-cp did not become ready within %s (healthz at %s)", e.Timeout, e.URL)
	switch {
	case e.Err != nil:
		return fmt.Sprintf("%s; last transport error: %v", msg, e.Err)
	case e.LastStatus != 0:
		return fmt.Sprintf("%s; last status: HTTP %d; body: %q", msg, e.LastStatus, e.LastBody)
	}
	return msg
}

func (e *CPHealthTimeoutError) Unwrap() error { return e.Err }
