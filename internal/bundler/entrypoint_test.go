package bundler

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEntrypoint_TimeoutMatchesConsts pins the rendered timeout to
// post-init + 60s slack so the bash timeout stays strictly later than
// the CP-side per-step ceiling — failures must surface as CP's
// structured init event, not a bare shell timeout.
func TestEntrypoint_TimeoutMatchesConsts(t *testing.T) {
	wantSeconds := int(consts.InitStepTimeoutPostInitSeconds) + 60

	re := regexp.MustCompile(`CLAWKER_INIT_TIMEOUT:-(\d+)`)
	matches := re.FindAllStringSubmatch(EntrypointScript, -1)
	require.NotEmpty(t, matches, "no CLAWKER_INIT_TIMEOUT default found in rendered entrypoint")

	for _, m := range matches {
		got, err := strconv.Atoi(m[1])
		require.NoError(t, err)
		assert.Equal(t, wantSeconds, got,
			"rendered timeout default %ds must equal consts.InitStepTimeoutPostInitSeconds (%d) + 60s slack",
			got, consts.InitStepTimeoutPostInitSeconds)
	}
}

// TestEntrypoint_GuardsPresent pins the loud-failure guards. Without
// them, first-boot bootstrap failures (missing binary, bad cert
// chain, bound port) hang the container until the init timeout with
// no diagnostic.
func TestEntrypoint_GuardsPresent(t *testing.T) {
	for _, want := range []string{
		`trap 'echo "[clawker] error component=entrypoint cmd=${BASH_COMMAND} status=$?" >&2' ERR`,
		`if [ ! -x /usr/local/bin/clawkerd ]; then`,
		`if ! kill -0 "${clawkerd_pid}" 2>/dev/null; then`,
		`[clawker] error component=clawkerd msg=binary missing`,
		`[clawker] error component=clawkerd msg=daemon exited at startup`,
	} {
		assert.Contains(t, EntrypointScript, want,
			"loud-failure guard missing from rendered entrypoint: %q", want)
	}
}

// TestEntrypoint_PathsFromConsts pins fifo and ready-marker paths to
// consts and catches unrendered {{ .X }} placeholders leaking through.
func TestEntrypoint_PathsFromConsts(t *testing.T) {
	assert.Contains(t, EntrypointScript, consts.AgentReadyFifo,
		"fifo path %q missing from rendered entrypoint", consts.AgentReadyFifo)
	assert.Contains(t, EntrypointScript, consts.ReadyMarkerPath,
		"ready-marker path %q missing from rendered entrypoint", consts.ReadyMarkerPath)
	assert.NotContains(t, EntrypointScript, "{{",
		"unrendered template placeholder leaked into EntrypointScript")
}

// TestEntrypoint_AllTemplatesRegistered enforces the renderedAssetByName
// convention mechanically: every assets/*.tmpl that's rendered from
// compile-time constants must be registered, or EmbeddedScripts() would
// hash the raw template bytes and a consts bump would not invalidate
// the image content hash.
//
// runtimeRendered lists templates whose render depends on caller data
// (e.g., Dockerfile.tmpl takes version/variant/OTEL per call). For
// these, the raw template bytes ARE the correct hash input — a fixed
// snapshot would lie about what the image contains.
func TestEntrypoint_AllTemplatesRegistered(t *testing.T) {
	runtimeRendered := map[string]bool{
		"Dockerfile.tmpl": true,
	}
	entries, err := fs.ReadDir(assetsFS, "assets")
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".tmpl") || runtimeRendered[name] {
			continue
		}
		_, ok := renderedAssetByName[name]
		assert.True(t, ok,
			"assets/%s is a compile-time template but has no entry in renderedAssetByName — its raw bytes would land in the content hash and consts bumps would not invalidate the image", name)
	}
}
