package bundler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
)

// MonitoringUnitManifestFile is the manifest filename inside a monitoring
// unit directory.
const MonitoringUnitManifestFile = "monitoring.yaml"

// MonitoringUnitsSubdir is the subdirectory of a harness bundle holding
// bundle-shipped monitoring units.
const MonitoringUnitsSubdir = "monitoring"

// Monitoring unit artifact subdirectories. They mirror the
// opensearch-bootstrap tree so materialization is a plain overlay copy and
// the bootstrap script's directory loops apply unit artifacts unmodified.
const (
	MonitoringDirIndexTemplates     = "index-templates"
	MonitoringDirIngestPipelines    = "ingest-pipelines"
	MonitoringDirComponentTemplates = "component-templates"
	MonitoringDirISMPolicies        = "ism-policies"
	MonitoringDirSavedObjects       = "saved-objects"
	MonitoringDirExplore            = "explore" // under saved-objects/
	MonitoringDirDatasources        = "datasources"
)

// indexNameRe is the OpenSearch index-name grammar a unit lane may
// declare: lowercase letters, digits, and internal hyphens. Deliberately a
// subset of what OpenSearch accepts — the same quote/backslash-free
// charset makes OTTL routing-condition injection unspellable by
// construction. Service names share the rule for the same reason.
var indexNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// metricAttrRe is the grammar for datapoint-rename attribute keys.
var metricAttrRe = regexp.MustCompile(`^[a-zA-Z0-9_.]+$`)

// MonitoringUnit is a loaded monitoring unit: manifest plus a handle to
// the unit directory for reading artifact files.
type MonitoringUnit struct {
	// Name is the registry/lookup key (dir name for bundle-shipped units).
	Name string
	// Manifest is the parsed monitoring.yaml.
	Manifest config.MonitoringUnitManifest

	fsys fs.FS
}

// ValidateMonitoringUnitName rejects names that cannot serve as monitoring
// registry keys. Delegates to the unified naming rule shared by stacks,
// harnesses, and their registry keys (see consts.ValidateName).
func ValidateMonitoringUnitName(name string) error {
	if err := consts.ValidateName(name); err != nil {
		return fmt.Errorf("monitoring unit %w", err)
	}
	return nil
}

// LoadMonitoringUnit reads a unit from fsys, whose root must be the unit
// directory (monitoring.yaml plus artifact subdirs). Use [os.DirFS] for
// registered on-disk units and a sub-FS of embedded assets for
// bundle-shipped ones. Every validation failure is a named error at this
// front door — never a silent bootstrap-time skip.
func LoadMonitoringUnit(name string, fsys fs.FS) (*MonitoringUnit, error) {
	if err := ValidateMonitoringUnitName(name); err != nil {
		return nil, err
	}

	rawManifest, err := fs.ReadFile(fsys, MonitoringUnitManifestFile)
	if err != nil {
		return nil, fmt.Errorf("monitoring unit %q: read %s: %w", name, MonitoringUnitManifestFile, err)
	}
	var m config.MonitoringUnitManifest
	if unmarshalErr := yaml.Unmarshal(rawManifest, &m); unmarshalErr != nil {
		return nil, fmt.Errorf("monitoring unit %q: parse %s: %w", name, MonitoringUnitManifestFile, unmarshalErr)
	}

	if lanesErr := validateUnitLanes(name, m.Logs); lanesErr != nil {
		return nil, lanesErr
	}
	if metricsErr := validateUnitMetrics(name, m.Metrics); metricsErr != nil {
		return nil, metricsErr
	}
	if treeErr := validateUnitTree(name, fsys, m); treeErr != nil {
		return nil, treeErr
	}

	return &MonitoringUnit{Name: name, Manifest: m, fsys: fsys}, nil
}

// validateUnitLanes checks the manifest's log lanes: at least one lane, and
// per lane a well-formed unit-owned non-reserved index, at least one
// well-formed non-reserved service name, and a known retention token —
// with indices and service names each unique across the unit.
func validateUnitLanes(name string, lanes []config.MonitoringLogLane) error {
	if len(lanes) == 0 {
		return fmt.Errorf("monitoring unit %q: logs must declare at least one lane", name)
	}
	seenIndex := map[string]bool{}
	seenService := map[string]bool{}
	for _, lane := range lanes {
		if err := validateUnitLane(name, lane, seenIndex, seenService); err != nil {
			return err
		}
	}
	return nil
}

