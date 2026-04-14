package cpboot

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
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

	// cpStopTimeout (seconds) is the grace period before SIGKILL on Stop.
	cpStopTimeout = 30
)

// ensureMu serializes concurrent EnsureRunning calls within a single
// process (INV-B2-006). Cross-process concurrency is guarded by Docker's
// container-name uniqueness — the "already in use" recovery path below
// catches that race and reconciles to the existing container.
var ensureMu sync.Mutex

// Test seams. These are the three side-effecting steps of EnsureRunning
// that unit tests need to stub: generating CLI auth material, building
// the CP image, and polling /healthz. Production uses the real
// implementations below; tests overwrite these package-level vars.
var (
	ensureAuthFn    = auth.EnsureAuthMaterial
	ensureCPImageFn = ensureCPImage
	healthzFn       = waitForCPHealthz
)

// cpImageDockerfile is the multi-stage build recipe for the clawker-cp
// image. All base images are pinned by multi-arch manifest digest.
// clawker-cp and ebpf-manager binaries are supplied from embedded bytes
// (see ClawkerCPBinary / EBPFManagerBinary) in the build tar context.
const cpImageDockerfile = "" +
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
	"CMD [\"/usr/local/bin/clawker-cp\"]\n"

// EnsureRunning is the host-side entry point for bringing up the control
// plane. Idempotent and concurrency-safe. Returns nil when the CP
// container is running and /healthz is green.
//
// Steps (in order):
//  1. Ensure CLI auth material (CA, signing key, server cert).
//  2. Ensure CP image present (build from embedded binaries if missing).
//  3. Reconcile an existing CP container — if its mount spec diverges
//     from BuildCPContainerConfig (INV-B2-006), stop + remove so step 6
//     recreates with the authoritative mounts.
//  4. Ensure clawker-net exists (defensive guard — CLI bootstrap is
//     normally the primary owner).
//  5. Discover the network to compute the CP's static IP.
//  6. Create and start the CP container with static IP + clawker-net
//     attachment (INV-B2-014).
//  7. Poll /healthz on 127.0.0.1:<HealthPort> until 200 or timeout.
//
// On partial failure (container created but /healthz timed out) the next
// call observes the stopped/unhealthy container and reconciles.
func EnsureRunning(ctx context.Context, dc *docker.Client, cfg config.Config, log *logger.Logger) error {
	if log == nil {
		log = logger.Nop()
	}
	ensureMu.Lock()
	defer ensureMu.Unlock()

	if err := ensureAuthFn(); err != nil {
		return fmt.Errorf("controlplane: ensure auth material: %w", err)
	}

	if err := ensureCPImageFn(ctx, dc, log); err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}

	summary, err := findCPContainer(ctx, dc)
	if err != nil {
		return fmt.Errorf("controlplane: find cp: %w", err)
	}
	if summary != nil {
		divergent, err := hasMountDivergence(ctx, dc, cfg, summary.ID)
		if err != nil {
			return fmt.Errorf("controlplane: inspect cp: %w", err)
		}
		if divergent {
			log.Warn().Str("container", summary.ID).Msg("cp mount mode divergent — recreating")
			if err := stopAndRemoveCP(ctx, dc, summary.ID); err != nil {
				return fmt.Errorf("controlplane: reconcile cp: %w", err)
			}
		} else {
			if summary.State != container.StateRunning {
				if _, err := dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: summary.ID}); err != nil {
					return fmt.Errorf("controlplane: start existing cp: %w", err)
				}
			}
			return healthzFn(ctx, cfg)
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

	if err := createCPContainer(ctx, dc, cfg, netInfo.NetworkID, cpIP); err != nil {
		return fmt.Errorf("controlplane: %w", err)
	}

	return healthzFn(ctx, cfg)
}

// Stop removes the CP container. Used by `clawker controlplane down`.
// Does NOT stop Envoy or CoreDNS — callers who want those torn down must
// call the firewall handler's global-teardown RPC first (INV-B2-008).
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

// hasMountDivergence reports whether the existing CP container's mount
// spec diverges from BuildCPContainerConfig's authoritative layout
// (INV-B2-006). A missing mount, an extra mount, a flipped ReadOnly
// flag, a different host Source path, or a different mount Type all
// qualify. Any mismatch causes recreation rather than silent operation
// with a broken config.
func hasMountDivergence(ctx context.Context, dc *docker.Client, cfg config.Config, containerID string) (bool, error) {
	inspect, err := dc.ContainerInspect(ctx, containerID, whail.ContainerInspectOptions{})
	if err != nil {
		return false, err
	}
	want, err := BuildCPContainerConfig(cfg)
	if err != nil {
		return false, err
	}

	got := make(map[string]mount.Mount, len(inspect.Container.HostConfig.Mounts))
	for _, m := range inspect.Container.HostConfig.Mounts {
		got[m.Target] = m
	}
	if len(got) != len(want.Mounts) {
		return true, nil
	}
	for _, w := range want.Mounts {
		g, present := got[w.Target]
		if !present {
			return true, nil
		}
		if g.ReadOnly != w.ReadOnly || g.Source != w.Source || g.Type != w.Type {
			return true, nil
		}
	}
	return false, nil
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
// use" race from concurrent bootstraps by recovering the existing
// container and starting it.
func createCPContainer(ctx context.Context, dc *docker.Client, cfg config.Config, networkID string, ip netip.Addr) error {
	cpCfg, err := BuildCPContainerConfig(cfg)
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
		RestartPolicy: cpCfg.RestartPolicy,
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			cpCfg.NetworkName: {
				NetworkID:  networkID,
				IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: ip},
			},
		},
	}

	createResp, err := dc.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name:             consts.ContainerCP,
		Config:           containerCfg,
		HostConfig:       hostCfg,
		NetworkingConfig: netCfg,
	})
	if err != nil {
		return recoverFromNameConflict(ctx, dc, cfg, err)
	}
	if _, err := dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: createResp.ID}); err != nil {
		return fmt.Errorf("starting cp container: %w", err)
	}
	return nil
}

