// Package componentcheck loads enumerated bundle components through their
// consumption-time front doors — the same loaders `clawker build` and
// `clawker monitor up` use. It exists as a separate package because those
// loaders live in packages that import internal/bundle, so internal/bundle
// cannot call them itself. It is the production bundle.ComponentValidator,
// wired into every bundle.Manager at construction.
package componentcheck

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/monitor"
)

// Validate loads one enumerated component through its consumption-time
// loader, returning the error the consuming command would surface.
func Validate(c bundle.Component) error {
	var err error
	switch c.Type {
	case bundle.ComponentHarness:
		_, err = bundler.LoadBundle(c.Address.Name, c.FS)
	case bundle.ComponentStack:
		_, err = bundler.LoadStackDefinition(c.Address.Name, c.FS)
	case bundle.ComponentMonitoring:
		_, err = monitor.LoadMonitoringUnit(c.Address.Name, c.FS)
	default:
		return fmt.Errorf("component %s: unknown component type", c.Address.Name)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", c.Dir, err)
	}
	return nil
}