// validateUnitLane checks one lane, recording its index and service names
// in the unit-wide seen sets.
func validateUnitLane(name string, lane config.MonitoringLogLane, seenIndex, seenService map[string]bool) error {
	if err := validateUnitIndexName(name, lane.Index); err != nil {
		return err
	}
	if seenIndex[lane.Index] {
		return fmt.Errorf("monitoring unit %q: duplicate index %q", name, lane.Index)
	}
	seenIndex[lane.Index] = true

	if len(lane.ServiceNames) == 0 {
		return fmt.Errorf(
			"monitoring unit %q: lane %q: service_names must declare at least one value — "+
				"a lane with no route can never receive records",
			name, lane.Index,
		)
	}
	for _, svc := range lane.ServiceNames {
		if err := validateUnitServiceName(name, svc); err != nil {
			return err
		}
		if seenService[svc] {
			return fmt.Errorf("monitoring unit %q: duplicate service name %q", name, svc)
		}
		seenService[svc] = true
	}

	switch lane.Retention {
	case "", config.MonitoringRetentionDefault, config.MonitoringRetentionCustom:
	default:
		return fmt.Errorf(
			"monitoring unit %q: lane %q: unknown retention %q (want %q or %q)",
			name, lane.Index, lane.Retention,
			config.MonitoringRetentionDefault, config.MonitoringRetentionCustom,
		)
	}
	return nil
}

func validateUnitIndexName(name, index string) error {
	if !indexNameRe.MatchString(index) {
		return fmt.Errorf(
			"monitoring unit %q: index %q is invalid: must match %s",
			name, index, indexNameRe.String(),
		)
	}
	if slices.Contains(consts.ReservedMonitoringIndices(), index) {
		return fmt.Errorf("monitoring unit %q: index %q is reserved for clawker infra", name, index)
	}
	if index != name && !strings.HasPrefix(index, name+"-") {
		return fmt.Errorf(
			"monitoring unit %q: index %q must equal the unit name or be %q-prefixed",
			name, index, name+"-",
		)
	}
	return nil
}

func validateUnitServiceName(name, svc string) error {
	if !indexNameRe.MatchString(svc) {
		return fmt.Errorf(
			"monitoring unit %q: service name %q is invalid: must match %s",
			name, svc, indexNameRe.String(),
		)
	}
	if slices.Contains(consts.ReservedTelemetryServiceNames(), svc) {
		return fmt.Errorf(
			"monitoring unit %q: service name %q is reserved for clawker infra telemetry",
			name, svc,
		)
	}
	return nil
}

// validateUnitMetrics checks the optional metrics block: well-formed
// non-reserved service names and rename keys in the metric-attribute
// grammar.
func validateUnitMetrics(name string, m *config.MonitoringUnitMetrics) error {
	if m == nil {
		return nil
	}
	for _, svc := range m.ServiceNames {
		if err := validateUnitServiceName(name, svc); err != nil {
			return err
		}
	}
	for _, r := range m.DatapointRenames {
		for _, key := range []string{r.From, r.To} {
			if !metricAttrRe.MatchString(key) {
				return fmt.Errorf(
					"monitoring unit %q: datapoint rename key %q is invalid: must match %s",
					name, key, metricAttrRe.String(),
				)
			}
		}
	}
	return nil
}

// validateUnitTree cross-checks the unit directory against the manifest:
// every top-level entry is a known artifact dir, every declared lane ships
// its index template (basename == index name, index_patterns == [index]),
// pipeline/component-template basenames are unit-prefixed, ISM policy
// files appear exactly when a lane declares custom retention and are
// pattern-scoped to unit-owned indices, and saved-objects carries only
// ndjson plus the explore/ panel dir. A typo'd directory or extension must
// be a load error — the bootstrap script's directory loops would otherwise
// silently never apply the file.
func validateUnitTree(name string, fsys fs.FS, m config.MonitoringUnitManifest) error {
	if err := validateUnitTopLevel(name, fsys); err != nil {
		return err
	}
	if err := validateUnitIndexTemplates(name, fsys, m.Logs); err != nil {
		return err
	}
	for _, dir := range []string{MonitoringDirIngestPipelines, MonitoringDirComponentTemplates} {
		if err := validateUnitPrefixedJSONDir(name, fsys, dir); err != nil {
			return err
		}
	}
	if err := validateUnitISMPolicies(name, fsys, m.Logs); err != nil {
		return err
	}
	if err := validateUnitSavedObjects(name, fsys); err != nil {
		return err
	}
	return validateUnitJSONDir(name, fsys, MonitoringDirDatasources)
}

func knownUnitDirs() map[string]bool {
	return map[string]bool{
		MonitoringDirIndexTemplates:     true,
		MonitoringDirIngestPipelines:    true,
		MonitoringDirComponentTemplates: true,
		MonitoringDirISMPolicies:        true,
		MonitoringDirSavedObjects:       true,
		MonitoringDirDatasources:        true,
	}
}

