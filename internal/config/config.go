// Package config provides types for interacting with clawker configuration files.
// It loads clawker.yaml (project) and settings.yaml (user) into a typed Config
// backed by storage.Store[T], with separate stores for project and settings schemas.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// schemaHeaderPrefix is the yaml-language-server directive prefix. Combined
// with a published JSON Schema URL it becomes the head comment editors (VS
// Code, JetBrains via the YAML language server) read to validate and
// autocomplete the file.
const schemaHeaderPrefix = "yaml-language-server: $schema="

// schemaHeader returns the header comment to stamp into files written by a
// store: the yaml-language-server directive pointing at the published JSON
// Schema, pinned to the frozen git ref derived from this binary's build
// metadata (release tag, describe base tag, or commit SHA). Derived here — not plumbed from the Factory — because NewConfig is
// called directly by every binary (CLI, CP, host proxy, bridge) and all of
// them must stamp the same header for the same build.
func schemaHeader(filename string) string {
	return schemaHeaderPrefix + consts.SchemaURL(filename, consts.SchemaRef(build.Version, build.Revision))
}

// Config is the public configuration contract.
// Add methods here as the config contract grows.
//
//go:generate moq -rm -pkg mocks -out mocks/config_mock.go . Config
type Config interface {
	ClawkerIgnoreName() string
	Project() *Project
	Settings() *Settings

	// ProjectStore returns the underlying project config store.
	// Use Store.Set(path, value)/Store.Remove(path) to mutate and Store.Write() to persist.
	ProjectStore() *storage.Store[Project]

	// SettingsStore returns the underlying settings store.
	// Use Store.Set(path, value)/Store.Remove(path) to mutate and Store.Write() to persist.
	SettingsStore() *storage.Store[Settings]

	// ProjectRoot returns the resolved project root the config was loaded
	// against (the WithProjectRoot anchor), or "" when none was set (config-dir
	// only loads: CP/host-proxy/bridge daemons, and the in-memory test doubles).
	// It is the base directory relative registry paths (stacks:/harnesses:
	// path entries) resolve against.
	ProjectRoot() string

	// Deprecated: Use SettingsStore().Read().Logging instead.
	LoggingConfig() LoggingConfig

	// Deprecated: Use SettingsStore().Read().Monitoring instead.
	MonitoringConfig() MonitoringConfig

	// Deprecated: Use SettingsStore().Read().HostProxy instead.
	HostProxyConfig() HostProxyConfig

	ProjectEgressRules() []EgressRule

	// BundleDeclarations returns every declared bundle source paired with the
	// clawker.yaml layer that declared it, highest-priority layer first. The
	// union-merged Project().Bundles slice loses per-entry provenance; the
	// bundle resolver needs the declaring file so an identity-collision error
	// (two sources resolving to the same namespace.name) can name both
	// offending files.
	BundleDeclarations() []BundleDeclaration

	Domain() string
	LabelDomain() string
	ConfigDirEnvVar() string
	StateDirEnvVar() string
	DataDirEnvVar() string
	TestRepoDirEnvVar() string
	MonitorSubdir() (string, error)
	BuildSubdir() (string, error)
	ClawkerNetwork() string
	LogsSubdir() (string, error)
	BridgesSubdir() (string, error)
	PidsSubdir() (string, error)
	BridgePIDFilePath(containerID string) (string, error)
	HostProxyLogFilePath() (string, error)
	HostProxyPIDFilePath() (string, error)
	ShareSubdir() (string, error)
	LabelPrefix() string
	LabelManaged() string
	LabelProject() string
	LabelAgent() string
	LabelVersion() string
	LabelImage() string
	LabelCreated() string
	LabelWorkdir() string
	LabelPurpose() string
	PurposeAgent() string
	PurposeMonitoring() string
	PurposeFirewall() string
	LabelTestName() string
	LabelTest() string
	LabelE2ETest() string
	ManagedLabelValue() string
	EngineLabelPrefix() string
	EngineManagedLabel() string
	ContainerUID() int
	ContainerGID() int
	// In-cluster base URLs (host + port) for monitoring services
	// reachable from the clawker network. Composed from
	// [consts.MonitoringService*] hostnames + the corresponding
	// MonitoringConfig port. No path component.
	OpenSearchURL() string
	OpenSearchDashboardsURL() string
	PrometheusURL() string

	// OtelCollectorURL is the OTLP collector base URL on the clawker network
	// (no path). Wire it into the container as OTEL_EXPORTER_OTLP_ENDPOINT
	// — the OTel SDK derives /v1/metrics, /v1/logs, /v1/traces by
	// appending the standard path per signal, so a single base covers
	// every current and future OTLP signal. Default routes via the
	// collector so Prometheus retains metric metadata (its
	// /api/v1/metadata excludes OTLP-ingested series).
	OtelCollectorURL() string
	EgressRulesFileName() string
	FirewallDataSubdir() (string, error)
	FirewallCertSubdir() (string, error)
	EnvoyIPLastOctet() byte
	CoreDNSIPLastOctet() byte
	CPIPLastOctet() byte
	EnvoyEgressPort() int
	EnvoyTCPPortBase() int
	EnvoyUDPPortBase() int
	EnvoyHealthPort() int
	EnvoyHealthHostPort() int
	CoreDNSHealthHostPort() int
	CoreDNSHealthPath() string
	ProjectConfigFileName() string
	SettingsFileName() string
}

