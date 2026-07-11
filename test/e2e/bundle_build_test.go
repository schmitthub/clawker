package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// bundledStackMarkerPath is where the bundled stack's root fragment writes its
// proof file inside the base image; a container started from the built harness
// image must be able to read it, proving the qualified bundle stack fragment was
// resolved out of the host cache and composed into the real build.
const bundledStackMarkerPath = "/tmp/clawker-bundled-stack.marker"

// TestBundledStackBuild_E2E is the one real-Docker bundled-stack build: a bundle
// published on the in-process git fixture is declared, fetched with
// `clawker bundle install`, selected by its qualified address in build.stacks,
// and built with `clawker build`. The bundle's stack fragment writing a marker
// into the base image, read back out of a running container, proves the whole
// declare → install → cache → qualified-resolve → render → docker-build path end
// to end against a real daemon.
//
// Runs at host UAT only (a real build + container start needs the CP/firewall
// stack); excluded from `make test` by directory.
func TestBundledStackBuild_E2E(t *testing.T) {
	const (
		projectName = "bundle-build"
		agentName   = "bundle-build-agent"
	)

	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", map[string]string{
		filepath.Join(bundle.MarkerDir, bundle.ManifestFile): "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"stacks/extra/" + bundler.StackManifestFile:          "description: bundled extra stack\n",
		"stacks/extra/" + bundler.StackRootFragmentFile:      "RUN echo bundled-stack-ok > " + bundledStackMarkerPath + "\n",
	})
	repo.Tag(t, "v1.0.0")

	h := &harness.Harness{
		T:       t,
		Opts:    bundleHarnessOpts(),
		Cleanup: nil,
	}
	setup := h.NewIsolatedFS(&harness.FSOptions{ProjectDir: projectName})

	// Register + scaffold the project, then rewrite its config to declare the
	// bundle source and select the qualified stack in build.stacks.
	initRes := h.Run("project", "init", projectName, "--yes", "--preset", "Bare", "--vcs", "github")
	require.NoError(t, initRes.Err, "init failed\nstdout: %s\nstderr: %s", initRes.Stdout, initRes.Stderr)

	writeBundleProjectConfig(t, setup.ProjectDir, srv.HTTPURL("tools"))

	// Fetch the declared-but-missing bundle into the host cache.
	installRes := h.Run("bundle", "install")
	require.NoError(t, installRes.Err,
		"bundle install failed\nstdout: %s\nstderr: %s", installRes.Stdout, installRes.Stderr)

	// Build: the qualified stack resolves from the cache and its fragment renders
	// into the base image.
	buildRes := h.Run("build", "--progress=none")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s", buildRes.Stdout, buildRes.Stderr)

	ctx := context.Background()
	dc, err := docker.NewClient(ctx, nil, logger.Nop())
	require.NoError(t, err, "docker client for image assertions")
	t.Cleanup(func() { _ = dc.Close() })

	baseRef := docker.BaseImageTag(projectName)
	_, err = dc.ImageInspect(ctx, baseRef)
	require.NoError(t, err, "shared base image %s must exist after build", baseRef)

	// The strongest proof: the bundled fragment actually EXECUTED in the base
	// image — a container from the harness image reads its marker.
	catRes := h.RunInContainer(agentName, "cat", bundledStackMarkerPath)
	require.NoError(t, catRes.Err,
		"reading bundled stack marker failed\nstdout: %s\nstderr: %s", catRes.Stdout, catRes.Stderr)
	assert.Contains(t, catRes.Stdout, "bundled-stack-ok",
		"the bundled stack fragment must have run during the real build")
}

// bundleHarnessOpts returns FactoryOptions wired with the production
// constructors the bundle/monitor E2E tests exercise; the CP/git/host-proxy
// nouns default to the harness's fakes (nil).
func bundleHarnessOpts() *harness.FactoryOptions {
	return &harness.FactoryOptions{
		Config:             config.NewConfig,
		Client:             docker.NewClient,
		ProjectManager:     project.NewProjectManager,
		GitManager:         nil,
		HostProxy:          nil,
		SocketBridge:       nil,
		UseRealAdminClient: false,
		ControlPlane:       nil,
	}
}

// writeBundleProjectConfig overwrites the project's .clawker.yaml with a config
// that declares the fixture bundle source and selects its qualified stack. The
// project is already registered by `project init`; rewriting the file content
// leaves that registration intact (the registry keys on path, not content).
func writeBundleProjectConfig(t *testing.T, projectDir, bundleURL string) {
	t.Helper()
	doc := `version: "1"
bundles:
  - url: ` + bundleURL + `
    ref: v1.0.0
build:
  stacks: [acme.tools.extra]
security:
  firewall:
    add_domains:
      - github.com
      - api.github.com
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".clawker.yaml"), []byte(doc), 0o600))
}
