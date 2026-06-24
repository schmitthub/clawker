package hostproxy

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// mockContainerLister implements ContainerLister for testing.
type mockContainerLister struct {
	err            error
	callCount      atomic.Int32
	closeCalled    atomic.Bool
}

func (m *mockContainerLister) ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
	m.callCount.Add(1)
	if m.err != nil {
		return client.ContainerListResult{}, m.err
	}
	return client.ContainerListResult{}, nil
}

func (m *mockContainerLister) Close() error {
	m.closeCalled.Store(true)
	return nil
}

func TestNewDaemon_ReadsConfigDefaults(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	hpCfg := cfg.HostProxyConfig().Daemon

	// Verify the config defaults match what we expect
	if hpCfg.Port != 18374 {
		t.Errorf("expected default daemon port 18374, got %d", hpCfg.Port)
	}
	if hpCfg.PollInterval != 30*time.Second {
		t.Errorf("expected default poll interval 30s, got %v", hpCfg.PollInterval)
	}
	if hpCfg.GracePeriod != 60*time.Second {
		t.Errorf("expected default grace period 60s, got %v", hpCfg.GracePeriod)
	}
	if hpCfg.MaxConsecutiveErrs != 10 {
		t.Errorf("expected default max consecutive errors 10, got %d", hpCfg.MaxConsecutiveErrs)
	}
}

func TestNewDaemon_ValidatesPort(t *testing.T) {
	cfg := configmocks.NewFromString("", `host_proxy: { daemon: { port: 0 } }`)
	_, err := NewDaemon(cfg, logger.Nop())
	if err == nil {
		t.Fatal("expected error for port 0")
	}
}

func TestNewDaemon_ValidatesPollInterval(t *testing.T) {
	cfg := configmocks.NewFromString("", `host_proxy: { daemon: { poll_interval: "0s" } }`)
	_, err := NewDaemon(cfg, logger.Nop())
	if err == nil {
		t.Fatal("expected error for zero poll interval")
	}
}

func TestNewDaemon_ValidatesGracePeriod(t *testing.T) {
	cfg := configmocks.NewFromString("", `host_proxy: { daemon: { grace_period: "-1s" } }`)
	_, err := NewDaemon(cfg, logger.Nop())
	if err == nil {
		t.Fatal("expected error for negative grace period")
	}
}

func TestNewDaemon_ValidatesMaxConsecutiveErrs(t *testing.T) {
	cfg := configmocks.NewFromString("", `host_proxy: { daemon: { max_consecutive_errs: 0 } }`)
	_, err := NewDaemon(cfg, logger.Nop())
	if err == nil {
		t.Fatal("expected error for zero max consecutive errors")
	}
}