// recoverFromNameConflict handles the cross-process race where another
// bootstrapper created the CP container between findCPContainer and
// ContainerCreate. It validates the recovered container against the
// authoritative spec — if the name was taken by something mount-
// divergent (stale B2 container from a prior config), we force a
// re-bootstrap by surfacing the divergence. A NotConflict error
// propagates unchanged; there is nothing to recover from.
func recoverFromNameConflict(ctx context.Context, dc *docker.Client, cfg config.Config, createErr error) error {
	if !cerrdefs.IsConflict(createErr) {
		return fmt.Errorf("creating cp container: %w", createErr)
	}
	recovered, recErr := findCPContainer(ctx, dc)
	if recErr != nil {
		return fmt.Errorf("cp container name conflict (%v) and lookup failed: %w", createErr, recErr)
	}
	if recovered == nil {
		// Docker says the name is taken but the managed-label jail
		// doesn't see it. Usually means an unmanaged container squatted
		// on the name — safe recovery requires operator intervention.
		return fmt.Errorf("cp container name %q in use by an unmanaged container: %w", consts.ContainerCP, createErr)
	}
	divergent, divErr := hasMountDivergence(ctx, dc, cfg, recovered.ID)
	if divErr != nil {
		return fmt.Errorf("inspecting recovered cp container %s: %w", recovered.ID, divErr)
	}
	if divergent {
		return fmt.Errorf("recovered cp container %s has divergent mount spec; rerun to reconcile", recovered.ID)
	}
	if recovered.State == container.StateRunning {
		return nil
	}
	if _, startErr := dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: recovered.ID}); startErr != nil {
		return fmt.Errorf("starting recovered cp container: %w", startErr)
	}
	return nil
}

// ensureCPImage checks for the clawker-cp image and builds it from
// embedded binaries + pinned base images when absent. Mirrors the
// Stack.ensureCorednsImage pattern so both CP and CoreDNS images have
// the same error-surfacing behavior.
func ensureCPImage(ctx context.Context, dc *docker.Client, log *logger.Logger) error {
	if _, err := dc.ImageInspect(ctx, consts.CPImageTag); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("checking %s image: %w", consts.CPImageTag, err)
	}

	if len(ClawkerCPBinary) == 0 {
		return fmt.Errorf("%s binary not embedded — run 'make cp-binary' then rebuild clawker", consts.CPImageTag)
	}
	if len(EBPFManagerBinary) == 0 {
		return fmt.Errorf("ebpf-manager binary not embedded — run 'make ebpf-binary' then rebuild clawker")
	}

	buildCtx, err := cpBuildContext()
	if err != nil {
		return fmt.Errorf("creating cp build context: %w", err)
	}

	log.Debug().Str("image", consts.CPImageTag).Msg("building cp image from embedded binaries")
	resp, err := dc.ImageBuild(ctx, buildCtx, whail.ImageBuildOptions{
		Tags:           []string{consts.CPImageTag},
		Dockerfile:     "Dockerfile",
		Remove:         true,
		ForceRemove:    true,
		SuppressOutput: true,
	})
	if err != nil {
		return fmt.Errorf("building cp image: %w", err)
	}
	defer resp.Body.Close()
	return drainBuildStream(resp.Body, "building cp image")
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
func cpBuildContext() (io.Reader, error) {
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
	if err := writeFile("Dockerfile", []byte(cpImageDockerfile), 0o644); err != nil {
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
		// Deadline check before ctx.Err() so a timed-out ctx that the
		// poller adopted as its own deadline surfaces the typed error
		// with its captured last-probe diagnostics — callers that
		// supplied a deadline want "CP didn't come up", not bare
		// context.DeadlineExceeded. Caller-initiated cancellation
		// (context.Canceled) still returns the bare ctx error below.
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
			// ctx.Err() distinguishes Canceled from DeadlineExceeded.
			// For DeadlineExceeded, loop once more so the deadline
			// check at top surfaces the typed error. For Canceled,
			// return immediately.
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
