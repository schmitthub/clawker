package monitor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/config"
)

// ResolvedUnit is one monitoring extension the current project selects
// (`monitor.extensions`), resolved through the one three-tier algorithm in
// internal/bundle (bare name → user loose > project loose > embedded floor;
// qualified namespace.bundle.component → installed/in-place bundle). Every
// selected extension is active by construction — there is no activation flag and
// no host-side registry; selection IS enablement.
type ResolvedUnit struct {
	// Name is the selection spelling: the bare name for a floor/loose extension,
	// the dotted namespace.bundle.component address for a bundled one. It is the
	// ledger key and the collision key.
	Name string
	// Qualified reports whether Name is a bundled (qualified) address. Bare
	// extensions can C5-clobber across projects; qualified ones are
	// collision-proof by construction.
	Qualified bool
	// Unit carries the loaded manifest and artifact filesystem.
	Unit *MonitoringUnit
	// Source is the resolved provenance source clause (`built-in`,
	// `project (…)`, `user (…)`, `bundle acme.tools`).
	Source string
	// ProjectRoot is the current project's root at resolve time ("" when no
	// project is anchored) — the C5 discriminator recorded in the ledger.
	ProjectRoot string
	// ContentHash is a stable digest of the unit's manifest + artifacts, used
	// to detect an identical re-seed (no-op) versus an edited unit (update).
	ContentHash string
}

// ResolveUnits projects the current project's `monitor.extensions` selection
// onto resolved monitoring units. Selection follows build.stacks semantics — the
// highest config layer that sets `monitor.extensions` wins wholesale (the
// defaults layer ships `[claude-code]`), so this reads the merged selection off
// the project view. Each selected name resolves through the internal/bundle
// three-tier resolver; a name that resolves nowhere, or a unit that fails its
// front-door validation, is a hard error — a selected extension that cannot load
// must never be silently dropped.
func ResolveUnits(cfg config.Config) ([]ResolvedUnit, error) {
	selected := cfg.Project().Monitor.Extensions
	resolver := bundle.NewResolver(cfg)
	projectRoot := cfg.ProjectRoot()

	seen := map[string]bool{}
	out := make([]ResolvedUnit, 0, len(selected))
	for _, name := range selected {
		if seen[name] {
			continue
		}
		seen[name] = true

		comp, err := resolver.Resolve(bundle.ComponentMonitoring, name)
		if err != nil {
			return nil, fmt.Errorf("monitor: resolve extension %q: %w", name, err)
		}
		unit, loadErr := LoadMonitoringUnit(comp.Address.Name, comp.FS)
		if loadErr != nil {
			return nil, fmt.Errorf("monitor: extension %q: %w", name, loadErr)
		}
		hash, hashErr := hashUnit(unit)
		if hashErr != nil {
			return nil, hashErr
		}
		out = append(out, ResolvedUnit{
			Name:        comp.Address.String(),
			Qualified:   comp.Address.Qualified(),
			Unit:        unit,
			Source:      comp.Provenance.Source(),
			ProjectRoot: projectRoot,
			ContentHash: hash,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ValidateSeededSet enforces resource exclusivity across a seeded unit set: no
// two units may claim the same index or route the same service.name. Selection
// is collision-free by construction at the addressing layer; the REAL collision
// surface is these runtime OpenSearch resources, checked over the ledger union
// so a foreign project's already-seeded routing can't be silently double-claimed
// by this project's up.
func ValidateSeededSet(units []SeededUnit) error {
	indexOwner := map[string]string{}
	serviceOwner := map[string]string{}
	for _, u := range units {
		if err := claimUnitResources(u, indexOwner, serviceOwner); err != nil {
			return err
		}
	}
	return nil
}

// claimUnitResources records one unit's indices and service routes, erroring on
// a claim another unit already holds.
func claimUnitResources(u SeededUnit, indexOwner, serviceOwner map[string]string) error {
	for _, lane := range u.Manifest.Logs {
		if owner, taken := indexOwner[lane.Index]; taken {
			return fmt.Errorf(
				"monitor: index %q claimed by units %q and %q — deselect one from monitor.extensions",
				lane.Index, owner, u.Name,
			)
		}
		indexOwner[lane.Index] = u.Name
		for _, svc := range lane.ServiceNames {
			if owner, taken := serviceOwner[svc]; taken {
				return fmt.Errorf(
					"monitor: service name %q routed by units %q and %q — deselect one from monitor.extensions",
					svc, owner, u.Name,
				)
			}
			serviceOwner[svc] = u.Name
		}
	}
	return nil
}

// UnitRouting is the precomputed collector-config data for one seeded unit:
// everything otel-config.yaml.tmpl ranges over. All identifiers are
// engine-derived so the template stays dumb.
type UnitRouting struct {
	Name  string
	Lanes []UnitLogLane
	// MetricRenameStatements are fully-built OTTL datapoint statements, scoped to
	// the unit's metric service names.
	MetricRenameStatements []string
}

// UnitLogLane is one unit log lane's collector wiring.
type UnitLogLane struct {
	Index        string
	PipelineName string // logs/unit_<sanitized-index>
	ExporterName string // opensearch/logs_unit_<sanitized-index>
	ServiceNames []string
}

// BuildUnitRoutings derives collector routing data from the seeded union.
// Pipeline/exporter identifiers sanitize the index name ([a-z0-9-] → '_' for
// '-'); a post-sanitization identifier collision (e.g. "a-b" vs "a_b") is a hard
// error rather than a silently merged pipeline.
func BuildUnitRoutings(units []SeededUnit) ([]UnitRouting, error) {
	idOwner := map[string]string{}
	routings := make([]UnitRouting, 0, len(units))
	for _, u := range units {
		r := UnitRouting{Name: u.Name, Lanes: nil, MetricRenameStatements: nil}
		for _, lane := range u.Manifest.Logs {
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
		r.MetricRenameStatements = buildRenameStatements(u.Manifest)
		routings = append(routings, r)
	}
	return routings, nil
}

// buildRenameStatements renders the unit's declarative datapoint renames as OTTL
// statements scoped to its metric service names (defaulting to the union of
// log-lane service names). Inputs are charset-validated at unit load, so
// interpolation cannot escape the string literals.
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

// sanitizeCollectorID maps an index name into a collector component identifier
// segment.
func sanitizeCollectorID(index string) string {
	return strings.ReplaceAll(index, "-", "_")
}
