package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
)

// UnitSourceBuiltIn labels a unit shipped by an embedded harness bundle.
const UnitSourceBuiltIn = "(built-in)"

// UnitsMarkerFile records the active unit set a bootstrap dir was rendered
// with, so `monitor up` can warn when the settings-side active set has
// drifted since the last `monitor init`.
const UnitsMarkerFile = ".clawker-units"

// ResolvedUnit is one monitoring unit in the host-global effective set:
// built-in units from shipped harness bundles plus settings-registered
// path entries.
type ResolvedUnit struct {
	Name string
	// Unit carries the manifest and artifact FS. Nil when LoadErr is set.
	Unit *bundler.MonitoringUnit
	// Source is UnitSourceBuiltIn + declaring harness for shipped units,
	// or the registered absolute path.
	Source string
	// Path is the registered directory ("" for built-in units).
	Path string
	// Active reports whether the unit is selected for seeding. Both
	// built-in and registered units default inactive — seeding is a
	// user-driven, explicit choice.
	Active bool
	// LoadErr is set when a registered entry's path did not load (moved,
	// deleted, or invalidated since registration). An inactive broken
	// entry is a `monitor list` marker; an active one is an error at
	// every consumption front door.
	LoadErr error
}

// Manifest returns the unit's manifest (zero value when unloaded).
func (r ResolvedUnit) Manifest() config.MonitoringUnitManifest {
	if r.Unit == nil {
		return config.MonitoringUnitManifest{}
	}
	return r.Unit.Manifest
}

// ResolveUnits builds the host-global unit set: shipped built-ins ∪
// settings monitoring.units registry, sorted by name. The namespace is
// flat — a registered entry carrying a path under a built-in unit's name
// is a hard error (the register front door refuses it; hitting this means
// the settings file was hand-edited). A path-less entry on a built-in
// name is the activation toggle. Registered entries that fail to load are
// returned with LoadErr set rather than failing the whole resolve, so
// `monitor list` can render the broken row; active-set consumers reject
// them via ActiveUnits/ValidateActiveSet.
func ResolveUnits(cfg config.Config) ([]ResolvedUnit, error) {
	shipped, err := bundler.ShippedMonitoringUnits()
	if err != nil {
		return nil, fmt.Errorf("monitor: shipped monitoring units: %w", err)
	}

	entries := cfg.Settings().Monitoring.Units
	units, builtIn, err := resolveBuiltInUnits(shipped, entries)
	if err != nil {
		return nil, err
	}
	registered, err := resolveRegisteredUnits(entries, builtIn)
	if err != nil {
		return nil, err
	}
	units = append(units, registered...)

	sort.Slice(units, func(i, j int) bool { return units[i].Name < units[j].Name })
	return units, nil
}

// resolveBuiltInUnits turns the shipped set into rows, applying flag-only
// settings entries as activation toggles.
func resolveBuiltInUnits(
	shipped []bundler.ShippedMonitoringUnit,
	entries map[string]config.MonitoringUnitEntry,
) ([]ResolvedUnit, map[string]bool, error) {
	units := make([]ResolvedUnit, 0, len(shipped))
	builtIn := map[string]bool{}
	for _, s := range shipped {
		entry := entries[s.Unit.Name]
		builtIn[s.Unit.Name] = true
		if entry.Path != "" {
			return nil, nil, fmt.Errorf(
				"monitor: settings entry monitoring.units.%s carries a path but %q is a built-in unit — "+
					"remove the entry or register the path under another name",
				s.Unit.Name, s.Unit.Name,
			)
		}
		units = append(units, ResolvedUnit{
			Name:    s.Unit.Name,
			Unit:    s.Unit,
			Source:  UnitSourceBuiltIn + " via harness " + s.Harness,
			Path:    "",
			Active:  entry.Active != nil && *entry.Active,
			LoadErr: nil,
		})
	}
	return units, builtIn, nil
}

