package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

//go:embed scenarios
var scenariosFS embed.FS

// fawkerFactory builds a Factory with faked deps and returns a pointer to the
// scenario name (populated by the --scenario flag on the root command).
func fawkerFactory() (*cmdutil.Factory, *string) {
	scenario := "multi-stage" // default, overridden by --scenario flag

	ios := iostreams.System()

	f := &cmdutil.Factory{
		Version:  "0.0.0-fawker",
		Commit:   "fawker",
		IOStreams: ios,
		TUI:      tui.NewTUI(ios),
		Config: func() *config.Config {
			return config.NewConfigForTest(fawkerProject(), config.DefaultSettings())
		},
		Client: func(_ context.Context) (*docker.Client, error) {
			return fawkerClient(scenario)
		},
		GitManager:   func() (*git.GitManager, error) { return nil, nil },
		HostProxy:    func() *hostproxy.Manager { return nil },
		SocketBridge: func() socketbridge.SocketBridgeManager { return nil },
		Prompter:     func() *prompter.Prompter { return nil },
	}

	return f, &scenario
}

// fawkerProject returns a minimal config.Project for the fawker demo.
func fawkerProject() *config.Project {
	return &config.Project{
		Version: "1",
		Project: "fawker-demo",
		Build: config.BuildConfig{
			Image: "node:20-slim",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{
				Enable: false,
			},
		},
	}
}

// fawkerClient builds a fake Docker client wired with the selected scenario's
// recorded events for build progress.
func fawkerClient(scenarioName string) (*docker.Client, error) {
	fake := dockertest.NewFakeClient()

	// Wire BuildKit detection so buildRun's BuildKitEnabled() check passes.
	fake.SetupPingBuildKit()

	// Wire build progress from recorded scenario.
	scenario, err := loadEmbeddedScenario(scenarioName)
	if err != nil {
		return nil, fmt.Errorf("fawker: %w", err)
	}
	fake.SetupBuildKitWithRecordedProgress(scenario.Events)

	// Wire container list with some demo fixtures.
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("fawker-demo", "ralph"),
		dockertest.ContainerFixture("fawker-demo", "worker", "node:20-slim"),
	)

	// Wire image list with demo fixtures.
	fake.SetupImageList(
		dockertest.ImageSummaryFixture("clawker-fawker-demo:latest"),
		dockertest.ImageSummaryFixture("node:20-slim"),
	)

	return fake.Client, nil
}

// loadEmbeddedScenario loads a recorded scenario from the embedded scenarios/ dir.
// Falls back to filesystem lookup for development (when running from repo root).
func loadEmbeddedScenario(name string) (*whailtest.RecordedBuildScenario, error) {
	filename := name + ".json"

	// Try embedded FS first (works in built binary).
	data, err := scenariosFS.ReadFile(filepath.Join("scenarios", filename))
	if err == nil {
		return whailtest.LoadRecordedScenarioFromBytes(data)
	}

	// Fallback: try filesystem relative to source (for go run).
	_, thisFile, _, _ := runtime.Caller(0)
	fsPath := filepath.Join(filepath.Dir(thisFile), "scenarios", filename)
	if _, statErr := os.Stat(fsPath); statErr == nil {
		return whailtest.LoadRecordedScenario(fsPath)
	}

	// Fallback: try testdata in whailtest package.
	testdataPath := filepath.Join("pkg", "whail", "whailtest", "testdata", filename)
	if _, statErr := os.Stat(testdataPath); statErr == nil {
		return whailtest.LoadRecordedScenario(testdataPath)
	}

	return nil, fmt.Errorf("scenario %q not found (tried embedded, source dir, and testdata)", name)
}
