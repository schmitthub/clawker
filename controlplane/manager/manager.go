package manager

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

//go:generate go run github.com/matryer/moq@v0.5.3 -out mocks/manager_mock.go -pkg mocks . Manager

// Manager is the CLI-facing noun for the host-side clawker control plane
// lifecycle. CLI commands that need to bring the CP up, tear it down, or
// observe its health go through this interface rather than importing the
// package-level functions directly — that keeps the Factory the single
// place where Docker/Config/Logger resolution is wired and lets tests
// inject a fake without reaching into package-level seams.
type Manager interface {
	// Start is idempotent: it builds the CP image if missing,
	// creates/starts the container on the clawker network, and blocks until the
	// aggregate /healthz endpoint returns 200 and the CP clock has caught up
	// to the host. The clock-sync step is a readiness gate, not a
	// value source: it guarantees the CP clock has reconverged with the host
	// before a container start lets clawkerd exchange its (host-clock-minted)
	// agent assertion.
	//
	// See it running or start it, then report the outcome — that is the whole
	// contract. A boot the CP cannot finish alone comes back as a
	// *CPSOSError describing what it needs; acting on that is the caller's
	// job, not this one's.
	Start(ctx context.Context) error

	// Stop removes the CP container. SIGTERM reaches PID 1 (clawkercp),
	// which drains the firewall stack and flushes per-container eBPF
	// state before exiting, so this leaves no orphans behind
	// (INV-B2-008). No-op when the CP container is absent.
	Stop(ctx context.Context) error

	// IsRunning reports whether a managed CP container exists AND is in
	// Docker's `running` state. Never triggers Start — safe for
	// status commands that must not bootstrap as a side effect.
	IsRunning(ctx context.Context) (bool, error)

	// ProbeHealthz performs a single short-deadline GET against the CP's
	// /healthz endpoint on the configured HealthPort. Returns the HTTP
	// status on any response (caller decides if 200 is required), or
	// (0, err) on transport failure.
	ProbeHealthz(ctx context.Context) (int, error)
}

// probeHealthzTimeout bounds each HTTP probe. Short enough to fail fast
// on a dead CP; long enough to tolerate a slow localhost handshake.
const probeHealthzTimeout = 2 * time.Second

// manager is the production Manager. It holds the live dependencies it was
// built with and nothing else — resolving a Docker client, config, or logger
// is the caller's business, done once where the manager is constructed.
type manager struct {
	docker *docker.Client
	config config.Config
	logger *logger.Logger
}

// NewManager constructs a Manager over already-resolved dependencies. The
// Factory (or a command that builds its own) resolves them; by the time a
// Manager exists there is nothing left for it to fail at except talking to
// the control plane.
//
//nolint:ireturn // Manager is the mockable seam every consumer holds; handing back the struct would defeat it
func NewManager(dc *docker.Client, cfg config.Config, log *logger.Logger) Manager {
	return &manager{docker: dc, config: cfg, logger: log}
}

func (m *manager) Start(ctx context.Context) error {
	return ensureRunning(ctx, EnsureOpts{
		Docker: m.docker,
		Config: m.config,
		Logger: m.logger,
		HostDirs: HostDirs{
			Config: consts.ConfigDir(),
			Data:   consts.DataDir(),
			State:  consts.StateDir(),
			Cache:  consts.CacheDir(),
		},
	})
}

func (m *manager) Stop(ctx context.Context) error {
	return Stop(ctx, m.docker)
}

func (m *manager) IsRunning(ctx context.Context) (bool, error) {
	return CPRunning(ctx, m.docker)
}

func (m *manager) ProbeHealthz(ctx context.Context) (int, error) {
	return probeHealthz(ctx, m.config.ControlPlaneSettings().HealthPort)
}

// probeHealthz performs a GET on http://127.0.0.1:<port>/healthz with a
// short deadline. Separate from newHealthzProbe (bootstrap.go) — that
// one drives the readiness wait with budget and diagnostics; this one
// is a point-in-time snapshot for `controlplane status`.
func probeHealthz(ctx context.Context, port int) (int, error) {
	url := fmt.Sprintf("http://"+consts.Localhost+":%d/healthz", port)
	httpClient := &http.Client{Timeout: probeHealthzTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("building healthz request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("probing healthz: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
