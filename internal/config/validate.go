package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// Known YAML field sets for the stack/harness registry nodes and the
// per-harness build overlay. storage's generic node merge intentionally
// lets unknown keys survive elsewhere — comments and forward/backward
// compatibility depend on it (see internal/storage: "Unknown keys
// survive") — so these schema nodes get an explicit front-door check
// instead: a typo'd field here is a load error naming its exact file and
// key path, rather than a silently ignored key.
// TestKnownFieldSets_MatchSchemaTags guards each set against drift from
// its schema struct's yaml tags.

// fieldPath is the shared registry/source field name for a filesystem (or
// repo-subdir) path.
const fieldPath = "path"

func knownStackRegistryFields() map[string]bool { return map[string]bool{fieldPath: true} }

func knownHarnessRegistryFields() map[string]bool {
	return map[string]bool{
		"config": true, "mount_projects": true, "env_file": true,
		"from_env": true, "env": true, "post_init": true, "pre_run": true,
		fieldPath: true,
	}
}

func knownHarnessOverlayFields() map[string]bool {
	return map[string]bool{"stacks": true, "packages": true, "inject": true}
}

func knownHarnessOverlayInjectFields() map[string]bool {
	return map[string]bool{"after_harness_install": true, "before_entrypoint": true}
}

func knownHarnessConfigOptionsFields() map[string]bool { return map[string]bool{"strategy": true} }

func knownMonitoringUnitFields() map[string]bool {
	return map[string]bool{fieldPath: true, "active": true}
}

func knownBundleSourceFields() map[string]bool {
	return map[string]bool{"url": true, "ref": true, "sha": true, fieldPath: true, "auto_update": true}
}

// validateProjectRegistries walks every discovered clawker.yaml layer —
// never the merged tree, so an error names the actual offending file — and
// validates the stacks:, harnesses:, and build.harnesses: nodes: every
// registered/overlay name must satisfy the shared naming rule
// (consts.ValidateName / consts.ValidateHarnessName), every stack-name
// reference (build.stacks, build.harnesses.<name>.stacks) must too, a
// stack registry entry must carry a path, and every entry's fields must be
// a known subset.
func validateProjectRegistries(store *storage.Store[Project]) error {
	for _, layer := range store.Layers() {
		label := layerLabel(layer)
		if err := validateStacksNode(label, layer.Data); err != nil {
			return err
		}
		if err := validateHarnessesNode(label, layer.Data); err != nil {
			return err
		}
		if err := validateBuildNode(label, layer.Data); err != nil {
			return err
		}
		if err := validateBundlesNode(layer); err != nil {
			return err
		}
	}
	return nil
}

// validateSettingsRegistries walks every discovered settings.yaml layer —
// never the merged tree, so an error names the actual offending file — and
// validates the monitoring.units registry node: every registered name must
// satisfy the shared naming rule (consts.ValidateName), every entry's
// fields must be a known subset, a path (when present) must be a
// non-empty absolute string without ~/$VAR expansion (the registry is
// host-global — a relative path would be cwd-dependent), and active
// (when present) must be a boolean.
func validateSettingsRegistries(store *storage.Store[Settings]) error {
	for _, layer := range store.Layers() {
		label := settingsLayerLabel(layer)
		if err := validateMonitoringUnitsNode(label, layer.Data); err != nil {
			return err
		}
	}
	return nil
}

// settingsLayerLabel names a settings layer for error messages, mirroring
// layerLabel's placeholder handling for the virtual defaults layer.
func settingsLayerLabel(l storage.LayerInfo) string {
	if l.Filename == "" {
		return "clawker settings"
	}
	return l.Filename
}

func validateMonitoringUnitsNode(label string, data map[string]any) error {
	rawMon, ok := data["monitoring"]
	if !ok {
		return nil
	}
	mon, isMap := nodeMapping(rawMon)
	if !isMap {
		// Not this validator's node to police: the typed decode of the
		// merged tree owns the monitoring block's general shape.
		return nil
	}
	raw, ok := mon["units"]
	if !ok {
		return nil
	}
	m, isMap := nodeMapping(raw)
	if !isMap {
		return fmt.Errorf("%s: monitoring.units: must be a mapping of name to {path, active}", label)
	}
	return validateEntryMap(label, "monitoring.units", m, consts.ValidateName,
		"must be a mapping with path and/or active keys", knownMonitoringUnitFields(),
		validateMonitoringUnitEntry(label))
}

