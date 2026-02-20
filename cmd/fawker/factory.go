package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"gopkg.in/yaml.v3"
)

//go:embed scenarios
var scenariosFS embed.FS

// fawkerFactory builds a Factory with faked deps and returns a pointer to the
// scenario name (populated by the --scenario flag on the root command).
func fawkerFactory() (*cmdutil.Factory, *string) {
	scenario := "multi-stage" // default, overridden by --scenario flag

	ios := iostreams.System()
	configFn := fawkerConfigFunc()

	f := &cmdutil.Factory{
		Version:   "0.0.0-fawker",
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		Config:    configFn,
		Client: func(_ context.Context) (*docker.Client, error) {
			// Mirror real factory: inject config into client for image resolution.
			cfg, err := configFn()
			if err != nil {
				return nil, fmt.Errorf("fawker config: %w", err)
			}
			return fawkerClient(scenario, cfg)
		},
		GitManager:   func() (*git.GitManager, error) { return nil, nil },
		HostProxy:    func() hostproxy.HostProxyService { return nil },
		SocketBridge: func() socketbridge.SocketBridgeManager { return nil },
		Prompter:     func() *prompter.Prompter { return prompter.NewPrompter(ios) },
	}

	return f, &scenario
}

// fawkerConfigFunc returns a lazy Config constructor with sync.Once semantics.
// Uses configmocks.NewFromString to avoid temp directory creation and filesystem leaks.
func fawkerConfigFunc() func() (config.Config, error) {
	var (
		once sync.Once
		cfg  config.Config
	)
	return func() (config.Config, error) {
		once.Do(func() {
			project := fawkerProject()
			yamlData, _ := yaml.Marshal(project)
			// Project.Project has yaml:"-" so it's not marshaled — prepend it.
			// Also prepend default_image so the config knows the default image tag.
			cfgYAML := fmt.Sprintf("project: %s\ndefault_image: %s\n%s", project.Project, docker.DefaultImageTag, string(yamlData))
			cfg = configmocks.NewFromString(cfgYAML)
		})
		return cfg, nil
	}
}

// fawkerProject returns a minimal config.Project for the fawker demo.
func fawkerProject() *config.Project {
	hostProxyDisabled := false
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
			EnableHostProxy: &hostProxyDisabled,
		},
	}
}

// fawkerClient builds a fake Docker client wired with the selected scenario's
// recorded events for build progress.
func fawkerClient(scenarioName string, cfg config.Config) (*docker.Client, error) {
	fake := dockertest.NewFakeClient(cfg)

	// Wire legacy image build as fallback for non-BuildKit paths.
	fake.SetupLegacyBuild()

	// Wire BuildKit detection so buildRun's BuildKitEnabled() check passes.
	fake.SetupPingBuildKit()

	// Wire build progress from recorded scenario.
	scenario, err := loadEmbeddedScenario(scenarioName)
	if err != nil {
		return nil, fmt.Errorf("fawker: %w", err)
	}
	capture := fake.SetupBuildKitWithRecordedProgress(scenario.Events)
	capture.DelayMultiplier = 5 // slow down for visual review

	// Wire container list with some demo fixtures.
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("fawker-demo", "dev"),
		dockertest.ContainerFixture("fawker-demo", "worker", "node:20-slim"),
	)

	// Wire image list with demo fixtures.
	// NOTE: Do NOT include a project image with :latest tag here — findProjectImage
	// would match it and skip the default image rebuild flow that we want to demo.
	// Use a version tag instead so image ls shows the image but run @ triggers rebuild.
	fake.SetupImageList(
		dockertest.ImageSummaryFixture("clawker-fawker-demo:sha-abc123"),
		dockertest.ImageSummaryFixture("node:20-slim"),
	)

	// Wire ImageExists to return true for default image — skips the interactive
	// rebuild prompt flow that requires TTY input. The rebuild progress display
	// will be wired separately when fawker gets its own run orchestration.
	fake.SetupImageExists(docker.DefaultImageTag, true)

	// Wire container create/start so `fawker container create @` completes after rebuild.
	fake.SetupContainerCreate()
	fake.SetupContainerStart()

	// Wire volume/network/copy operations used by the container run/create flow.
	// Default FakeAPIClient inspect handlers make volumes/networks appear pre-existing,
	// so EnsureVolume/EnsureNetwork skip creation. CopyToContainer is needed if
	// InjectOnboardingFile triggers (host auth enabled).
	fake.SetupCopyToContainer()
	fake.SetupVolumeCreate()
	fake.SetupNetworkCreate()

	// Wire interactive mode operations for `fawker container run -it`.
	// ContainerAttach returns a pipe that immediately closes (simulates instant exit).
	// ContainerWait returns exit code 0. ContainerResize is a no-op.
	fake.SetupContainerAttach()
	fake.SetupContainerWait(0)
	fake.SetupContainerResize()
	fake.SetupContainerRemove()

	// Wire BuildDefaultImage override — replays recorded scenario for rebuild flow.
	fake.Client.BuildDefaultImageFunc = fakeBuildDefaultImage(scenarioName)

	return fake.Client, nil
}

// fakeBuildDefaultImage returns a fake BuildDefaultImage function that replays
// recorded build progress events for visual UAT.
func fakeBuildDefaultImage(scenarioName string) docker.BuildDefaultImageFn {
	return func(_ context.Context, _ string, onProgress whail.BuildProgressFunc) error {
		scenarioData, err := loadEmbeddedScenario(scenarioName)
		if err != nil {
			return fmt.Errorf("fawker: loading scenario for rebuild: %w", err)
		}

		for _, recorded := range scenarioData.Events {
			delay := time.Duration(recorded.DelayMs) * time.Millisecond * 5 // slow for visual review
			time.Sleep(delay)
			if onProgress != nil {
				onProgress(recorded.Event)
			}
		}
		return nil
	}
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
	_, thisFile, _, ok := runtime.Caller(0)
	if ok {
		fsPath := filepath.Join(filepath.Dir(thisFile), "scenarios", filename)
		if _, statErr := os.Stat(fsPath); statErr == nil {
			return whailtest.LoadRecordedScenario(fsPath)
		}
	}

	// Fallback: try testdata in whailtest package.
	testdataPath := filepath.Join("pkg", "whail", "whailtest", "testdata", filename)
	if _, statErr := os.Stat(testdataPath); statErr == nil {
		return whailtest.LoadRecordedScenario(testdataPath)
	}

	return nil, fmt.Errorf("scenario %q not found (tried embedded, source-relative, and testdata)", name)
}