type configImpl struct {
	project     *storage.Store[Project]
	settings    *storage.Store[Settings]
	projectRoot string
}

// ProjectRoot returns the resolved project root anchor the config was loaded
// against (empty when walk-up was disabled). Relative registry paths resolve
// against it.
func (c *configImpl) ProjectRoot() string {
	return c.projectRoot
}

type NewConfigOption func(*newConfigOptions)

type newConfigOptions struct {
	projectYAML  string
	settingsYAML string
	projectRoot  string
}

// NewConfig loads all clawker configuration files into a Config.
// The project store discovers clawker.yaml via walk-up (CWD → project root)
// and config dir. The settings store loads settings.yaml from config dir.
// Both stores use defaults as the lowest-priority base layer.
func NewConfig(opts ...NewConfigOption) (Config, error) {
	options := &newConfigOptions{}
	for _, opt := range opts {
		opt(options)
	}
	projectOpts := []storage.Option{
		storage.WithFilenames(consts.ProjectLocalConfigFile, consts.ProjectConfigFile),
		storage.WithDefaultFilename(consts.ProjectConfigFile),
	}
	if options.projectYAML != "" {
		projectOpts = append(projectOpts, storage.WithDefaults(options.projectYAML))
	} else {
		projectOpts = append(projectOpts, storage.WithDefaultsFromStruct[Project]())
	}
	projectOpts = append(projectOpts,
		storage.WithWalkUp(options.projectRoot),
		storage.WithConfigDir(),
		storage.WithDotDefault(),
		storage.WithMigrations(ProjectMigrations()...),
		storage.WithHeader(schemaHeader(consts.ProjectSchemaFile)),
	)
	projectStore, err := storage.New[Project]("", projectOpts...)
	if err != nil {
		return nil, fmt.Errorf("config: loading project config: %w", err)
	}
	if vErr := validateProjectNodes(projectStore); vErr != nil {
		return nil, fmt.Errorf("config: validating project config: %w", vErr)
	}

	settingsOpts := []storage.Option{
		storage.WithFilenames(consts.SettingsFile),
	}
	if options.settingsYAML != "" {
		settingsOpts = append(settingsOpts, storage.WithDefaults(options.settingsYAML))
	} else {
		settingsOpts = append(settingsOpts, storage.WithDefaultsFromStruct[Settings]())
	}
	settingsOpts = append(settingsOpts,
		storage.WithConfigDir(),
		storage.WithMigrations(SettingsMigrations()...),
		storage.WithHeader(schemaHeader(consts.SettingsSchemaFile)),
	)
	settingsStore, err := storage.New[Settings]("", settingsOpts...)
	if err != nil {
		return nil, fmt.Errorf("config: loading settings: %w", err)
	}

	return &configImpl{
		project:     projectStore,
		settings:    settingsStore,
		projectRoot: options.projectRoot,
	}, nil
}