// validateMonitoringUnitEntry checks one monitoring.units entry: path
// (when present) absolute without expansion, active (when present) a
// boolean.
func validateMonitoringUnitEntry(label string) func(keyPath string, entry map[string]any) error {
	return func(keyPath string, entry map[string]any) error {
		if p, hasPath := entry[fieldPath]; hasPath {
			if err := validateAbsolutePathValue(label, keyPath+".path", p); err != nil {
				return err
			}
		}
		if a, hasActive := entry["active"]; hasActive {
			if _, isBool := a.(bool); !isBool {
				return fmt.Errorf("%s: %s.active: must be a boolean", label, keyPath)
			}
		}
		return nil
	}
}

// layerLabel names a layer for error messages: its filename, or a
// placeholder for the virtual defaults/seed layer that every storage.Store
// carries (it has no backing file, so no filename); real file layers always
// carry a non-empty filename.
func layerLabel(l storage.LayerInfo) string {
	if l.Filename == "" {
		return "clawker config"
	}
	return l.Filename
}

func validateStacksNode(label string, data map[string]any) error {
	raw, ok := data["stacks"]
	if !ok {
		return nil
	}
	m, isMap := nodeMapping(raw)
	if !isMap {
		return fmt.Errorf("%s: stacks: must be a mapping of name to {path}", label)
	}
	return validateEntryMap(label, "stacks", m, consts.ValidateName,
		"must be a mapping with a path key", knownStackRegistryFields(),
		func(keyPath string, entry map[string]any) error {
			p, hasPath := entry[fieldPath]
			if !hasPath {
				return fmt.Errorf("%s: %s: missing required path", label, keyPath)
			}
			return validatePathValue(label, keyPath+".path", p)
		})
}

func validateHarnessesNode(label string, data map[string]any) error {
	raw, ok := data["harnesses"]
	if !ok {
		return nil
	}
	m, isMap := nodeMapping(raw)
	if !isMap {
		return fmt.Errorf("%s: harnesses: must be a mapping of name to config", label)
	}
	return validateEntryMap(label, "harnesses", m, consts.ValidateHarnessName,
		"must be a mapping", knownHarnessRegistryFields(),
		func(keyPath string, entry map[string]any) error {
			if c, hasConfig := entry["config"]; hasConfig {
				if err := validateHarnessConfigOptions(label, keyPath+".config", c); err != nil {
					return err
				}
			}
			p, hasPath := entry[fieldPath]
			if !hasPath {
				return nil
			}
			return validatePathValue(label, keyPath+".path", p)
		})
}

func validateBuildNode(label string, data map[string]any) error {
	raw, ok := data["build"]
	if !ok {
		return nil
	}
	build, isMap := nodeMapping(raw)
	if !isMap {
		return fmt.Errorf("%s: build: must be a mapping", label)
	}
	if stacks, hasStacks := build["stacks"]; hasStacks && stacks != nil {
		if err := validateStackNameList(label, "build.stacks", stacks); err != nil {
			return err
		}
	}
	harnessesRaw, hasHarnesses := build["harnesses"]
	if !hasHarnesses {
		return nil
	}
	harnesses, isMap := nodeMapping(harnessesRaw)
	if !isMap {
		return fmt.Errorf("%s: build.harnesses: must be a mapping of name to overlay", label)
	}
	return validateEntryMap(label, "build.harnesses", harnesses, consts.ValidateHarnessName,
		"must be a mapping", knownHarnessOverlayFields(), validateOverlayEntry(label))
}

// validateOverlayEntry returns the per-entry check for one
// build.harnesses.<name> overlay: its stacks list and its inject block.
func validateOverlayEntry(label string) func(keyPath string, overlay map[string]any) error {
	return func(keyPath string, overlay map[string]any) error {
		if stacks, hasStacks := overlay["stacks"]; hasStacks && stacks != nil {
			if err := validateStackNameList(label, keyPath+".stacks", stacks); err != nil {
				return err
			}
		}
		injectRaw, hasInject := overlay["inject"]
		if !hasInject {
			return nil
		}
		inject, isMap := nodeMapping(injectRaw)
		if !isMap {
			return fmt.Errorf("%s: %s.inject: must be a mapping", label, keyPath)
		}
		return validateKnownFields(label, keyPath+".inject", inject, knownHarnessOverlayInjectFields())
	}
}