// resolveRegisteredUnits loads the non-built-in settings entries; a
// failing path lands as LoadErr on the row rather than failing resolve.
func resolveRegisteredUnits(
	entries map[string]config.MonitoringUnitEntry,
	builtIn map[string]bool,
) ([]ResolvedUnit, error) {
	var units []ResolvedUnit
	for name, entry := range entries {
		if builtIn[name] {
			continue
		}
		if entry.Path == "" {
			return nil, fmt.Errorf(
				"monitor: settings entry monitoring.units.%s has no path and matches no built-in unit",
				name,
			)
		}
		r := ResolvedUnit{
			Name:    name,
			Unit:    nil,
			Source:  entry.Path,
			Path:    entry.Path,
			Active:  entry.Active != nil && *entry.Active,
			LoadErr: nil,
		}
		unit, loadErr := bundler.LoadMonitoringUnit(name, os.DirFS(entry.Path))
		if loadErr != nil {
			r.LoadErr = loadErr
		} else {
			r.Unit = unit
		}
		units = append(units, r)
	}
	return units, nil
}

// ActiveUnits resolves the active subset, failing on a broken active
// entry and on resource collisions inside the active set.
func ActiveUnits(cfg config.Config) ([]ResolvedUnit, error) {
	units, err := ResolveUnits(cfg)
	if err != nil {
		return nil, err
	}
	return ActiveFromResolved(units)
}

// ActiveFromResolved filters an already-resolved unit set down to the
// validated active subset — for callers that also need the full set
// (e.g. init's provenance output) without resolving twice.
func ActiveFromResolved(units []ResolvedUnit) ([]ResolvedUnit, error) {
	var active []ResolvedUnit
	for _, u := range units {
		if !u.Active {
			continue
		}
		if u.LoadErr != nil {
			return nil, fmt.Errorf(
				"monitor: active unit %q failed to load from %s: %w — fix the path or 'clawker monitor disable %s'",
				u.Name, u.Path, u.LoadErr, u.Name,
			)
		}
		active = append(active, u)
	}
	if err := ValidateActiveSet(active); err != nil {
		return nil, err
	}
	return active, nil
}

// ValidateActiveSet enforces resource exclusivity across an active set:
// no two active units may claim the same index or route the same
// service.name. Registration is additive and collision-free by
// construction (flat name map); the REAL collision surface is these
// runtime resources, checked at activation time — never silently
// double-routed.
func ValidateActiveSet(active []ResolvedUnit) error {
	indexOwner := map[string]string{}
	serviceOwner := map[string]string{}
	for _, u := range active {
		if err := claimUnitResources(u, indexOwner, serviceOwner); err != nil {
			return err
		}
	}
	return nil
}

// claimUnitResources records one unit's indices and service routes,
// erroring on a claim another active unit already holds.
func claimUnitResources(u ResolvedUnit, indexOwner, serviceOwner map[string]string) error {
	for _, lane := range u.Manifest().Logs {
		if owner, taken := indexOwner[lane.Index]; taken {
			return fmt.Errorf(
				"monitor: index %q claimed by active units %q and %q — disable one first",
				lane.Index, owner, u.Name,
			)
		}
		indexOwner[lane.Index] = u.Name
		for _, svc := range lane.ServiceNames {
			if owner, taken := serviceOwner[svc]; taken {
				return fmt.Errorf(
					"monitor: service name %q routed by active units %q and %q — disable one first",
					svc, owner, u.Name,
				)
			}
			serviceOwner[svc] = u.Name
		}
	}
	return nil
}

// DiscoverableUnit is a bundle-shipped unit visible from the current
// project's registered harness bundles but not yet in the host-global
// registry. Discovery is read-only and list-only — promotion to the
// registry is always an explicit `clawker monitor register`.
type DiscoverableUnit struct {
	Name    string
	Harness string
	Path    string // absolute unit dir, ready to hand to register
}

