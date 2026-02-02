package commands

import (
	"os"
	"testing"

	"github.com/schmitthub/clawker/test/harness"
)

func TestMain(m *testing.M) {
	os.Exit(harness.RunTestMain(m))
}
