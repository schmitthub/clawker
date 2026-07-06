package docker

import (
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/pkg/whail"
)

// TestLabelConfig returns a LabelConfig that adds the test label
// to all resource types. Use with WithLabels in test code to ensure
// CleanupTestResources can find and remove test-created resources.
//
// When testName is provided (typically t.Name()), the test name label
// is also set, enabling per-test resource debugging:
//
//	docker ps -a --filter label=dev.clawker.test.name=TestMyFunction
//	docker volume ls --filter label=dev.clawker.test.name=TestMyFunction
func TestLabelConfig(cfg config.Config, testName ...string) whail.LabelConfig {
	labels := map[string]string{
		cfg.LabelTest(): cfg.ManagedLabelValue(),
	}
	if len(testName) > 0 && testName[0] != "" {
		labels[cfg.LabelTestName()] = testName[0]
	}
	return whail.LabelConfig{Default: labels}
}
