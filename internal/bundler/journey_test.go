package bundler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// The full bundle journey renders a marker the fixture's stack fragment carries;
// asserting it in the base Dockerfile proves the installed, qualified stack was
// resolved out of the host cache and its fragment composed into the image.
const journeyStackMarker = "JOURNEY-STACK-FRAGMENT"

// journeyBundleFiles is a one-stack bundle: the marker dir plus a single
// stacks/node convention dir whose root fragment carries [journeyStackMarker].
func journeyBundleFiles(version string) map[string]string {
	return map[string]string{
		filepath.Join(bundle.MarkerDir, bundle.ManifestFile): "namespace: acme\nname: tools\nversion: " + version + "\n",
		"stacks/node/" + bundler.StackManifestFile:           "description: journey node\n",
		"stacks/node/" + bundler.StackRootFragmentFile:       "RUN echo " + journeyStackMarker + "\n",
	}
}

// journeySettingsYAML is the monitoring settings block the base generator reads
// for its OTEL wiring — the only settings surface a base render touches.
func journeySettingsYAML() string {
	return `
monitoring:
  otel_collector_port: 4318
  otel_collector_host: "localhost"
  telemetry:
    metric_export_interval_ms: 10000
    logs_export_interval_ms: 5000
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`
}

// journeyConfig wires one config over the isolated tiers: a project whose
// build.stacks selects the qualified installed stack, anchored at projectRoot.
// The same config drives the Manager (install into the XDG cache) and the
// ProjectGenerator (resolve that cache at render time).
func journeyConfig(t *testing.T, projectRoot string) *configmocks.ConfigMock {
	t.Helper()
	cfg := configmocks.NewFromString("version: \"1\"\nbuild:\n  stacks: [acme.tools.node]\n", journeySettingsYAML())
	cfg.ProjectRootFunc = func() string { return projectRoot }
	return cfg
}

// newGenerator builds a base-image generator over cfg with the hermetic
// version/base-ref a render needs (no network, no real project name).
func newGenerator(t *testing.T, cfg config.Config) *bundler.ProjectGenerator {
	t.Helper()
	gen := bundler.NewProjectGenerator(cfg, t.TempDir())
	gen.HarnessVersion = "9.9.9"
	gen.BaseImageRef = "clawker-test:base"
	return gen
}

// TestBundleJourney_InstallToRender drives the entire non-Docker bundle journey
// end to end over BOTH git transports: declare a source → real fetch/clone into
// the host cache → assert the cache layout → select the qualified stack in
// build.stacks → GenerateBase resolves it out of the cache and composes its
// fragment. Every step is real production code against the in-process git
// fixture — no mocked transport, no hand-planted cache.
func TestBundleJourney_InstallToRender(t *testing.T) {
	transports := []struct {
		name string
		url  func(*bundletest.Server) string
	}{
		{name: "http", url: func(s *bundletest.Server) string { return s.HTTPURL("tools") }},
		{name: "ssh", url: func(s *bundletest.Server) string { return s.SSHURL("tools") }},
	}

	for _, tp := range transports {
		t.Run(tp.name, func(t *testing.T) {
			env := testenv.New(t)
			srv := bundletest.New(t)
			repo := srv.InitRepo(t, "tools")
			repo.Commit(t, "v1", journeyBundleFiles("1.0.0"))
			repo.Tag(t, "v1.0.0")

			projectRoot := filepath.Join(env.Dirs.Base, "project")
			require.NoError(t, os.MkdirAll(projectRoot, 0o755))

			cfg := journeyConfig(t, projectRoot)
			mgr := bundle.NewManager(cfg)
			ctx := context.Background()

			// Declare → install: a real clone of the tagged ref into the cache.
			src := config.BundleSource{
				URL: tp.url(srv), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false,
			}
			require.NoError(t, mgr.Install(ctx, src))

			// Cache layout: <data>/bundles/<ns>/<name>/<version>/<convention>/...,
			// content roots only (the .git dir is stripped on commit).
			cacheRoot, err := consts.BundlesSubdir()
			require.NoError(t, err)
			versionDir := filepath.Join(cacheRoot, "acme", "tools", "1.0.0")
			assert.FileExists(t, filepath.Join(versionDir, "stacks", "node", bundler.StackManifestFile))
			assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", "source.yaml"))
			assert.NoDirExists(t, filepath.Join(versionDir, ".git"))

			// Selection → render: build.stacks names the qualified address, so
			// GenerateBase resolves it from the cache and composes its fragment.
			gen := newGenerator(t, cfg)
			base, err := gen.GenerateBase()
			require.NoError(t, err)
			assert.Contains(t, string(base), journeyStackMarker,
				"the installed qualified stack fragment must render into the base image")

			// The build records where the non-floor stack resolved from.
			assert.Contains(t, strings.Join(gen.Provenance(), "\n"),
				"stack acme.tools.node ← bundle acme.tools")
		})
	}
}

// TestBundleJourney_FailedUpdateStillBuilds proves the cache-keeps-serving
// contract end to end: a failed update (its source gone unreachable) leaves the
// previously fetched content in place, and a subsequent base render still
// resolves and composes the cached stack. A SourceError never purges the cache.
func TestBundleJourney_FailedUpdateStillBuilds(t *testing.T) {
	env := testenv.New(t)
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", journeyBundleFiles("1.0.0"))

	projectRoot := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(projectRoot, 0o755))

	cfg := journeyConfig(t, projectRoot)
	mgr := bundle.NewManager(cfg)
	ctx := context.Background()

	// Track a moving branch so the source is a ref (updatable), not a sha-pin.
	require.NoError(t, mgr.Install(ctx, config.BundleSource{
		URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: false,
	}))

	// Repoint the cached source at an unreachable repository, then update:
	// the resolve fails but the fetched content is untouched.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	repointJourneySource(t,
		filepath.Join(cacheRoot, "acme", "tools", "source.yaml"), srv.HTTPURL("tools"), srv.HTTPURL("gone"))

	results, err := mgr.Update(ctx, bundle.BundleID{Namespace: "acme", Name: "tools"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, bundle.UpdateFailed, results[0].Outcome)
	require.Error(t, results[0].Err)

	// The base still builds off the retained cache.
	base, err := newGenerator(t, cfg).GenerateBase()
	require.NoError(t, err)
	assert.Contains(t, string(base), journeyStackMarker,
		"a failed update must leave the cache serving, so the build still renders the stack")
}

// repointJourneySource rewrites the cache-internal source.yaml URL, swapping the
// from URL for to so an update targets an unreachable repository without
// disturbing the fetched content.
func repointJourneySource(t *testing.T, path, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	updated := strings.Replace(string(raw), from, to, 1)
	require.NotEqual(t, string(raw), updated, "source url not found in %s", path)
	require.NoError(t, os.WriteFile(path, []byte(updated), 0o600))
}