// shaRe matches a full 40-character lowercase-hex git commit SHA — the only
// shape a bundle source's sha: field may take (an abbreviated or upper-case
// SHA is rejected so the resolver never has to canonicalize it).
var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// validateBundlesNode validates one clawker.yaml layer's bundles: node. It
// takes the whole LayerInfo (not just the decoded data) because the
// config-dir-layer absolute-path rule is layer-position-dependent: a local
// path-only source in the user config-dir clawker.yaml has no project root to
// resolve a relative path against, so it must be absolute — a project-layer
// file resolves relative paths against its own project root and is fine. The
// bundles: list is union-merged across layers, so this per-layer walk (never
// the merged tree) is what surfaces a malformed source hidden in a
// lower-priority file behind a valid winning layer.
func validateBundlesNode(layer storage.LayerInfo) error {
	label := layerLabel(layer)
	raw, ok := layer.Data["bundles"]
	if !ok || raw == nil {
		return nil
	}
	list, isList := raw.([]any)
	if !isList {
		return fmt.Errorf("%s: bundles: must be a list of bundle sources", label)
	}
	configDirLayer := isConfigDirLayer(layer)
	for i, item := range list {
		keyPath := fmt.Sprintf("bundles[%d]", i)
		entry, isMap := nodeMapping(item)
		if !isMap {
			return fmt.Errorf("%s: %s: must be a mapping", label, keyPath)
		}
		if err := validateKnownFields(label, keyPath, entry, knownBundleSourceFields()); err != nil {
			return err
		}
		if err := validateBundleSourceEntry(label, keyPath, entry, configDirLayer); err != nil {
			return err
		}
	}
	return nil
}

// validateBundleSourceEntry checks one bundle source: exactly one of a remote
// url (optionally with a subdir path + at least one of ref/sha) or a local
// path-alone source (no url, no ref/sha). A sha must be a full 40-hex commit
// id; a config-dir-layer path source must be absolute.
func validateBundleSourceEntry(label, keyPath string, entry map[string]any, configDirLayer bool) error {
	src, err := decodeBundleSourceFields(label, keyPath, entry)
	if err != nil {
		return err
	}
	if src.hasURL {
		return validateRemoteBundleSource(label, keyPath, src)
	}
	return validateLocalBundleSource(label, keyPath, src, configDirLayer)
}

// bundleSourceFields is the type-checked field view of one bundles[] entry,
// shared by the remote/local validators.
type bundleSourceFields struct {
	url, ref, sha, path             string
	hasURL, hasRef, hasSHA, hasPath bool
}

func decodeBundleSourceFields(label, keyPath string, entry map[string]any) (bundleSourceFields, error) {
	var src bundleSourceFields
	var err error
	if src.url, src.hasURL, err = optionalStringField(label, keyPath, "url", entry); err != nil {
		return src, err
	}
	if src.ref, src.hasRef, err = optionalStringField(label, keyPath, "ref", entry); err != nil {
		return src, err
	}
	if src.sha, src.hasSHA, err = optionalStringField(label, keyPath, "sha", entry); err != nil {
		return src, err
	}
	if src.path, src.hasPath, err = optionalStringField(label, keyPath, fieldPath, entry); err != nil {
		return src, err
	}
	if a, hasAuto := entry["auto_update"]; hasAuto && a != nil {
		if _, isBool := a.(bool); !isBool {
			return src, fmt.Errorf("%s: %s.auto_update: must be a boolean", label, keyPath)
		}
	}
	return src, nil
}