func validateUnitTopLevel(name string, fsys fs.FS) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("monitoring unit %q: read unit dir: %w", name, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			if !knownUnitDirs()[e.Name()] {
				return fmt.Errorf(
					"monitoring unit %q: unknown directory %q — bootstrap would silently ignore it",
					name, e.Name(),
				)
			}
			continue
		}
		if e.Name() != MonitoringUnitManifestFile {
			return fmt.Errorf(
				"monitoring unit %q: unknown top-level file %q — bootstrap would silently ignore it",
				name, e.Name(),
			)
		}
	}
	return nil
}

// validateUnitIndexTemplates requires index-templates/<index>.json per
// declared lane, with the template's index_patterns exactly [<index>] —
// the bootstrap pre-create loop derives index names from these basenames.
func validateUnitIndexTemplates(name string, fsys fs.FS, lanes []config.MonitoringLogLane) error {
	declared := map[string]bool{}
	for _, lane := range lanes {
		declared[lane.Index] = true
		file := path.Join(MonitoringDirIndexTemplates, lane.Index+".json")
		raw, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf(
				"monitoring unit %q: lane %q: read %s: %w — every declared lane ships its index template",
				name, lane.Index, file, err,
			)
		}
		var tpl struct {
			IndexPatterns []string `json:"index_patterns"` //nolint:tagliatelle // OpenSearch wire format
		}
		if jsonErr := json.Unmarshal(raw, &tpl); jsonErr != nil {
			return fmt.Errorf("monitoring unit %q: parse %s: %w", name, file, jsonErr)
		}
		if len(tpl.IndexPatterns) != 1 || tpl.IndexPatterns[0] != lane.Index {
			return fmt.Errorf(
				"monitoring unit %q: %s: index_patterns must be exactly [%q] (got %v) — "+
					"the bootstrap pre-create loop derives index names from template basenames",
				name, file, lane.Index, tpl.IndexPatterns,
			)
		}
	}
	// Reverse direction: an index template for an undeclared index would
	// PUT a cluster-level template no lane routes to.
	names, err := jsonBasenames(name, fsys, MonitoringDirIndexTemplates)
	if err != nil {
		return err
	}
	for _, n := range names {
		if !declared[n] {
			return fmt.Errorf(
				"monitoring unit %q: %s/%s.json has no matching logs lane",
				name, MonitoringDirIndexTemplates, n,
			)
		}
	}
	return nil
}

// validateUnitPrefixedJSONDir checks a dir holds only .json files whose
// basenames are "<unit>-"-prefixed: these become cluster-level object
// names (pipeline id, component template name), so an unprefixed name
// could silently rewrite core or another unit's object.
func validateUnitPrefixedJSONDir(name string, fsys fs.FS, dir string) error {
	names, err := jsonBasenames(name, fsys, dir)
	if err != nil {
		return err
	}
	for _, n := range names {
		if n != name && !strings.HasPrefix(n, name+"-") {
			return fmt.Errorf(
				"monitoring unit %q: %s/%s.json: basename must be %q-prefixed — "+
					"it becomes a cluster-level object name",
				name, dir, n, name+"-",
			)
		}
	}
	return nil
}

// validateUnitISMPolicies enforces the custom-retention contract: policy
// files present iff some lane declares custom retention, and every
// policy's ism_template index patterns are prefixed by a unit-owned index
// name so two policies can never fight over the same indices on priority.
func validateUnitISMPolicies(name string, fsys fs.FS, lanes []config.MonitoringLogLane) error {
	custom := false
	ownIndices := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		ownIndices = append(ownIndices, lane.Index)
		if lane.Retention == config.MonitoringRetentionCustom {
			custom = true
		}
	}
	names, err := jsonBasenames(name, fsys, MonitoringDirISMPolicies)
	if err != nil {
		return err
	}
	if !custom {
		if len(names) > 0 {
			return fmt.Errorf(
				"monitoring unit %q: %s/ present but no lane declares retention: %s",
				name, MonitoringDirISMPolicies, config.MonitoringRetentionCustom,
			)
		}
		return nil
	}
	if len(names) == 0 {
		return fmt.Errorf(
			"monitoring unit %q: retention: %s declared but %s/ ships no policy",
			name, config.MonitoringRetentionCustom, MonitoringDirISMPolicies,
		)
	}
	for _, n := range names {
		file := path.Join(MonitoringDirISMPolicies, n+".json")
		if scopeErr := validateISMPolicyScope(name, fsys, file, ownIndices); scopeErr != nil {
			return scopeErr
		}
	}
	return nil
}