// DiscoverableUnits walks the current project's harnesses: registry,
// loads each registered bundle, and returns declared monitoring units
// whose names are neither built-in nor registered. A bundle that fails to
// load is skipped — discovery must not break `monitor list` over an
// unrelated project misconfiguration (the harness front doors own that
// reporting).
func DiscoverableUnits(cfg config.Config) []DiscoverableUnit {
	known := map[string]bool{}
	if units, err := ResolveUnits(cfg); err == nil {
		for _, u := range units {
			known[u.Name] = true
		}
	}

	var found []DiscoverableUnit
	for harness, hc := range cfg.Project().Harnesses {
		dir, ok := bundleDir(cfg, hc.Path)
		if !ok {
			continue
		}
		found = append(found, discoverBundleUnits(harness, dir, known)...)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found
}

// bundleDir resolves a project harness registry path to an absolute
// bundle dir; ok=false when unregistered or unresolvable.
func bundleDir(cfg config.Config, registered string) (string, bool) {
	if registered == "" {
		return "", false
	}
	if filepath.IsAbs(registered) {
		return registered, true
	}
	root := cfg.ProjectRoot()
	if root == "" {
		return "", false
	}
	return filepath.Join(root, registered), true
}

// discoverBundleUnits lists one registered bundle's declared units not
// already known to the registry.
func discoverBundleUnits(harness, dir string, known map[string]bool) []DiscoverableUnit {
	b, err := bundler.LoadBundle(harness, os.DirFS(dir))
	if err != nil {
		return nil
	}
	var found []DiscoverableUnit
	for _, unitName := range b.DeclaredMonitoringUnits() {
		if known[unitName] {
			continue
		}
		found = append(found, DiscoverableUnit{
			Name:    unitName,
			Harness: harness,
			Path:    filepath.Join(dir, bundler.MonitoringUnitsSubdir, unitName),
		})
	}
	return found
}

// UnitRouting is the precomputed collector-config data for one active
// unit: everything otel-config.yaml.tmpl ranges over. All identifiers are
// engine-derived so the template stays dumb.
type UnitRouting struct {
	Name  string
	Lanes []UnitLogLane
	// MetricRenameStatements are fully-built OTTL datapoint statements,
	// scoped to the unit's metric service names.
	MetricRenameStatements []string
}

// UnitLogLane is one unit log lane's collector wiring.
type UnitLogLane struct {
	Index        string
	PipelineName string // logs/unit_<sanitized-index>
	ExporterName string // opensearch/logs_unit_<sanitized-index>
	ServiceNames []string
}

// BuildUnitRoutings derives collector routing data from the active set.
// Pipeline/exporter identifiers sanitize the index name ([a-z0-9-] → '_'
// for '-'); a post-sanitization identifier collision (e.g. "a-b" vs
// "a_b") is a hard error rather than a silently merged pipeline.
func BuildUnitRoutings(active []ResolvedUnit) ([]UnitRouting, error) {
	idOwner := map[string]string{}
	routings := make([]UnitRouting, 0, len(active))
	for _, u := range active {
		m := u.Manifest()
		r := UnitRouting{Name: u.Name, Lanes: nil, MetricRenameStatements: nil}
		for _, lane := range m.Logs {
			id := sanitizeCollectorID(lane.Index)
			if owner, taken := idOwner[id]; taken {
				return nil, fmt.Errorf(
					"monitor: collector identifier %q for index %q collides with %s — rename the index",
					id, lane.Index, owner,
				)
			}
			idOwner[id] = fmt.Sprintf("unit %q index %q", u.Name, lane.Index)
			r.Lanes = append(r.Lanes, UnitLogLane{
				Index:        lane.Index,
				PipelineName: "logs/unit_" + id,
				ExporterName: "opensearch/logs_unit_" + id,
				ServiceNames: lane.ServiceNames,
			})
		}
		r.MetricRenameStatements = buildRenameStatements(m)
		routings = append(routings, r)
	}
	return routings, nil
}

// buildRenameStatements renders the unit's declarative datapoint renames
// as OTTL statements scoped to its metric service names (defaulting to
// the union of log-lane service names). Inputs are charset-validated at
// unit load, so interpolation cannot escape the string literals.
func buildRenameStatements(m config.MonitoringUnitManifest) []string {
	if m.Metrics == nil || len(m.Metrics.DatapointRenames) == 0 {
		return nil
	}
	services := m.Metrics.ServiceNames
	if len(services) == 0 {
		for _, lane := range m.Logs {
			services = append(services, lane.ServiceNames...)
		}
	}
	var stmts []string
	for _, rename := range m.Metrics.DatapointRenames {
		for _, svc := range services {
			scope := fmt.Sprintf(`resource.attributes["service.name"] == %q`, svc)
			stmts = append(stmts,
				fmt.Sprintf(`set(attributes[%q], attributes[%q]) where %s and attributes[%q] != nil`,
					rename.To, rename.From, scope, rename.From),
				fmt.Sprintf(`delete_key(attributes, %q) where %s and attributes[%q] != nil`,
					rename.From, scope, rename.From),
			)
		}
	}
	return stmts
}

// sanitizeCollectorID maps an index name into a collector component
// identifier segment.
func sanitizeCollectorID(index string) string {
	return strings.ReplaceAll(index, "-", "_")
}