func WithDefaultProjectYAML(yaml string) NewConfigOption {
	return func(o *newConfigOptions) {
		o.projectYAML = yaml
	}
}

func WithDefaultSettingsYAML(yaml string) NewConfigOption {
	return func(o *newConfigOptions) {
		o.settingsYAML = yaml
	}
}

// WithProjectRoot bounds project-config walk-up discovery at the project root:
// the store walks from CWD up to root (inclusive). The caller resolves the root
// (e.g. via project.Registry.ResolveRoot) and passes it in; config does not
// resolve it. An empty root disables walk-up, so discovery uses the config dir
// only — correct for consumers that never resolve project config from a working
// directory (CP / host-proxy / bridge daemons), which read only settings.yaml.
func WithProjectRoot(root string) NewConfigOption {
	return func(o *newConfigOptions) {
		o.projectRoot = root
	}
}

// NewProjectStoreFromPreset creates an isolated project store from a preset
// YAML string. Unlike NewConfig, this does NO file discovery — no walk-up,
// no config dir, no user-level config merging. The store contains only the
// preset values, marked for write (MarkSeedForWrite) so WriteTo persists them.
//
// This is the correct constructor for project init: the written project file
// should contain exactly the preset values + any Set() mutations (VCS config,
// customize edits). User-level and parent configs are layered at runtime via
// normal config loading, not baked into the project file.
//
// The schema URL is wired so the file WriteTo writes carries the
// yaml-language-server header for editor validation.
func NewProjectStoreFromPreset(presetYAML string) (*storage.Store[Project], error) {
	store, err := storage.New[Project](presetYAML, storage.WithHeader(schemaHeader(consts.ProjectSchemaFile)))
	if err != nil {
		return nil, err
	}
	if vErr := validateProjectNodes(store); vErr != nil {
		return nil, fmt.Errorf("config: validating preset project config: %w", vErr)
	}
	store.MarkSeedForWrite()
	return store, nil
}

// NewBlankConfig creates a Config with defaults but no file discovery.
// Useful as the default test double for consumers that don't care about
// specific config values.
func NewBlankConfig() (Config, error) {
	projectStore, err := storage.New[Project](storage.GenerateDefaultsYAML[Project]())
	if err != nil {
		return nil, fmt.Errorf("config: blank project: %w", err)
	}
	if vErr := validateProjectNodes(projectStore); vErr != nil {
		return nil, fmt.Errorf("config: validating project config: %w", vErr)
	}
	settingsStore, err := storage.New[Settings](storage.GenerateDefaultsYAML[Settings]())
	if err != nil {
		return nil, fmt.Errorf("config: blank settings: %w", err)
	}
	return &configImpl{
		project:  projectStore,
		settings: settingsStore,
	}, nil
}

// NewFromString creates a Config from raw YAML strings without defaults.
// Empty strings produce empty structs. Useful for test fixtures that need
// precise control over values without defaults being merged.
func NewFromString(projectYAML, settingsYAML string) (Config, error) {
	projectStore, err := storage.New[Project](projectYAML)
	if err != nil {
		return nil, fmt.Errorf("config: parsing project YAML: %w", err)
	}
	if vErr := validateProjectNodes(projectStore); vErr != nil {
		return nil, fmt.Errorf("config: validating project config: %w", vErr)
	}
	settingsStore, err := storage.New[Settings](settingsYAML)
	if err != nil {
		return nil, fmt.Errorf("config: parsing settings YAML: %w", err)
	}
	return &configImpl{
		project:  projectStore,
		settings: settingsStore,
	}, nil
}

// ProjectEgressRules returns the egress rules configured under the
// project's security.firewall: explicit rules verbatim, then add_domains
// shorthand expansions. This is the project's contribution only — the
// selected harness's required egress floor is composed in by
// bundler.EgressRules, which is what firewall sync paths must call.
func (c *configImpl) ProjectEgressRules() []EgressRule {
	var rules []EgressRule
	projectFw := c.Project().Security.Firewall
	if projectFw != nil {
		rules = append(rules, projectFw.Rules...)
		for _, d := range projectFw.AddDomains {
			rules = append(
				rules,
				EgressRule{
					Dst:                   d,
					Proto:                 EgressProtoHTTPS,
					Port:                  EgressPortHTTPS,
					Action:                EgressActionAllow,
					PathRules:             nil,
					PathDefault:           "",
					InsecureSkipTLSVerify: false,
				},
			)
		}
	}
	return rules
}

