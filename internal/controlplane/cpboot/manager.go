package cpboot

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
	// EnsureRunning is idempotent: it builds the CP image if missing,
	// creates/starts the container on clawker-net, and blocks until the
	// aggregate /healthz endpoint returns 200 and the host↔CP clock is in
	// sync within tolerance. The clock-sync step is a readiness gate, not a
	// value source: it guarantees the CP clock has reconverged with the host
	// before a container start lets clawkerd exchange its (host-clock-minted)
	// agent assertion.
	EnsureRunning(ctx context.Context) error

	// Stop removes the CP container. SIGTERM reaches PID 1 (clawker-cp),
	// which drains the firewall stack and flushes per-container eBPF
	// state before exiting, so this leaves no orphans behind
	// (INV-B2-008). No-op when the CP container is absent.
	Stop(ctx context.Context) error

	// IsRunning reports whether a managed CP container exists AND is in
	// Docker's `running` state. Never triggers EnsureRunning — safe for
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

// manager is the production Manager. All dependencies are lazy Factory
// closures — the manager itself holds no live Docker client, config, or
// logger. Resolution happens per-method, matching the "Client(ctx)"
// contract in cmdutil.Factory.
type manager struct {
	client func(context.Context) (*docker.Client, error)
	config func() (config.Config, error)
	logger func() (*logger.Logger, error)
}

// NewManager constructs a Manager from lazy Factory accessors. Callers
// are expected to hand in the same closures that live on *cmdutil.Factory
// so the manager and direct `f.Client/Config/Logger` callers observe the
// same cached singletons.
func NewManager(
	client func(context.Context) (*docker.Client, error),
	cfg func() (config.Config, error),
	log func() (*logger.Logger, error),
) Manager {
	return &manager{client: client, config: cfg, logger: log}
}

func (m *manager) EnsureRunning(ctx context.Context) error {
	dc, err := m.client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}
	cfg, err := m.config()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	log, err := m.logger()
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}
	return EnsureRunning(ctx, EnsureOpts{
		Docker: dc,
		Config: cfg,
		Logger: log,
		HostDirs: HostDirs{
			Config: consts.ConfigDir(),
			Data:   consts.DataDir(),
			State:  consts.StateDir(),
			Cache:  consts.CacheDir(),
		},
	})
}

func (m *manager) Stop(ctx context.Context) error {
	dc, err := m.client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}
	return Stop(ctx, dc)
}

func (m *manager) IsRunning(ctx context.Context) (bool, error) {
	dc, err := m.client(ctx)
	if err != nil {
		return false, fmt.Errorf("connecting to Docker: %w", err)
	}
	return CPRunning(ctx, dc)
}

func (m *manager) ProbeHealthz(ctx context.Context) (int, error) {
	cfg, err := m.config()
	if err != nil {
		return 0, fmt.Errorf("loading config: %w", err)
	}
	return probeHealthz(ctx, cfg.Settings().ControlPlane.HealthPort)
}

// probeHealthz performs a GET on http://127.0.0.1:<port>/healthz with a
// short deadline. Separate from waitForCPHealthz (bootstrap.go) — that
// one polls for readiness with retries; this one is a point-in-time
// snapshot for `controlplane status`.
func probeHealthz(ctx context.Context, port int) (int, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
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
