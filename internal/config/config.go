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

// Config is the public configuration contract: the domain facade over the
// project (clawker.yaml) and settings (settings.yaml) stores.
//
// Reads are value-specific — a consumer asks for the one value (or the one
// small group struct) it needs. There is deliberately NO whole-schema getter:
// it would hand every consumer every field and hide which keys they actually
// depend on. Group accessors (BuildConfig,
// AgentConfig, ControlPlaneSettings, …) return the nested block that genuinely
// travels together, never the schema root.
//
// Mutation goes through the raw store verbs via ProjectStore()/SettingsStore()
// (Set/Remove + Write) — the escape hatch for edge cases the typed accessors
// don't cover.
//
//go:generate moq -rm -pkg mocks -out mocks/config_mock.go . Config
type Config interface {
	ClawkerIgnoreName() string

	// ProjectStore returns the underlying project config store.
	// Use Store.Set(key, value)/Store.Remove(key...) to mutate and Store.Write() to persist.
	ProjectStore() *storage.Store[Project]

	// SettingsStore returns the underlying settings store.
	// Use Store.Set(key, value)/Store.Remove(key...) to mutate and Store.Write() to persist.
	SettingsStore() *storage.Store[Settings]

	// ProjectRoot returns the resolved project root the config was loaded
	// against (the WithProjectRoot anchor), or "" when none was set (config-dir
	// only loads: CP/host-proxy/bridge daemons, and the in-memory test doubles).
	// It is the base directory relative registry paths (stacks:/harnesses:
	// path entries) resolve against.
	ProjectRoot() string

	// --- project (clawker.yaml) accessors ---

	// ProjectName returns the `name` override for the directory-derived
	// project slug; empty when unset.
	ProjectName() string

	// Aliases returns the union-merged command aliases (alias name → clawker
	// argument string); nil when none are declared.
	Aliases() map[string]string

	// BuildConfig returns the `build:` block — the image-build inputs
	// (harness selection, stacks, packages, instructions, inject, per-harness
	// overlays) that are consumed as one unit by the Dockerfile generator.
	BuildConfig() BuildConfig

	// AgentConfig returns the `agent:` block — the harness-agnostic runtime
	// settings (env sources, editor, hooks) resolved together per container.
	AgentConfig() AgentConfig

	// WorkspaceDefaultMode returns `workspace.default_mode` (bind/snapshot);
	// empty when unset (no defaults layer). The value is enum-gated at decode
	// (Mode.UnmarshalYAML), so a loaded config can only ever hold a valid mode.
	WorkspaceDefaultMode() Mode

	// SecurityConfig returns the `security:` block — the container security
	// posture (firewall rules, docker socket, capabilities, host proxy, git
	// credential forwarding) applied as one unit at container create.
	SecurityConfig() SecurityConfig

	// HarnessConfigFor returns the effective per-harness initialization config
	// for the named harness: the `harnesses:` map entry when present, else the
	// deprecated agent.claude_code block for the built-in default harness,
	// else nil. Every HarnessConfig accessor is nil-tolerant.
	HarnessConfigFor(name string) *HarnessConfig

	// PostInitFor returns the composed post-init script for the named
	// harness: agent.post_init followed by the harness entry's post_init.
	PostInitFor(name string) string

	// PreRunFor returns the composed pre-run script for the named harness:
	// agent.pre_run followed by the harness entry's pre_run.
	PreRunFor(name string) string

	// MonitorExtensions returns `monitor.extensions` — the monitoring
	// extensions this project contributes to the host monitoring stack.
	MonitorExtensions() []string

	ProjectEgressRules() []EgressRule

	// --- settings (settings.yaml) accessors ---

	// LoggingConfig returns the `logging:` block (file logging + the OTEL
	// bridge), consumed as one unit when the file logger is constructed.
	LoggingConfig() LoggingConfig

	// MonitoringConfig returns the `monitoring:` block — the monitoring
	// stack's ports and telemetry knobs, rendered together into the compose
	// stack and the container's OTEL env.
	MonitoringConfig() MonitoringConfig

	// HostProxyConfig returns the `host_proxy:` block (manager + daemon
	// settings), consumed as one unit by the host proxy.
	HostProxyConfig() HostProxyConfig

	// ControlPlaneSettings returns the `control_plane:` block — the CP port
	// map, wired together into the CP container and the agent's endpoints.
	ControlPlaneSettings() ControlPlaneSettings

	// FirewallEnabled reports the global firewall master switch
	// (`firewall.enable`), defaulting to true when unset.
	FirewallEnabled() bool

	// DockerSocketPath returns the host-side Docker daemon socket path used
	// as the bind-mount source for Docker socket mounts. Resolution follows
	// docker CLI parity — environment beats stored configuration: a unix://
	// $DOCKER_HOST wins, then settings `docker.socket`, then
	// consts.DefaultDockerSocketPath. The in-container mount target is
	// always consts.DefaultDockerSocketPath, independent of this value.
	DockerSocketPath() string

	// BundleDeclarations returns every declared bundle source paired with the
	// clawker.yaml layer that declared it, highest-priority layer first. The
	// union-merged bundles: list loses per-entry provenance; the
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

// configImpl composes the two stores the config domain owns. It is the one
// store-backed package that cannot embed *storage.Store[T]: two schemas means
// two stores, and their promoted verb sets would collide. ProjectStore() /
// SettingsStore() are the named escape hatches that embedding would otherwise
// provide.
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
		// Concurrent processes write this file (project init, alias set/delete,
		// bundle install, the store editor), so every write must take the flock
		// around its read-modify-write cycle.
		storage.WithLock(),
	)
	projectStore, err := storage.New[Project](projectOpts...)
	if err != nil {
		return nil, fmt.Errorf("config: loading project config: %w", err)
	}
	if vErr := validateProjectNodes(projectStore); vErr != nil {
		return nil, fmt.Errorf("config: validating project config: %w", vErr)
	}

	settingsOpts := []storage.Option{
		storage.WithFilenames(consts.SettingsFile),
		storage.WithDefaultFilename(consts.SettingsFile),
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
		// Same cross-process exposure as the project store: the CLI, the store
		// editor, and the daemons all load and write settings.yaml.
		storage.WithLock(),
	)
	settingsStore, err := storage.New[Settings](settingsOpts...)
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
	store, err := storage.NewFromString[Project](
		presetYAML,
		storage.WithHeader(schemaHeader(consts.ProjectSchemaFile)),
	)
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
	projectStore, err := storage.New[Project](storage.WithDefaultsFromStruct[Project]())
	if err != nil {
		return nil, fmt.Errorf("config: blank project: %w", err)
	}
	if vErr := validateProjectNodes(projectStore); vErr != nil {
		return nil, fmt.Errorf("config: validating project config: %w", vErr)
	}
	settingsStore, err := storage.New[Settings](storage.WithDefaultsFromStruct[Settings]())
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
	projectStore, err := storage.NewFromString[Project](projectYAML)
	if err != nil {
		return nil, fmt.Errorf("config: parsing project YAML: %w", err)
	}
	if vErr := validateProjectNodes(projectStore); vErr != nil {
		return nil, fmt.Errorf("config: validating project config: %w", vErr)
	}
	settingsStore, err := storage.NewFromString[Settings](settingsYAML)
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
	// Both keys are independently optional: a project may declare only rules,
	// only the add_domains shorthand, or neither (ErrKeyNotFound → nothing to
	// contribute).
	rules, err := storage.Get[[]EgressRule](c.project, keySecurity, keyFirewall, keyRules)
	if err != nil {
		rules = nil
	}
	domains, err := storage.Get[[]string](c.project, keySecurity, keyFirewall, keyAddDomains)
	if err != nil {
		domains = nil
	}
	for _, d := range domains {
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
	return rules
}

