package monitor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
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
	// the dotted namespace.bundle.component address for a bundled one. Together
	// with SourceKey it forms the ledger identity (see ledgerKey).
	Name string
	// Qualified reports whether Name is a bundled (qualified) address. A bare
	// name is one cluster-wide namespace — different-content divergence across
	// projects is refused at seed; a qualified address is value-keyed, so
	// different pins seed sibling ledger entries and coexist unless their
	// runtime resources genuinely conflict (ValidateSeededSet).
	Qualified bool
	// Unit carries the loaded manifest and artifact filesystem.
	Unit *MonitoringUnit
	// Source is the resolved provenance source clause (`built-in`,
	// `project (…)`, `user (…)`, `bundle acme.tools`).
	Source string
	// SourceKey is the stable identity of the resolved content source — the
	// ledger's ownership discriminator. Host-global sources (the embedded
	// floor, the shared user dir) yield the same key from every project, so a
	// content change there is an in-place update; project-owned and
	// value-keyed bundle sources yield distinct keys per owner. See
	// sourceScopeKey.
	SourceKey string
	// ProjectRoot is the current project's root at resolve time ("" when no
	// project is anchored) — recorded in the ledger as seed provenance and
	// used to retire a project's own stale qualified pins.
	ProjectRoot string
	// ContentHash is a stable digest of the unit's manifest + artifacts, used
	// to detect an identical re-seed (no-op) versus an edited unit (update).
	ContentHash string
	// ClusterObjects are the cluster-scoped OpenSearch object names this
	// unit's artifacts PUT (pipelines, component templates, ISM policies,
	// datasources, saved-object ids), each with a content digest — snapshotted
	// into the ledger so cross-project name reuse with different content is
	// refused instead of silently rewriting cluster behavior.
	ClusterObjects []ClusterObject
}

// Source-scope key vocabulary: the tier-derived prefix identifying who owns a
// resolved unit's content. Floor content lives in the CLI binary (one source
// per host, no directory discriminator); the other scopes append the resolved
// directory, which for installed bundles embeds the declared value key.
const (
	sourceScopeFloor   = "floor"
	sourceScopeUser    = "user"
	sourceScopeProject = "project"
	sourceScopeBundle  = "bundle"
	sourceScopeSep     = ":"
)

// sourceScopeKey derives the ledger ownership key for a resolved component's
// provenance. Same key = same content source (its content evolving is an
// update); different keys = different owners (bare-name divergence is a
// collision; qualified divergence is sibling coexistence).
func sourceScopeKey(p bundle.Provenance) string {
	switch p.Tier {
	case bundle.TierFloor:
		return sourceScopeFloor
	case bundle.TierLooseUser:
		return sourceScopeUser + sourceScopeSep + p.Dir
	case bundle.TierLooseProject:
		return sourceScopeProject + sourceScopeSep + p.Dir
	case bundle.TierInstalled, bundle.TierInPlace:
		// The installed dir embeds the declared value key, so two projects
		// pinning one repository differently are distinct sources while the
		// same declared value is one shared source.
		return sourceScopeBundle + sourceScopeSep + p.Dir
	default:
		return p.Tier.Label() + sourceScopeSep + p.Dir
	}
}

// ClusterObject is one cluster-scoped OpenSearch object a unit's artifacts
// apply: its PUT-target kind and id plus a digest of the defining content.
// Claims are validated across the seeded union — the same (kind, id) from two
// units is a harmless idempotent PUT when the digests match and a refused
// last-write-wins overwrite when they differ.
type ClusterObject struct {
	Kind   string `yaml:"kind"`
	ID     string `yaml:"id"`
	Digest string `yaml:"digest"`
}

// Cluster-object kinds, mirroring the bootstrap script's PUT surfaces.
// Saved objects — whether shipped as an ndjson import line or as an explore
// panel file — are ONE kind: bootstrap writes both into the same Dashboards
// saved-object store (ndjson `_import?overwrite=true`; panels as
// `POST …/saved_objects/explore/<basename>?overwrite=true`), so their claims
// must share a namespace or a cross-representation same-id overwrite would
// pass undetected.
const (
	ClusterObjectIndexTemplate     = "index-template"
	ClusterObjectIngestPipeline    = "ingest-pipeline"
	ClusterObjectComponentTemplate = "component-template"
	ClusterObjectISMPolicy         = "ism-policy"
	ClusterObjectDatasource        = "datasource"
	ClusterObjectSavedObject       = "saved-object"
)