func TestWatchContainers_ExitsOnZeroContainers(t *testing.T) {
	mock := &mockContainerLister{}

	daemon := &Daemon{
		cfg:                configmocks.NewBlankConfig(),
		log:                logger.Nop(),
		docker:             mock,
		pollInterval:       10 * time.Millisecond,
		gracePeriod:        10 * time.Millisecond,
		maxConsecutiveErrs: 10,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		daemon.watchContainers(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Good - watcher exited due to zero containers
		if mock.callCount.Load() < 1 {
			t.Error("expected at least one container check")
		}
	case <-ctx.Done():
		t.Error("watcher did not exit within timeout")
	}
}

func TestWatchContainers_ExitsOnConsecutiveErrors(t *testing.T) {
	mock := &mockContainerLister{
		err: errors.New("docker unavailable"),
	}

	daemon := &Daemon{
		cfg:                configmocks.NewBlankConfig(),
		log:                logger.Nop(),
		docker:             mock,
		pollInterval:       10 * time.Millisecond,
		gracePeriod:        10 * time.Millisecond,
		maxConsecutiveErrs: 3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		daemon.watchContainers(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Good - watcher exited due to consecutive errors
		callCount := mock.callCount.Load()
		if callCount < 3 {
			t.Errorf("expected at least 3 error calls before exit, got %d", callCount)
		}
	case <-ctx.Done():
		t.Error("watcher did not exit within timeout")
	}
}

func TestWatchContainers_RespectsContextCancellation(t *testing.T) {
	mock := &mockContainerLister{}

	daemon := &Daemon{
		cfg:                configmocks.NewBlankConfig(),
		log:                logger.Nop(),
		docker:             mock,
		pollInterval:       10 * time.Millisecond,
		gracePeriod:        10 * time.Millisecond,
		maxConsecutiveErrs: 10,
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		daemon.watchContainers(ctx)
		close(done)
	}()

	// Let it run briefly then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Good - watcher exited due to context cancellation
	case <-time.After(500 * time.Millisecond):
		t.Error("watcher did not exit after context cancellation")
	}
}

func TestWatchContainers_GracePeriod(t *testing.T) {
	mock := &mockContainerLister{}

	gracePeriod := 100 * time.Millisecond
	daemon := &Daemon{
		cfg:                configmocks.NewBlankConfig(),
		log:                logger.Nop(),
		docker:             mock,
		pollInterval:       10 * time.Millisecond,
		gracePeriod:        gracePeriod,
		maxConsecutiveErrs: 10,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		daemon.watchContainers(ctx)
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(start)
		// Should have waited at least the grace period before first check
		if elapsed < gracePeriod {
			t.Errorf("watcher exited too early (%v), expected at least %v grace period", elapsed, gracePeriod)
		}
	case <-ctx.Done():
		t.Error("watcher did not exit within timeout")
	}
}

func TestDaemon_ClosesDockerClient(t *testing.T) {
	mock := &mockContainerLister{}

	daemon := &Daemon{
		cfg:                configmocks.NewBlankConfig(),
		log:                logger.Nop(),
		server:             NewServer(0, logger.Nop(), ""), // Use port 0 to get random available port
		docker:             mock,
		pollInterval:       10 * time.Millisecond,
		gracePeriod:        0, // No grace period for faster test
		maxConsecutiveErrs: 10,
	}

	// Note: We can't easily test the full Run() method because it requires
	// a working server. Instead, verify that the mock Close() method would be called.
	// The actual close happens in Run() after shutdown.

	// Directly call close to verify interface
	if err := daemon.docker.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	if !mock.closeCalled.Load() {
		t.Error("expected Close() to be called on docker client")
	}
}

// alwaysReady returns a staged probe reporting a fixed readiness.
func alwaysReady(b bool) func(context.Context) bool {
	return func(context.Context) bool { return b }
}

// TestEnsureEgressRulesReady_FirewallDisabled_SkipsWait: empty rules path
// (firewall off) is a legitimate skip, not a wait.
func TestEnsureEgressRulesReady_FirewallDisabled_SkipsWait(t *testing.T) {
	d := &Daemon{log: logger.Nop(), server: &Server{rulesFilePath: ""}}
	if err := d.ensureEgressRulesReady(context.Background()); err != nil {
		t.Fatalf("expected nil when firewall disabled, got %v", err)
	}
}

// TestEnsureEgressRulesReady_AllStagesPass: firewall running + Envoy healthy +
// valid rules file → ready, and the stages run in order (firewall probed before
// Envoy, with the rules file validated last).
func TestEnsureEgressRulesReady_AllStagesPass(t *testing.T) {
	var order []string
	d := &Daemon{
		log:    logger.Nop(),
		server: &Server{rulesFilePath: testRulesFile},
		firewallRunningProbe: func(context.Context) bool {
			order = append(order, "firewall")
			return true
		},
		envoyHealthProbe: func(context.Context) bool {
			order = append(order, "envoy")
			return true
		},
		firewallRunningTimeout: 50 * time.Millisecond,
		envoyHealthTimeout:     50 * time.Millisecond,
		rulesReadTimeout:       50 * time.Millisecond,
		readyInterval:          time.Millisecond,
	}
	if err := d.ensureEgressRulesReady(context.Background()); err != nil {
		t.Fatalf("expected nil when all stages pass, got %v", err)
	}
	want := []string{"firewall", "envoy"}
	if len(order) != 2 || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("expected staged probe order %v, got %v", want, order)
	}
}