// BundleDeclarations walks the project store's discovered layers (highest to
// lowest priority) and returns each layer's declared bundle sources paired
// with that layer's file path. It projects each source from the layer's
// decoded map view — a total projection over BundleSource's scalar fields,
// valid because validateBundlesNode already rejected any malformed source at
// load, so no per-layer decode can fail here. The union-merged
// merged bundles: list cannot carry this per-entry file provenance.
func (c *configImpl) BundleDeclarations() []BundleDeclaration {
	return declarationsFromLayers(c.project.Layers())
}

// declarationsFromLayers projects the bundles: node of each layer (highest
// priority first) into per-file declarations.
func declarationsFromLayers(layers []storage.LayerInfo) []BundleDeclaration {
	var decls []BundleDeclaration
	for _, layer := range layers {
		raw, ok := layer.Data[keyBundles]
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

// ProjectConfigExistsIn reports whether a project config file for dir itself
// exists: the project file or its local override variant, in either the flat
// dotted form or the .clawker/ directory form, with either extension. It
// probes with the storage engine's own discovery — the same dual placement a
// real load uses — so the answer cannot drift from what a later NewConfig
// would find, and it answers for dir alone (no walk-up, no config dir), which
// is the question `clawker init` / `clawker project register` ask about the
// directory they are about to claim.
//
// Migrations are wired because a load without them fails the strict decode on
// a legacy-shaped file — reporting "no config here" for a file that plainly
// exists is the one answer this predicate must never give. dir is the user's
// own working directory, so the rewrite a migration may commit is the same
// one the next real load would.
func ProjectConfigExistsIn(dir string) (bool, error) {
	store, err := storage.New[Project](
		storage.WithFilenames(consts.ProjectLocalConfigFile, consts.ProjectConfigFile),
		storage.WithDirs(dir),
		storage.WithMigrations(ProjectMigrations()...),
	)
	if err != nil {
		return false, fmt.Errorf("config: probing project config in %s: %w", dir, err)
	}
	return len(store.Layers()) > 0, nil
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
	store, err := storage.New[Project](
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

// --- Value accessors ---
//
// Every accessor below reads exactly the key it names via storage.Get and
// folds an absent key onto the zero value. Two facts make that fold correct
// and complete, and they hold for all of them:
//
//   - ErrKeyNotFound means the key is unset in EVERY layer. The zero value is
//     the right answer because schema defaults reach these accessors through
//     the store's own defaults layer (WithDefaultsFromStruct in NewConfig /
//     NewBlankConfig) — inventing a second default here would diverge from the
//     defaults-less constructors (NewFromString) on purpose-built test input.
//   - No other error is reachable: each key is a declared schema field, so a
//     value that could not decode into its type already failed the strict
//     decode inside the constructor.
//
// FirewallEnabled is the one exception — its domain default is true, not the
// zero value — and says so at its own definition.

func (c *configImpl) ProjectName() string {
	name, err := storage.Get[string](c.project, keyName)
	if err != nil {
		return ""
	}
	return name
}

func (c *configImpl) Aliases() map[string]string {
	aliases, err := storage.Get[map[string]string](c.project, keyAliases)
	if err != nil {
		return nil
	}
	return aliases
}

func (c *configImpl) BuildConfig() BuildConfig {
	buildCfg, err := storage.Get[BuildConfig](c.project, keyBuild)
	if err != nil {
		return BuildConfig{}
	}
	return buildCfg
}

func (c *configImpl) AgentConfig() AgentConfig {
	agent, err := storage.Get[AgentConfig](c.project, keyAgent)
	if err != nil {
		return AgentConfig{}
	}
	return agent
}

func (c *configImpl) WorkspaceDefaultMode() Mode {
	mode, err := storage.Get[Mode](c.project, keyWorkspace, keyDefaultMode)
	if err != nil {
		return "" // absent (ErrKeyNotFound) → unset; invalid values cannot survive the construction decode
	}
	return mode
}

func (c *configImpl) SecurityConfig() SecurityConfig {
	security, err := storage.Get[SecurityConfig](c.project, keySecurity)
	if err != nil {
		return SecurityConfig{}
	}
	return security
}

func (c *configImpl) MonitorExtensions() []string {
	extensions, err := storage.Get[[]string](c.project, keyMonitor, keyExtensions)
	if err != nil {
		return nil
	}
	return extensions
}

// HarnessConfigFor returns the effective per-harness initialization config for
// the named harness: the harnesses map entry when present, else the legacy
// agent.claude_code block for the built-in default harness (the deprecated
// read shim, which still matters for layers loaded without migrations), else
// nil. Every HarnessConfig accessor is nil-tolerant and yields defaults.
func (c *configImpl) HarnessConfigFor(name string) *HarnessConfig {
	if hc, err := storage.Get[HarnessConfig](c.project, keyHarnesses, name); err == nil {
		return &hc
	}
	if name != consts.DefaultHarnessName {
		return nil
	}
	if legacy, err := storage.Get[HarnessConfig](c.project, keyAgent, keyClaudeCode); err == nil {
		return &legacy
	}
	return nil
}

// PostInitFor composes the harness-agnostic agent.post_init base with the
// named harness's own post_init. Blank layers are skipped; both blank yields "".
func (c *configImpl) PostInitFor(name string) string {
	base, err := storage.Get[string](c.project, keyAgent, keyPostInit)
	if err != nil {
		base = ""
	}
	return composeHookScript(base, c.HarnessConfigFor(name).postInit())
}

// PreRunFor composes the harness-agnostic agent.pre_run base with the named
// harness's own pre_run. Blank layers are skipped; both blank yields "".
func (c *configImpl) PreRunFor(name string) string {
	base, err := storage.Get[string](c.project, keyAgent, keyPreRun)
	if err != nil {
		base = ""
	}
	return composeHookScript(base, c.HarnessConfigFor(name).preRun())
}

func (c *configImpl) LoggingConfig() LoggingConfig {
	logging, err := storage.Get[LoggingConfig](c.settings, keyLogging)
	if err != nil {
		return LoggingConfig{}
	}
	return logging
}

func (c *configImpl) HostProxyConfig() HostProxyConfig {
	hostProxy, err := storage.Get[HostProxyConfig](c.settings, keyHostProxy)
	if err != nil {
		return HostProxyConfig{}
	}
	return hostProxy
}

func (c *configImpl) MonitoringConfig() MonitoringConfig {
	monitoring, err := storage.Get[MonitoringConfig](c.settings, keyMonitoring)
	if err != nil {
		return MonitoringConfig{}
	}
	return monitoring
}

func (c *configImpl) ControlPlaneSettings() ControlPlaneSettings {
	controlPlane, err := storage.Get[ControlPlaneSettings](c.settings, keyControlPlane)
	if err != nil {
		return ControlPlaneSettings{}
	}
	return controlPlane
}

// FirewallEnabled reports the global firewall master switch. Its domain
// default is true, not the zero value: an unset firewall.enable — including a
// settings store with no defaults layer at all — means enabled, matching the
// fail-closed posture the rest of the stack assumes.
func (c *configImpl) FirewallEnabled() bool {
	enabled, err := storage.Get[bool](c.settings, keyFirewall, keyEnable)
	if err != nil {
		return true
	}
	return enabled
}

// DockerSocketPath resolves the host Docker socket path: $DOCKER_HOST
// (unix:// only) > settings docker.socket > default. The env override is a
// deliberate exception to the no-env-value-overrides rule — DOCKER_HOST is
// the docker CLI's own contract, and a bind source that ignores it breaks
// any host whose daemon serves the socket away from the conventional path
// (rootless Docker under $XDG_RUNTIME_DIR being the common case).
func (c *configImpl) DockerSocketPath() string {
	if path, ok := consts.DockerHostSocketPath(); ok {
		return path
	}
	socket, err := storage.Get[string](c.settings, keyDocker, keySocket)
	if err != nil || socket == "" {
		return consts.DefaultDockerSocketPath
	}
	return socket
}