// savedObjectTypeExplore is the Dashboards saved-object TYPE explore panels
// are written under. It happens to match the panel dir name
// (MonitoringDirExplore) because the dir is named for it, but it is a wire
// value, not a path segment — keep them distinct.
const savedObjectTypeExplore = "explore"

// savedObjectID spells a saved-object claim id as type/id — one spelling for
// both on-disk representations.
func savedObjectID(typ, id string) string {
	return typ + "/" + id
}

// jsonExt/ndjsonExt are the artifact extensions the collector strips/matches.
const (
	jsonExt   = ".json"
	ndjsonExt = ".ndjson"
)

// contentDigest is the stable digest recorded on a ClusterObject.
func contentDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// clusterObjects walks a unit's artifacts and collects every cluster-scoped
// object claim: JSON basenames in the PUT-target dirs, each ndjson line's
// (type, id), and explore panel filenames — claimed as saved objects of type
// explore, the same namespace an ndjson line with that type lands in.
func clusterObjects(u *MonitoringUnit) ([]ClusterObject, error) {
	var out []ClusterObject
	exploreDir := path.Join(MonitoringDirSavedObjects, MonitoringDirExplore)
	kindByDir := map[string]string{
		MonitoringDirIndexTemplates:     ClusterObjectIndexTemplate,
		MonitoringDirIngestPipelines:    ClusterObjectIngestPipeline,
		MonitoringDirComponentTemplates: ClusterObjectComponentTemplate,
		MonitoringDirISMPolicies:        ClusterObjectISMPolicy,
		MonitoringDirDatasources:        ClusterObjectDatasource,
	}
	err := u.WalkArtifacts(func(relPath string, content []byte) error {
		dir := path.Dir(relPath)
		base := path.Base(relPath)
		switch {
		case dir == exploreDir && path.Ext(base) == jsonExt:
			out = append(out, ClusterObject{
				Kind:   ClusterObjectSavedObject,
				ID:     savedObjectID(savedObjectTypeExplore, strings.TrimSuffix(base, jsonExt)),
				Digest: contentDigest(content),
			})
		case dir == MonitoringDirSavedObjects && path.Ext(base) == ndjsonExt:
			objs, err := savedObjectClaims(relPath, content)
			if err != nil {
				return err
			}
			out = append(out, objs...)
		default:
			if kind, known := kindByDir[dir]; known && path.Ext(base) == jsonExt {
				out = append(out, ClusterObject{
					Kind:   kind,
					ID:     strings.TrimSuffix(base, jsonExt),
					Digest: contentDigest(content),
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("monitoring unit %q: collect cluster objects: %w", u.Name, err)
	}
	return out, nil
}

// savedObjectClaims parses one ndjson import file into per-line saved-object
// claims. Dashboards `_import` runs with overwrite=true, so every (type, id)
// is a cluster-scoped write target.
func savedObjectClaims(relPath string, content []byte) ([]ClusterObject, error) {
	var out []ClusterObject
	for line := range strings.SplitSeq(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return nil, fmt.Errorf("%s: malformed ndjson line: %w", relPath, err)
		}
		out = append(out, ClusterObject{
			Kind:   ClusterObjectSavedObject,
			ID:     savedObjectID(obj.Type, obj.ID),
			Digest: contentDigest([]byte(line)),
		})
	}
	return out, nil
}

// ResolveUnits projects the current project's `monitor.extensions` selection
// onto resolved monitoring units. Selection follows build.stacks semantics — the
// highest config layer that sets `monitor.extensions` wins wholesale (the
// virtual defaults layer ships the claude-code extension; an explicit empty
// list opts out of all monitoring), so this reads the
// merged selection off the project view. Each selected name resolves through the internal/bundle
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
		objects, objErr := clusterObjects(unit)
		if objErr != nil {
			return nil, objErr
		}
		out = append(out, ResolvedUnit{
			Name:           comp.Address.String(),
			Qualified:      comp.Address.Qualified(),
			Unit:           unit,
			Source:         comp.Provenance.Source(),
			SourceKey:      sourceScopeKey(comp.Provenance),
			ProjectRoot:    projectRoot,
			ContentHash:    hash,
			ClusterObjects: objects,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ValidateSeededSet enforces resource exclusivity across a seeded unit set: no
// two units may claim the same index, route the same service.name, or PUT
// different content under one cluster-scoped object name. Selection is
// collision-free by construction at the addressing layer; the REAL collision
// surface is these runtime OpenSearch resources, checked over the ledger union
// so a foreign project's already-seeded state can't be silently double-claimed
// or rewritten by this project's up. Sibling pins of one qualified address may
// share a resource when they define it identically — an idempotent re-apply,
// not a conflict.
func ValidateSeededSet(units []SeededUnit) error {
	indexOwner := map[string]indexClaim{}
	serviceOwner := map[string]serviceClaim{}
	objectOwner := map[clusterObjectKey]clusterObjectClaim{}
	for _, u := range units {
		if err := claimUnitResources(u, indexOwner, serviceOwner); err != nil {
			return err
		}
		if err := claimClusterObjects(u, objectOwner); err != nil {
			return err
		}
	}
	return nil
}

// indexClaim records who owns an index and how its lane is defined, so an
// identical re-declaration by a sibling pin of the same unit is shareable.
type indexClaim struct {
	unit  string // SeededUnit.Name
	lane  string // laneFingerprint of the defining lane
	label string
}

// serviceClaim records who routes a service name and to which index.
type serviceClaim struct {
	unit  string
	index string
	label string
}

type clusterObjectKey struct{ kind, id string }

// clusterObjectClaim records who ships a cluster-scoped object and its content
// digest — identical content is a harmless idempotent PUT. The owning entry is
// the seeded identity (name + source key), NOT the display label: sibling pins
// of one address share a label but are distinct owners.
type clusterObjectClaim struct {
	entry  string
	unit   string
	digest string
	label  string
}

// seededEntryID is a seeded unit's ledger identity — distinct per sibling pin.
func seededEntryID(u SeededUnit) string {
	return u.Name + "\x00" + u.SourceKey
}

// seededUnitLabel names a seeded unit with its provenance so two sibling pins
// of one address are distinguishable in errors.
func seededUnitLabel(u SeededUnit) string {
	return fmt.Sprintf("%q (%s)", u.Name, u.Source)
}

// laneFingerprint canonically encodes a lane's routing definition (service set
// + retention) for identity comparison across sibling pins.
func laneFingerprint(lane config.MonitoringLogLane) string {
	services := append([]string(nil), lane.ServiceNames...)
	sort.Strings(services)
	return lane.Retention + "|" + strings.Join(services, ",")
}

// claimUnitResources records one unit's indices and service routes, erroring on
// a claim another unit already holds — unless the holder is a sibling pin of
// the same unit with an identical definition.
func claimUnitResources(u SeededUnit, indexOwner map[string]indexClaim, serviceOwner map[string]serviceClaim) error {
	label := seededUnitLabel(u)
	for _, lane := range u.Manifest.Logs {
		if err := claimLaneIndex(u.Name, label, lane, indexOwner); err != nil {
			return err
		}
		if err := claimLaneServices(u.Name, label, lane, serviceOwner); err != nil {
			return err
		}
	}
	return nil
}

// claimLaneIndex records one lane's index claim.
func claimLaneIndex(unit, label string, lane config.MonitoringLogLane, indexOwner map[string]indexClaim) error {
	fp := laneFingerprint(lane)
	owner, taken := indexOwner[lane.Index]
	if !taken {
		indexOwner[lane.Index] = indexClaim{unit: unit, lane: fp, label: label}
		return nil
	}
	if owner.unit != unit {
		return fmt.Errorf(
			"monitor: index %q claimed by units %s and %s — deselect one from monitor.extensions",
			lane.Index, owner.label, label,
		)
	}
	if owner.lane != fp {
		return fmt.Errorf(
			"monitor: index %q is declared with different lane definitions by two seeded pins of unit %q — "+
				"align the projects' pinned versions, or deselect the extension in one project",
			lane.Index, unit,
		)
	}
	return nil
}

// claimLaneServices records one lane's service-name claims.
func claimLaneServices(unit, label string, lane config.MonitoringLogLane, serviceOwner map[string]serviceClaim) error {
	for _, svc := range lane.ServiceNames {
		owner, taken := serviceOwner[svc]
		if !taken {
			serviceOwner[svc] = serviceClaim{unit: unit, index: lane.Index, label: label}
			continue
		}
		if owner.unit != unit {
			return fmt.Errorf(
				"monitor: service name %q routed by units %s and %s — deselect one from monitor.extensions",
				svc, owner.label, label,
			)
		}
		if owner.index != lane.Index {
			return fmt.Errorf(
				"monitor: service name %q is routed to different indices (%q and %q) by two seeded pins of unit %q — "+
					"align the projects' pinned versions, or deselect the extension in one project",
				svc, owner.index, lane.Index, unit,
			)
		}
	}
	return nil
}

// claimClusterObjects records one unit's cluster-scoped object names, erroring
// when another unit already ships the same name with DIFFERENT content —
// cluster PUTs are last-write-wins, so an undetected reuse would silently
// rewrite the earlier unit's behavior (e.g. another bundle's same-named ingest
// pipeline replacing its Painless script cluster-wide).
func claimClusterObjects(u SeededUnit, objectOwner map[clusterObjectKey]clusterObjectClaim) error {
	label := seededUnitLabel(u)
	entry := seededEntryID(u)
	for _, obj := range u.ClusterObjects {
		key := clusterObjectKey{kind: obj.Kind, id: obj.ID}
		owner, taken := objectOwner[key]
		if !taken {
			objectOwner[key] = clusterObjectClaim{entry: entry, unit: u.Name, digest: obj.Digest, label: label}
			continue
		}
		switch {
		case owner.digest == obj.Digest:
			// Identical content: an idempotent PUT, shareable by anyone.
		case owner.entry == entry:
			// One entry re-listing its own id (its per-render collision checks
			// govern within-unit shape) — not a cross-unit conflict.
		case owner.unit == u.Name:
			return fmt.Errorf(
				"monitor: %s %q is shipped with different content by two seeded pins of unit %q — "+
					"the name is a cluster-wide write target (last write wins); "+
					"align the projects' pinned versions, or deselect the extension in one project",
				obj.Kind, obj.ID, u.Name,
			)
		default:
			return fmt.Errorf(
				"monitor: %s %q shipped with different content by units %s and %s — "+
					"the name is a cluster-wide write target (last write wins), so one unit would silently rewrite the other's; "+
					"rename the object in one unit, or deselect one extension from monitor.extensions",
				obj.Kind, obj.ID, owner.label, label,
			)
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
// error rather than a silently merged pipeline. A lane declared identically by
// two seeded pins of one unit is emitted once — sibling pins share unchanged
// lanes, and duplicate YAML component keys would corrupt the rendered config.
func BuildUnitRoutings(units []SeededUnit) ([]UnitRouting, error) {
	idOwner := map[string]laneClaim{}
	seenStatements := map[string]bool{}
	routings := make([]UnitRouting, 0, len(units))
	for _, u := range units {
		lanes, err := buildUnitLanes(u, idOwner)
		if err != nil {
			return nil, err
		}
		r := UnitRouting{Name: u.Name, Lanes: lanes, MetricRenameStatements: nil}
		// Identical rename statements from a sibling pin are emitted once;
		// distinct statements can never alias across units because they embed
		// service names, which are unit-exclusive.
		for _, stmt := range buildRenameStatements(u.Manifest) {
			if seenStatements[stmt] {
				continue
			}
			seenStatements[stmt] = true
			r.MetricRenameStatements = append(r.MetricRenameStatements, stmt)
		}
		if len(r.Lanes) == 0 && len(r.MetricRenameStatements) == 0 {
			continue // a sibling pin whose whole projection is already emitted
		}
		routings = append(routings, r)
	}
	return routings, nil
}

// buildUnitLanes derives one unit's collector lanes, deduping identical
// sibling-pin lanes and erroring on identifier collisions.
func buildUnitLanes(u SeededUnit, idOwner map[string]laneClaim) ([]UnitLogLane, error) {
	var lanes []UnitLogLane
	for _, lane := range u.Manifest.Logs {
		id := sanitizeCollectorID(lane.Index)
		fp := laneFingerprint(lane)
		if owner, taken := idOwner[id]; taken {
			if owner.unit == u.Name && owner.index == lane.Index && owner.lane == fp {
				continue // identical lane from a sibling pin — already emitted
			}
			return nil, fmt.Errorf(
				"monitor: collector identifier %q for index %q collides with unit %q index %q — rename the index",
				id, lane.Index, owner.unit, owner.index,
			)
		}
		idOwner[id] = laneClaim{unit: u.Name, index: lane.Index, lane: fp}
		lanes = append(lanes, UnitLogLane{
			Index:        lane.Index,
			PipelineName: "logs/unit_" + id,
			ExporterName: "opensearch/logs_unit_" + id,
			ServiceNames: lane.ServiceNames,
		})
	}
	return lanes, nil
}

// laneClaim records which unit and lane definition own a sanitized collector
// identifier, so identical sibling-pin lanes dedupe and everything else errors.
type laneClaim struct {
	unit  string
	index string
	lane  string
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