// validateRemoteBundleSource checks a url-bearing source: non-empty url, at
// least one usable ref/sha, and a full 40-hex sha when given. A path alongside
// a url is a repository subdirectory (monorepo case), not a host path — no
// absolute/relative rule applies to it.
func validateRemoteBundleSource(label, keyPath string, src bundleSourceFields) error {
	if src.url == "" {
		return fmt.Errorf("%s: %s.url: must not be empty", label, keyPath)
	}
	if src.hasRef && src.ref == "" {
		return fmt.Errorf("%s: %s.ref: must not be empty", label, keyPath)
	}
	if !src.hasRef && !src.hasSHA {
		return fmt.Errorf("%s: %s: a remote url source requires ref or sha", label, keyPath)
	}
	if src.hasSHA && !shaRe.MatchString(src.sha) {
		return fmt.Errorf("%s: %s.sha: %q is not a 40-character hex commit SHA", label, keyPath, src.sha)
	}
	return nil
}

// validateLocalBundleSource checks a path-alone source (the dev loop): ref/sha
// are meaningless without something to fetch, and a config-dir-layer path must
// be absolute because that layer has no project root to resolve against.
func validateLocalBundleSource(label, keyPath string, src bundleSourceFields, configDirLayer bool) error {
	if src.hasRef || src.hasSHA {
		return fmt.Errorf("%s: %s: ref and sha require a url", label, keyPath)
	}
	if !src.hasPath {
		return fmt.Errorf("%s: %s: must set url or path", label, keyPath)
	}
	if err := validatePathValue(label, keyPath+".path", src.path); err != nil {
		return err
	}
	if configDirLayer && !filepath.IsAbs(src.path) {
		return fmt.Errorf(
			"%s: %s.path: %q must be an absolute path in the user config-dir layer (a relative path there has no project root to resolve against)",
			label,
			keyPath,
			src.path,
		)
	}
	return nil
}

// optionalStringField reads an optional string-valued key from a decoded map
// entry: present=false when the key is absent or explicitly null; present=true
// with a "must be a string" error when the value is a non-string scalar; the
// decoded string otherwise. The map view is what yaml.v3 produced, so an int
// or bool never silently coerces here — it surfaces as a type error naming the
// exact file and key path.
func optionalStringField(label, keyPath, field string, entry map[string]any) (string, bool, error) {
	raw, ok := entry[field]
	if !ok || raw == nil {
		return "", false, nil
	}
	s, isString := raw.(string)
	if !isString {
		return "", true, fmt.Errorf("%s: %s.%s: must be a string", label, keyPath, field)
	}
	return s, true, nil
}

// isConfigDirLayer reports whether a discovered layer is the user config-dir
// clawker.yaml (as opposed to a project walk-up layer). It compares the
// layer's directory against consts.ConfigDir(); a virtual/in-memory layer
// (empty Path) is never the config-dir layer.
func isConfigDirLayer(layer storage.LayerInfo) bool {
	if layer.Path == "" {
		return false
	}
	return filepath.Clean(filepath.Dir(layer.Path)) == filepath.Clean(consts.ConfigDir())
}

// validateEntryMap iterates a name→entry mapping (stacks:, harnesses:,
// build.harnesses:) in sorted key order. For each entry it checks the key
// against validateName, asserts the value is a mapping (erroring with
// notAMapMsg otherwise), rejects unknown fields via known, then runs
// perEntry for the node's entry-specific field checks. node is the key-path
// prefix used in error messages (e.g. "stacks", "build.harnesses").
func validateEntryMap(
	label, node string,
	m map[string]any,
	validateName func(string) error,
	notAMapMsg string,
	known map[string]bool,
	perEntry func(keyPath string, entry map[string]any) error,
) error {
	for _, name := range sortedKeys(m) {
		keyPath := node + "." + name
		if err := validateName(name); err != nil {
			return fmt.Errorf("%s: %s: %w", label, keyPath, err)
		}
		entry, isMap := nodeMapping(m[name])
		if !isMap {
			return fmt.Errorf("%s: %s: %s", label, keyPath, notAMapMsg)
		}
		if err := validateKnownFields(label, keyPath, entry, known); err != nil {
			return err
		}
		if err := perEntry(keyPath, entry); err != nil {
			return err
		}
	}
	return nil
}

// validateStackNameList validates a build.stacks-shaped node: a list of
// stack-name strings, each checked against the shared naming rule.
func validateStackNameList(label, keyPath string, raw any) error {
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s: %s: must be a list of stack names", label, keyPath)
	}
	for i, item := range list {
		name, isString := item.(string)
		if !isString {
			return fmt.Errorf("%s: %s[%d]: must be a string", label, keyPath, i)
		}
		if err := consts.ValidateName(name); err != nil {
			return fmt.Errorf("%s: %s[%d]: %w", label, keyPath, i, err)
		}
	}
	return nil
}