// BundleDeclarations walks the project store's discovered layers (highest to
// lowest priority) and returns each layer's declared bundle sources paired
// with that layer's file path. It projects each source from the layer's
// decoded map view — a total projection over BundleSource's scalar fields,
// valid because validateBundlesNode already rejected any malformed source at
// load, so no per-layer decode can fail here. The union-merged
// Project().Bundles snapshot cannot carry this per-entry file provenance.
func (c *configImpl) BundleDeclarations() []BundleDeclaration {
	return declarationsFromLayers(c.project.Layers())
}

// declarationsFromLayers projects the bundles: node of each layer (highest
// priority first) into per-file declarations.
func declarationsFromLayers(layers []storage.LayerInfo) []BundleDeclaration {
	var decls []BundleDeclaration
	for _, layer := range layers {
		raw, ok := layer.Data["bundles"]
		if !ok || raw == nil {
			continue
		}
		list, isList := raw.([]any)
		if !isList {
			continue
		}
		for _, item := range list {
			entry, isMap := item.(map[string]any)
			if !isMap {
				continue
			}
			decls = append(decls, BundleDeclaration{
				Source: bundleSourceFromMap(entry),
				File:   layer.Path,
			})
		}
	}
	return decls
}

// BundleDeclarationsAt loads the bundle declarations of one project root
// WITHOUT a full config load: it probes EVERY directory under root (walk-up
// discovery makes any directory between a working directory and the project
// root a declaring layer, so a nested clawker.yaml is a first-class root)
// with the same dual placement a walk-up level gets (.clawker/ dir form
// first, then flat dotted files) for the project and local-override config
// files, validates only their bundles: nodes, and projects the declarations.
// It exists for the bundle cache's GC roots, which must union the declared
// source values of every REGISTERED project — not just the one the current
// process runs in.
//
// A missing root or a tree with no config files contributes nothing; an
// unparseable file or a malformed bundles: node is an error (roots must be
// computable before anything is collected), while mistakes in unrelated keys
// are ignored. The walk does not descend into dot-directories (each level's
// .clawker/ dir form is probed from its parent via dual placement), does not
// follow directory symlinks, and SKIPS a permission-denied subdirectory
// rather than failing — root-owned directories inside bind-mounted
// workspaces are routine for a Docker tool, and one of them must not make
// every prune fail forever. The skipped paths are returned so the caller can
// surface them. A subdirectory that VANISHES mid-walk (ordinary build churn —
// npm ci, a removed dist/) is likewise skipped, silently: it holds no
// declarations and is not operator-actionable. A layer hidden behind any of
// these bounds is not counted as a root, and a wrong collect self-heals with
// one refetch. An unreadable ROOT (as opposed to subdirectory) stays a hard
// error — that is the whole input, not a corner of it.
//
// Deliberately wired with NO migrations and NO writes: this loader runs
// against OTHER projects' files during GC, and it must never rewrite them —
// storage.applyMigrations is a no-op without WithMigrations, and nothing here
// calls Set/Write.
func BundleDeclarationsAt(root string) ([]BundleDeclaration, []string, error) {
	dirs, skipped, err := projectLayerDirs(root)
	if err != nil {
		return nil, nil, err
	}
	if len(dirs) == 0 {
		return nil, skipped, nil
	}
	store, err := storage.New[Project]("",
		storage.WithFilenames(consts.ProjectLocalConfigFile, consts.ProjectConfigFile),
		storage.WithDirs(dirs...),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("config: loading project config at %s: %w", root, err)
	}
	for _, layer := range store.Layers() {
		if vErr := validateBundlesNode(layer); vErr != nil {
			return nil, nil, fmt.Errorf("config: validating bundles at %s: %w", root, vErr)
		}
	}
	return declarationsFromLayers(store.Layers()), skipped, nil
}