func validateISMPolicyScope(name string, fsys fs.FS, file string, ownIndices []string) error {
	raw, err := fs.ReadFile(fsys, file)
	if err != nil {
		return fmt.Errorf("monitoring unit %q: read %s: %w", name, file, err)
	}
	var policy struct {
		Policy struct {
			ISMTemplate []struct {
				IndexPatterns []string `json:"index_patterns"` //nolint:tagliatelle // OpenSearch wire format
			} `json:"ism_template"` //nolint:tagliatelle // OpenSearch wire format
		} `json:"policy"`
	}
	if jsonErr := json.Unmarshal(raw, &policy); jsonErr != nil {
		return fmt.Errorf("monitoring unit %q: parse %s: %w", name, file, jsonErr)
	}
	patterns := []string{}
	for _, t := range policy.Policy.ISMTemplate {
		patterns = append(patterns, t.IndexPatterns...)
	}
	if len(patterns) == 0 {
		return fmt.Errorf(
			"monitoring unit %q: %s: policy declares no ism_template index_patterns — it would never auto-apply",
			name, file,
		)
	}
	for _, p := range patterns {
		if !patternScopedToIndices(p, ownIndices) {
			return fmt.Errorf(
				"monitoring unit %q: %s: index pattern %q is not scoped to a unit-owned index (%s) — "+
					"an unscoped pattern fights other retention policies on priority",
				name, file, p, strings.Join(ownIndices, ", "),
			)
		}
	}
	return nil
}

// patternScopedToIndices reports whether an ISM index pattern targets only
// a unit-owned index: the exact index name, or the index name followed by
// a glob/suffix (e.g. "codex-usage*", "codex-usage-*").
func patternScopedToIndices(pattern string, indices []string) bool {
	for _, idx := range indices {
		if pattern == idx || strings.HasPrefix(pattern, idx+"-") || strings.HasPrefix(pattern, idx+"*") {
			return true
		}
	}
	return false
}

// validateUnitSavedObjects checks saved-objects/ holds only .ndjson import
// files plus the explore/ panel dir (.json per panel).
func validateUnitSavedObjects(name string, fsys fs.FS) error {
	entries, err := fs.ReadDir(fsys, MonitoringDirSavedObjects)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("monitoring unit %q: read %s/: %w", name, MonitoringDirSavedObjects, err)
	}
	for _, e := range entries {
		if entryErr := validateSavedObjectEntry(name, fsys, e); entryErr != nil {
			return entryErr
		}
	}
	return nil
}

// validateSavedObjectEntry checks one saved-objects/ entry: the explore/
// panel dir, or an .ndjson import file.
func validateSavedObjectEntry(name string, fsys fs.FS, e fs.DirEntry) error {
	if e.IsDir() {
		if e.Name() != MonitoringDirExplore {
			return fmt.Errorf(
				"monitoring unit %q: unknown directory %s/%s — bootstrap would silently ignore it",
				name, MonitoringDirSavedObjects, e.Name(),
			)
		}
		exploreDir := path.Join(MonitoringDirSavedObjects, MonitoringDirExplore)
		return validateUnitJSONDir(name, fsys, exploreDir)
	}
	if path.Ext(e.Name()) != ".ndjson" {
		return fmt.Errorf(
			"monitoring unit %q: %s/%s: saved-object imports must be .ndjson files",
			name, MonitoringDirSavedObjects, e.Name(),
		)
	}
	return nil
}

// validateUnitJSONDir checks an optional dir holds only .json files.
func validateUnitJSONDir(name string, fsys fs.FS, dir string) error {
	_, err := jsonBasenames(name, fsys, dir)
	return err
}

// jsonBasenames lists the ".json"-stripped basenames in dir, sorted. A
// missing dir yields nil. A subdirectory or non-.json file is an error —
// the bootstrap loops glob "*.json" and would silently skip anything else.
func jsonBasenames(name string, fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("monitoring unit %q: read %s/: %w", name, dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			return nil, fmt.Errorf(
				"monitoring unit %q: %s/%s: only .json files belong here — bootstrap would silently ignore it",
				name, dir, e.Name(),
			)
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	slices.Sort(names)
	return names, nil
}

// WalkArtifacts calls fn for every artifact file in the unit with its
// unit-relative slash path (e.g. "index-templates/codex.json") and
// content, skipping the manifest. Paths mirror the bootstrap tree so a
// consumer overlays them verbatim.
func (u *MonitoringUnit) WalkArtifacts(fn func(relPath string, content []byte) error) error {
	err := fs.WalkDir(u.fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || p == MonitoringUnitManifestFile {
			return nil
		}
		content, readErr := fs.ReadFile(u.fsys, p)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", p, readErr)
		}
		return fn(p, content)
	})
	if err != nil {
		return fmt.Errorf("monitoring unit %q: walk artifacts: %w", u.Name, err)
	}
	return nil
}