// validateHarnessConfigOptions checks a harness entry's config sub-block:
// only known fields, and a strategy value from the closed vocabulary
// (ConfigStrategyCopy / ConfigStrategyFresh; empty means the default). A
// typo'd strategy would otherwise decode silently and be treated as the
// default by ConfigStrategy().
func validateHarnessConfigOptions(label, keyPath string, raw any) error {
	c, isMap := nodeMapping(raw)
	if !isMap {
		return fmt.Errorf("%s: %s: must be a mapping", label, keyPath)
	}
	if err := validateKnownFields(label, keyPath, c, knownHarnessConfigOptionsFields()); err != nil {
		return err
	}
	rawStrategy, hasStrategy := c["strategy"]
	if !hasStrategy || rawStrategy == nil {
		return nil
	}
	strategy, isString := rawStrategy.(string)
	if !isString {
		return fmt.Errorf("%s: %s.strategy: must be a string", label, keyPath)
	}
	switch strategy {
	case "", ConfigStrategyCopy, ConfigStrategyFresh:
		return nil
	default:
		return fmt.Errorf(
			"%s: %s.strategy: unknown strategy %q (want %s or %s)",
			label, keyPath, strategy, ConfigStrategyCopy, ConfigStrategyFresh,
		)
	}
}

// validatePathValue checks a registry entry's path field shape: a
// non-empty string with no ~ home-dir or $VAR environment-variable
// expansion (resolution against the project root, and existence on disk,
// happen downstream at consumption time — this is the load-time shape
// check only). The ~ and $ characters are rejected ANYWHERE in the path,
// not just in expansion position: paths legitimately containing them are
// pathological enough that ruling them out wholesale keeps the check dumb
// and the failure obvious.
func validatePathValue(label, keyPath string, raw any) error {
	if raw == nil {
		return fmt.Errorf("%s: %s: must not be empty", label, keyPath)
	}
	p, isString := raw.(string)
	if !isString {
		return fmt.Errorf("%s: %s: must be a string", label, keyPath)
	}
	if p == "" {
		return fmt.Errorf("%s: %s: must not be empty", label, keyPath)
	}
	if strings.Contains(p, "~") {
		return fmt.Errorf(
			"%s: %s: %q must not use ~ home-dir expansion — register a relative (from the project root) or absolute path",
			label,
			keyPath,
			p,
		)
	}
	if strings.Contains(p, "$") {
		return fmt.Errorf(
			"%s: %s: %q must not use $VAR environment-variable expansion — register a relative (from the project root) or absolute path",
			label,
			keyPath,
			p,
		)
	}
	return nil
}

// validateAbsolutePathValue applies validatePathValue and additionally
// requires an absolute path — for host-global registries whose entries
// must not depend on the invoking working directory.
func validateAbsolutePathValue(label, keyPath string, raw any) error {
	if err := validatePathValue(label, keyPath, raw); err != nil {
		return err
	}
	p, isString := raw.(string)
	if !isString {
		// Unreachable: validatePathValue already rejected non-strings.
		return fmt.Errorf("%s: %s: must be a string", label, keyPath)
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s: %s: %q must be an absolute path — the registry is host-global", label, keyPath, p)
	}
	return nil
}

// validateKnownFields rejects any key in entry not present in known,
// naming the offending file + key path.
func validateKnownFields(label, keyPath string, entry map[string]any, known map[string]bool) error {
	for _, field := range sortedKeys(entry) {
		if !known[field] {
			return fmt.Errorf("%s: %s.%s: unknown field", label, keyPath, field)
		}
	}
	return nil
}

// nodeMapping coerces a raw decoded node value into a mapping. A nil value
// (a key present with no content, e.g. a bare "build:" line) is an empty
// mapping — YAML nulls decode to the zero struct, so they are valid
// everywhere a mapping is expected.
func nodeMapping(raw any) (map[string]any, bool) {
	if raw == nil {
		return map[string]any{}, true
	}
	m, ok := raw.(map[string]any)
	return m, ok
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