// projectLayerDirs enumerates every directory under root that walk-up
// discovery could probe as a config layer for some working directory inside
// the project — root itself and every non-dot descendant directory. Dot-named
// directories are not descended into (their own .clawker/ dir-form files are
// probed from the parent level's dual placement), and symlinks are not
// followed (WalkDir never traverses them), so the walk cannot cycle or escape
// the root. A missing root yields no directories; a permission-denied
// SUBdirectory is skipped and reported in the second return rather than
// failing the walk (the directory itself stays in the probe list — its entry
// was seen from the parent, only its children are unreachable); a
// SUBdirectory deleted mid-walk (build churn) is skipped silently; any other
// walk error is surfaced — it could hide a declaring layer, and the GC roots
// this feeds must be computable before anything is collected.
func projectLayerDirs(root string) ([]string, []string, error) {
	var dirs []string
	var skipped []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			skip, verdict := classifyLayerWalkError(root, path, walkErr)
			if skip != "" {
				skipped = append(skipped, skip)
			}
			return verdict
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		dirs = append(dirs, path)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("config: enumerating config layer dirs under %s: %w", root, err)
	}
	return dirs, skipped, nil
}

// classifyLayerWalkError turns one layer-walk error into its verdict: a
// missing root ends the walk cleanly (contributes nothing); a
// permission-denied SUBdirectory is skipped and reported (first return names
// it — a persistent, operator-actionable state that genuinely hides content);
// a SUBdirectory that vanished mid-walk is skipped SILENTLY (ordinary build
// churn — npm ci, a removed dist/ — deletes non-dot dirs constantly, a
// directory that no longer exists holds no declarations, and warning on
// every prune that races a build would be noise, not signal); anything else —
// an unreadable root, EIO/ESTALE — is fatal: the tree genuinely could not be
// assessed, and a loud retriable failure beats a silently incomplete roots
// union.
func classifyLayerWalkError(root, path string, walkErr error) (string, error) {
	if path == root && errors.Is(walkErr, fs.ErrNotExist) {
		return "", filepath.SkipAll
	}
	if path != root && errors.Is(walkErr, fs.ErrPermission) {
		return path, filepath.SkipDir
	}
	if path != root && errors.Is(walkErr, fs.ErrNotExist) {
		return "", filepath.SkipDir
	}
	return "", walkErr
}

// bundleSourceFromMap projects a decoded bundles[] map entry into a typed
// BundleSource. It is total: each field coerces to its zero value when absent
// or the wrong type. Load-time validateBundlesNode guarantees the shape, so
// the zero-fallback branches are unreachable in practice — they keep the
// projection total without an error return. Extend this when BundleSource
// gains a field.
func bundleSourceFromMap(entry map[string]any) BundleSource {
	return BundleSource{
		URL:        stringFromMap(entry, "url"),
		Ref:        stringFromMap(entry, "ref"),
		SHA:        stringFromMap(entry, "sha"),
		Path:       stringFromMap(entry, "path"),
		AutoUpdate: boolFromMap(entry, "auto_update"),
	}
}

func stringFromMap(entry map[string]any, key string) string {
	if s, ok := entry[key].(string); ok {
		return s
	}
	return ""
}

func boolFromMap(entry map[string]any, key string) bool {
	if b, ok := entry[key].(bool); ok {
		return b
	}
	return false
}

// --- Store accessors ---

func (c *configImpl) ProjectStore() *storage.Store[Project] {
	return c.project
}

func (c *configImpl) SettingsStore() *storage.Store[Settings] {
	return c.settings
}

// --- Schema accessors ---

func (c *configImpl) Project() *Project {
	return c.project.Read()
}

func (c *configImpl) Settings() *Settings {
	return c.settings.Read()
}

func (c *configImpl) LoggingConfig() LoggingConfig {
	return c.settings.Read().Logging
}

func (c *configImpl) HostProxyConfig() HostProxyConfig {
	return c.settings.Read().HostProxy
}

func (c *configImpl) MonitoringConfig() MonitoringConfig {
	return c.settings.Read().Monitoring
}
