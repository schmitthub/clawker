package list_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	listcmd "github.com/schmitthub/clawker/internal/cmd/bundle/list"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/internal/tui"
)

func newFactory(t *testing.T, cfg config.Config) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	mgr := bundle.NewManager(cfg)
	f := &cmdutil.Factory{
		Version:         "",
		IOStreams:       ios,
		TUI:             tui.NewTUI(ios),
		Client:          nil,
		Config:          nil,
		Logger:          nil,
		CLIState:        nil,
		ProjectRegistry: nil,
		ProjectManager:  nil,
		GitManager:      nil,
		HostProxy:       nil,
		SocketBridge:    nil,
		Prompter:        nil,
		AdminClient:     nil,
		ControlPlane:    nil,
		HttpClient:      nil,
		BundleManager:   func() (*bundle.Manager, error) { return mgr, nil },
	}
	return f, out, errOut
}

// plantCachedBundle plants a cache entry carrying one stack.
func plantCachedBundle(t *testing.T, ns, name, version, url string) {
	t.Helper()
	bundletest.PlantCachedBundle(t, ns, name, version, url,
		map[string]string{"stacks/x/stack.yaml": "description: x\n"})
}

func declaration(url string) config.BundleDeclaration {
	return config.BundleDeclaration{
		Source: config.BundleSource{URL: url, Ref: "v1", SHA: "", Path: "", AutoUpdate: false},
		File:   "clawker.yaml",
	}
}

// The listing shows bundles ONLY — one honest row per identity linking the
// declaration side to the cache side — with actionable states repeated as
// stderr hints. Components are the per-type inventory commands' territory.
func TestBundleList_BundleStatusRows(t *testing.T) {
	testenv.New(t)
	plantCachedBundle(t, "acme", "tools", "1.0.0", "https://example.com/acme/tools.git")
	plantCachedBundle(t, "acme", "extra", "2.0.0", "https://example.com/acme/extra.git")
	// A second stranded value of the SAME identity — the hint must not repeat
	// per entry.
	bundletest.PlantCachedBundleSource(t, "acme", "extra", "2.1.0",
		bundle.Source{URL: "https://example.com/acme/extra.git", Ref: "v2", SHA: "", Path: ""},
		map[string]string{"stacks/x/stack.yaml": "description: x\n"})
	plantCachedBundle(t, "hand", "placed", "0.1.0", "")

	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{
			declaration("https://example.com/acme/tools.git"),
			declaration("https://example.com/acme/missing.git"),
		}
	}
	f, out, errOut := newFactory(t, cfg)

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	stdout := out.String()
	assert.Contains(t, stdout, "acme.tools")
	assert.Contains(t, stdout, "installed (clawker.yaml)")
	assert.Contains(t, stdout, "cached, not declared")
	assert.Contains(t, stdout, "cached, unmanaged (no source metadata)")
	assert.Contains(t, stdout, "declared, not installed")
	// Cached-but-undeclared rows show their receipt's display version.
	assert.Contains(t, stdout, "2.0.0")
	assert.Contains(t, stdout, "2.1.0")
	// No component rows — components belong to the per-type inventory commands.
	assert.NotContains(t, stdout, "acme.tools.x")
	assert.NotContains(t, stdout, "built-in")

	stderr := errOut.String()
	assert.Contains(t, stderr, "is not installed — run `clawker bundle install`")
	// ONE stale hint per identity — not one per stranded entry — pointing at
	// prune (the identity may still be installed via another value, so a
	// `bundle remove` remedy would purge the serving entry too).
	assert.Equal(t, 1, strings.Count(stderr, "acme.extra"),
		"stale hint must be deduplicated per identity:\n%s", stderr)
	assert.Contains(t, stderr, "bundle acme.extra has cached content no declaration addresses")
	assert.Contains(t, stderr, "clawker bundle prune")
	assert.Contains(t, stderr, "bundle hand.placed is cached without source metadata")
}

func TestBundleList_NoBundles(t *testing.T) {
	testenv.New(t)
	f, out, errOut := newFactory(t, configmocks.NewBlankConfig())

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Empty(t, out.String())
	assert.Contains(t, errOut.String(), "No bundles declared or cached.")
}

func TestBundleList_JSON(t *testing.T) {
	testenv.New(t)
	plantCachedBundle(t, "acme", "tools", "1.0.0", "https://example.com/acme/tools.git")
	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{declaration("https://example.com/acme/tools.git")}
	}
	f, out, _ := newFactory(t, cfg)

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{"--json"})
	require.NoError(t, cmd.Execute())

	s := strings.TrimSpace(out.String())
	assert.True(t, strings.HasPrefix(s, "["))
	assert.Contains(t, s, "\"bundle\":\"acme.tools\"")
	assert.Contains(t, s, "\"version\":\"1.0.0\"")
	assert.Contains(t, s, "\"status\":\"installed (clawker.yaml)\"")
	assert.Contains(t, s, "\"file\":\"clawker.yaml\"")
}

// --quiet emits one usable token per row: the identity when known, the
// declared source for a never-fetched entry.
func TestBundleList_Quiet(t *testing.T) {
	testenv.New(t)
	plantCachedBundle(t, "acme", "tools", "1.0.0", "https://example.com/acme/tools.git")
	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{
			declaration("https://example.com/acme/tools.git"),
			declaration("https://example.com/acme/missing.git"),
		}
	}
	f, out, _ := newFactory(t, cfg)

	cmd := listcmd.NewCmdList(f, nil)
	cmd.SetArgs([]string{"--quiet"})
	require.NoError(t, cmd.Execute())

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	assert.Contains(t, lines, "acme.tools")
	// The never-fetched declaration has no identity yet — its canonical source
	// coordinate is the usable token.
	assert.Contains(t, lines, "git:https://example.com/acme/missing.git//@ref:v1")
}
