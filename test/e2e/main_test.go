package e2e_test

import (
	"os"
	"testing"

	"github.com/schmitthub/clawker/internal/clawkercmd"
)

// TestMain lets this test binary act as the clawker CLI. The host proxy
// manager starts its daemon by running its own executable with
// "host-proxy serve", and in e2e runs that executable is this binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "host-proxy" {
		os.Exit(clawkercmd.Main())
	}
	os.Exit(m.Run())
}
