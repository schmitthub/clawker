// Package units implements the `clawker monitor register|remove|list|
// enable|disable` commands — the host-global monitoring unit registry
// front doors. A monitoring unit is a self-contained observability
// loadout (OpenSearch index + pipelines + dashboards + collector
// routing); registration writes settings.yaml, activation selects what
// `monitor init && monitor up` seeds.
package units

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/monitor"
)

// deriveName returns the default registry name for a unit directory: its
// base name. The dir name IS the unit name.
func deriveName(absDir string) string {
	return filepath.Base(absDir)
}

// pathKey returns the dotted settings.yaml path where a unit's registered
// path is written: monitoring.units.<name>.path.
func pathKey(name string) string {
	return "monitoring.units." + name + ".path"
}

// activeKey returns the dotted settings.yaml path of a unit's activation
// flag: monitoring.units.<name>.active.
func activeKey(name string) string {
	return "monitoring.units." + name + ".active"
}

// entryKey returns the dotted settings.yaml path of the whole unit entry:
// monitoring.units.<name>.
func entryKey(name string) string {
	return "monitoring.units." + name
}

// builtInUnitNames lists the units shipped inside embedded harness
// bundles — the registry's flat namespace reserves these names.
func builtInUnitNames() ([]string, error) {
	shipped, err := bundler.ShippedMonitoringUnits()
	if err != nil {
		return nil, fmt.Errorf("shipped monitoring units: %w", err)
	}
	names := make([]string, 0, len(shipped))
	for _, s := range shipped {
		names = append(names, s.Unit.Name)
	}
	return names, nil
}

// isBuiltInUnit reports whether name is shipped by an embedded bundle.
func isBuiltInUnit(name string) (bool, error) {
	names, err := builtInUnitNames()
	if err != nil {
		return false, err
	}
	return slices.Contains(names, name), nil
}

// findUnit returns the resolved unit with the given name, if present.
func findUnit(units []monitor.ResolvedUnit, name string) (monitor.ResolvedUnit, bool) {
	for _, u := range units {
		if u.Name == name {
			return u, true
		}
	}
	return monitor.ResolvedUnit{}, false
}

// printApplyRecipe prints the re-seed instructions every activation
// change ends with — enable/disable only mutate settings; applying the
// change to the running stack is always an explicit compose lifecycle
// action (CLI-owns-compose-lifecycle).
func printApplyRecipe(ios *iostreams.IOStreams) {
	fmt.Fprintln(ios.Out, "  Run 'clawker monitor init && clawker monitor up' to apply")
}
