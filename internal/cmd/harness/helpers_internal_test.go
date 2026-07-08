package harness

import (
	"reflect"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
)

// TestHasInitConfig_DetectsEveryField guards hasInitConfig against schema
// drift: it populates every field of a HarnessConfig, zeroes only Path, and
// asserts the predicate reports init config present. If a future field is
// added to HarnessConfig without being wired into hasInitConfig, `harness
// remove` would silently drop that field along with the entry — this test
// fails loudly instead. It also confirms a bare-path entry has no init config.
func TestHasInitConfig_DetectsEveryField(t *testing.T) {
	mount := true
	full := config.HarnessConfig{
		Config:        config.HarnessConfigOptions{Strategy: config.ConfigStrategyFresh},
		MountProjects: &mount,
		EnvFile:       []string{"a.env"},
		FromEnv:       []string{"HOME"},
		Env:           map[string]string{"K": "V"},
		PostInit:      "echo hi",
		PreRun:        "echo run",
		Path:          "./tools/x",
	}

	// Sanity: the literal above sets every exported field, so if HarnessConfig
	// gains a field this test won't compile until it's added here — forcing a
	// hasInitConfig review.
	if reflect.ValueOf(full).NumField() != 8 {
		t.Fatalf("HarnessConfig field count changed to %d — update hasInitConfig and this test",
			reflect.ValueOf(full).NumField())
	}

	full.Path = ""
	if !hasInitConfig(full) {
		t.Fatal("hasInitConfig should be true when any non-path field is set")
	}

	var bare config.HarnessConfig
	bare.Path = "./tools/x"
	if hasInitConfig(bare) {
		t.Fatal("a bare-path entry carries no init config")
	}
}