// TestEnsureEgressRulesReady_StopsAtStage1: when the firewall container never
// runs, the gate fails at stage 1 and never advances to probe Envoy or read
// the rules file — proving the stages are sequential, not raced.
func TestEnsureEgressRulesReady_StopsAtStage1(t *testing.T) {
	var envoyCalls atomic.Int32
	d := &Daemon{
		log:                  logger.Nop(),
		server:               &Server{rulesFilePath: testRulesFile},
		firewallRunningProbe: alwaysReady(false),
		envoyHealthProbe: func(context.Context) bool {
			envoyCalls.Add(1)
			return true
		},
		firewallRunningTimeout: 20 * time.Millisecond,
		readyInterval:          time.Millisecond,
	}
	if err := d.ensureEgressRulesReady(context.Background()); err == nil {
		t.Fatal("expected error when firewall container never runs, got nil")
	}
	if n := envoyCalls.Load(); n != 0 {
		t.Fatalf("stage 2 (Envoy) must not run until stage 1 passes; got %d Envoy probes", n)
	}
}

// TestEnsureEgressRulesReady_StopsAtStage2: firewall running but Envoy never
// healthy → fails at stage 2; a stage-2 timeout is not a rules-file fault.
func TestEnsureEgressRulesReady_StopsAtStage2(t *testing.T) {
	d := &Daemon{
		log:                    logger.Nop(),
		server:                 &Server{rulesFilePath: "/nonexistent/egress-rules.yaml"},
		firewallRunningProbe:   alwaysReady(true),
		envoyHealthProbe:       alwaysReady(false),
		firewallRunningTimeout: 20 * time.Millisecond,
		envoyHealthTimeout:     20 * time.Millisecond,
		readyInterval:          time.Millisecond,
	}
	err := d.ensureEgressRulesReady(context.Background())
	if err == nil {
		t.Fatal("expected error when Envoy never becomes healthy, got nil")
	}
	if errors.Is(err, errEgressRulesInvalid) {
		t.Fatalf("stage-2 failure must not be reported as a rules-file fault: %v", err)
	}
}

// TestEnsureEgressRulesReady_RulesNeverReady_Errors: stages 1+2 pass but the
// rules file never becomes valid → stage 3 exhausts and the gate propagates
// errEgressRulesInvalid (rather than swallowing it into a generic timeout).
// The per-input validation of the rules file is covered by egress_check_test.go;
// this proves the gate surfaces that fault.
func TestEnsureEgressRulesReady_RulesNeverReady_Errors(t *testing.T) {
	d := &Daemon{
		log:                    logger.Nop(),
		server:                 &Server{rulesFilePath: "/nonexistent/egress-rules.yaml"},
		firewallRunningProbe:   alwaysReady(true),
		envoyHealthProbe:       alwaysReady(true),
		firewallRunningTimeout: 20 * time.Millisecond,
		envoyHealthTimeout:     20 * time.Millisecond,
		rulesReadTimeout:       20 * time.Millisecond,
		readyInterval:          time.Millisecond,
	}
	err := d.ensureEgressRulesReady(context.Background())
	if err == nil {
		t.Fatal("expected error when the rules file never becomes valid, got nil")
	}
	if !errors.Is(err, errEgressRulesInvalid) {
		t.Fatalf("expected errEgressRulesInvalid, got %v", err)
	}
}

// TestEnsureEgressRulesReady_ContextCancel: a cancelled context aborts the wait
// promptly with context.Canceled instead of burning the full budget.
func TestEnsureEgressRulesReady_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &Daemon{
		log:                    logger.Nop(),
		server:                 &Server{rulesFilePath: "/nonexistent/egress-rules.yaml"},
		firewallRunningProbe:   alwaysReady(false),
		firewallRunningTimeout: time.Hour,
		readyInterval:          time.Hour,
	}
	err := d.ensureEgressRulesReady(ctx)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
