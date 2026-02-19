// Package config provides types for interacting with clawker configuration files.
// It loads clawker.yaml (project) and settings.yaml (user) into one merged
// in-memory Config backed by viper, with key-path traversal via Get/Set/Keys/Remove.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

var invalidKeysRe = regexp.MustCompile(`'([^']+)' has invalid keys: (.+)$`)

const (
	appData       = "AppData"
	xdgConfigHome = "XDG_CONFIG_HOME"
)

// Config is the public configuration contract.
// Add methods here as the config contract grows.
type Config interface {
	Logging() map[string]any
	Project() *Project
	Settings() Settings
	LoggingConfig() LoggingConfig
	MonitoringConfig() MonitoringConfig
	Get(key string) (any, error)
	Set(key string, value any) error
	Write(opts WriteOptions) error
	Watch(onChange func(fsnotify.Event)) error
	Domain() string
	LabelDomain() string
	ConfigDirEnvVar() string
	MonitorSubdir() (string, error)
	BuildSubdir() (string, error)
	DockerfilesSubdir() (string, error)
	ClawkerNetwork() string
	LogsSubdir() (string, error)
	BridgesSubdir() (string, error)
	ShareSubdir() (string, error)
	LabelPrefix() string
	LabelManaged() string
	LabelMonitoringStack() string
	LabelProject() string
	LabelAgent() string
	LabelVersion() string
	LabelImage() string
	LabelCreated() string
	LabelWorkdir() string
	LabelPurpose() string
	LabelTestName() string
	LabelBaseImage() string
	LabelFlavor() string
	LabelTest() string
	LabelE2ETest() string
	ManagedLabelValue() string
	EngineLabelPrefix() string
	EngineManagedLabel() string
	ContainerUID() int
	ContainerGID() int
	RequiredFirewallDomains() []string
	GetProjectRoot() (string, error)
}

var ErrNotInProject = errors.New("current directory is not within a configured project root")

type configImpl struct {
	v *viper.Viper

	settingsFile          string
	userProjectConfigFile string
	projectRegistryPath   string
	projectConfigFile     string
	dirty                 *dirtyNode

	mu sync.RWMutex
}

type dirtyNode struct {
	direct   bool
	children map[string]*dirtyNode
}

type ConfigScope string

const (
	ScopeSettings ConfigScope = "settings"
	ScopeProject  ConfigScope = "project"
	ScopeRegistry ConfigScope = "registry"
)

var keyOwnership = map[string]ConfigScope{
	"logging":       ScopeSettings,
	"monitoring":    ScopeSettings,
	"default_image": ScopeSettings,

	"projects": ScopeRegistry,

	"version":   ScopeProject,
	"project":   ScopeProject,
	"build":     ScopeProject,
	"agent":     ScopeProject,
	"workspace": ScopeProject,
	"security":  ScopeProject,
	"loop":      ScopeProject,
}

// WriteOptions controls how Write persists the current in-memory configuration.
//
// Path selects the target file:
//   - Empty: write to the currently loaded/configured Viper target.
//   - Non-empty: write to this explicit filesystem path.
//
// Safe controls overwrite behavior:
//   - false: create or overwrite (truncate) the target file.
//   - true: create only; return an error if the target already exists.
//
// Scope constrains persistence to a logical config file owner.
//   - Empty: selective dirty-root persistence to owning files (or explicit Path write).
//   - settings/project/registry: persist only dirty roots owned by that scope.
//
// Key optionally persists a single key.
//   - Empty: scope/default behavior applies.
//   - Non-empty: write only this key when dirty (scope inferred from ownership map when Scope is empty).
type WriteOptions struct {
	Path  string
	Safe  bool
	Scope ConfigScope
	Key   string
}

func newViperConfig() *viper.Viper {
	return newViperConfigWithEnv(true)
}

func newViperConfigWithEnv(enableAutomaticEnv bool) *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("CLAWKER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	if enableAutomaticEnv {
		bindSupportedEnvKeys(v)
	}
	setDefaults(v)
	return v
}

func bindSupportedEnvKeys(v *viper.Viper) {
	for _, key := range supportedEnvKeys {
		_ = v.BindEnv(key)
	}
}

var supportedEnvKeys = []string{
	"default_image",
	"build.image",
	"build.dockerfile",
	"build.packages",
	"build.context",
	"build.build_args",
	"agent.includes",
	"agent.env_file",
	"agent.from_env",
	"agent.env",
	"agent.memory",
	"agent.editor",
	"agent.visual",
	"agent.shell",
	"agent.claude_code.config.strategy",
	"agent.claude_code.use_host_auth",
	"agent.enable_shared_dir",
	"agent.post_init",
	"workspace.remote_path",
	"workspace.default_mode",
	"security.firewall.enable",
	"security.firewall.add_domains",
	"security.firewall.ip_range_sources",
	"security.docker_socket",
	"security.cap_add",
	"security.enable_host_proxy",
	"security.git_credentials.forward_https",
	"security.git_credentials.forward_ssh",
	"security.git_credentials.forward_gpg",
	"security.git_credentials.copy_git_config",
	"loop.max_loops",
	"loop.stagnation_threshold",
	"loop.timeout_minutes",
	"loop.calls_per_hour",
	"loop.completion_threshold",
	"loop.session_expiration_hours",
	"loop.same_error_threshold",
	"loop.output_decline_threshold",
	"loop.max_consecutive_test_loops",
	"loop.loop_delay_seconds",
	"loop.safety_completion_threshold",
	"loop.skip_permissions",
	"loop.hooks_file",
	"loop.append_system_prompt",
	"logging.file_enabled",
	"logging.max_size_mb",
	"logging.max_age_days",
	"logging.max_backups",
	"logging.compress",
	"logging.otel.enabled",
	"logging.otel.timeout_seconds",
	"logging.otel.max_queue_size",
	"logging.otel.export_interval_seconds",
	"monitoring.otel_collector_endpoint",
	"monitoring.otel_collector_port",
	"monitoring.otel_collector_host",
	"monitoring.otel_collector_internal",
	"monitoring.otel_grpc_port",
	"monitoring.loki_port",
	"monitoring.prometheus_port",
	"monitoring.jaeger_port",
	"monitoring.grafana_port",
	"monitoring.prometheus_metrics_port",
	"monitoring.telemetry.metrics_path",
	"monitoring.telemetry.logs_path",
	"monitoring.telemetry.metric_export_interval_ms",
	"monitoring.telemetry.logs_export_interval_ms",
	"monitoring.telemetry.log_tool_details",
	"monitoring.telemetry.log_user_prompts",
	"monitoring.telemetry.include_account_uuid",
	"monitoring.telemetry.include_session_id",
}

func newConfig(v *viper.Viper) *configImpl {
	return &configImpl{
		v:     v,
		dirty: newDirtyNode(),
	}
}

func newDirtyNode() *dirtyNode {
	return &dirtyNode{}
}

func (n *dirtyNode) isDirty() bool {
	if n == nil {
		return false
	}
	if n.direct {
		return true
	}
	for _, child := range n.children {
		if child.isDirty() {
			return true
		}
	}
	return false
}

func (n *dirtyNode) ensureChild(key string) *dirtyNode {
	if n.children == nil {
		n.children = make(map[string]*dirtyNode)
	}
	child, ok := n.children[key]
	if !ok {
		child = newDirtyNode()
		n.children[key] = child
	}
	return child
}

func splitKeyPath(key string) []string {
	raw := strings.Split(key, ".")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func (c *configImpl) markDirtyPath(key string) {
	parts := splitKeyPath(key)
	if len(parts) == 0 {
		return
	}

	node := c.dirty
	for i, part := range parts {
		node = node.ensureChild(part)
		if i == len(parts)-1 {
			node.direct = true
		}
	}
}

func (c *configImpl) dirtyNodeForPath(parts []string) *dirtyNode {
	node := c.dirty
	for _, part := range parts {
		if node == nil || node.children == nil {
			return nil
		}
		next, ok := node.children[part]
		if !ok {
			return nil
		}
		node = next
	}
	return node
}

func (c *configImpl) isDirtyPath(key string) bool {
	parts := splitKeyPath(key)
	if len(parts) == 0 {
		return false
	}
	node := c.dirtyNodeForPath(parts)
	return node != nil && node.isDirty()
}

func clearPathRecursive(node *dirtyNode, parts []string) bool {
	if node == nil {
		return false
	}

	if len(parts) == 0 {
		node.direct = false
		node.children = nil
		return node.isDirty()
	}

	next, ok := node.children[parts[0]]
	if !ok {
		return node.isDirty()
	}

	if !clearPathRecursive(next, parts[1:]) {
		delete(node.children, parts[0])
		if len(node.children) == 0 {
			node.children = nil
		}
	}

	return node.isDirty()
}

func (c *configImpl) clearDirtyPath(key string) {
	parts := splitKeyPath(key)
	if len(parts) == 0 {
		return
	}
	_ = clearPathRecursive(c.dirty, parts)
}

func (c *configImpl) dirtyOwnedRoots(scope ConfigScope) []string {
	roots := ownedRoots(scope)
	dirtyRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		if c.isDirtyPath(root) {
			dirtyRoots = append(dirtyRoots, root)
		}
	}
	return dirtyRoots
}

func (c *configImpl) writeDirtyRootsForScope(scope ConfigScope, overridePath string, safe bool) error {
	dirtyRoots := c.dirtyOwnedRoots(scope)
	if len(dirtyRoots) == 0 {
		return nil
	}

	targetPath, err := c.resolveTargetPath(scope, overridePath)
	if err != nil {
		return err
	}

	if err := writeRootsToFile(targetPath, dirtyRoots, c.v, safe); err != nil {
		return err
	}

	for _, root := range dirtyRoots {
		c.clearDirtyPath(root)
	}
	return nil
}

// NewConfig loads all clawker configuration files into a single Config.
// Precedence (highest to lowest): project config > project registry > user config > settings
func NewConfig() (Config, error) {
	c := newConfig(newViperConfig())
	opts := loadOptions{
		settingsFile:          settingsConfigFile(),
		userProjectConfigFile: userProjectConfigFile(),
		projectRegistryPath:   projectRegistryPath(),
	}
	c.settingsFile = opts.settingsFile
	c.userProjectConfigFile = opts.userProjectConfigFile
	c.projectRegistryPath = opts.projectRegistryPath
	if err := c.load(opts); err != nil {
		return nil, err
	}
	return c, nil
}

// ReadFromString takes a YAML string and returns a Config.
// Useful for testing or constructing configs programmatically.
func ReadFromString(str string) (Config, error) {
	if err := validateProjectYAMLString(str); err != nil {
		return nil, err
	}

	v := newViperConfigWithEnv(false)
	v.SetConfigType("yaml")
	if str != "" {
		err := v.ReadConfig(strings.NewReader(str))
		if err != nil {
			return nil, fmt.Errorf("parsing config from string: %w", err)
		}
	}
	return newConfig(v), nil
}

func (c *configImpl) RequiredFirewallDomains() []string {
	return append([]string(nil), requiredFirewallDomains...)
}

func (c *configImpl) Logging() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.v.GetStringMap("logging")
}

func (c *configImpl) Project() *Project {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p := &Project{}
	_ = c.v.Unmarshal(p)
	return p
}

func (c *configImpl) Settings() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Settings{
		Logging: LoggingConfig{
			FileEnabled: boolPtr(c.v.GetBool("logging.file_enabled")),
			MaxSizeMB:   c.v.GetInt("logging.max_size_mb"),
			MaxAgeDays:  c.v.GetInt("logging.max_age_days"),
			MaxBackups:  c.v.GetInt("logging.max_backups"),
			Compress:    boolPtr(c.v.GetBool("logging.compress")),
			Otel: OtelConfig{
				Enabled:               boolPtr(c.v.GetBool("logging.otel.enabled")),
				TimeoutSeconds:        c.v.GetInt("logging.otel.timeout_seconds"),
				MaxQueueSize:          c.v.GetInt("logging.otel.max_queue_size"),
				ExportIntervalSeconds: c.v.GetInt("logging.otel.export_interval_seconds"),
			},
		},
		Monitoring: MonitoringConfig{
			OtelCollectorEndpoint: c.v.GetString("monitoring.otel_collector_endpoint"),
			OtelCollectorPort:     c.v.GetInt("monitoring.otel_collector_port"),
			OtelCollectorHost:     c.v.GetString("monitoring.otel_collector_host"),
			OtelCollectorInternal: c.v.GetString("monitoring.otel_collector_internal"),
			OtelGRPCPort:          c.v.GetInt("monitoring.otel_grpc_port"),
			LokiPort:              c.v.GetInt("monitoring.loki_port"),
			PrometheusPort:        c.v.GetInt("monitoring.prometheus_port"),
			JaegerPort:            c.v.GetInt("monitoring.jaeger_port"),
			GrafanaPort:           c.v.GetInt("monitoring.grafana_port"),
			PrometheusMetricsPort: c.v.GetInt("monitoring.prometheus_metrics_port"),
			Telemetry: TelemetryConfig{
				MetricsPath:            c.v.GetString("monitoring.telemetry.metrics_path"),
				LogsPath:               c.v.GetString("monitoring.telemetry.logs_path"),
				MetricExportIntervalMs: c.v.GetInt("monitoring.telemetry.metric_export_interval_ms"),
				LogsExportIntervalMs:   c.v.GetInt("monitoring.telemetry.logs_export_interval_ms"),
				LogToolDetails:         boolPtr(c.v.GetBool("monitoring.telemetry.log_tool_details")),
				LogUserPrompts:         boolPtr(c.v.GetBool("monitoring.telemetry.log_user_prompts")),
				IncludeAccountUUID:     boolPtr(c.v.GetBool("monitoring.telemetry.include_account_uuid")),
				IncludeSessionID:       boolPtr(c.v.GetBool("monitoring.telemetry.include_session_id")),
			},
		},
		DefaultImage: c.v.GetString("default_image"),
	}
}

func (c *configImpl) LoggingConfig() LoggingConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return LoggingConfig{
		FileEnabled: boolPtr(c.v.GetBool("logging.file_enabled")),
		MaxSizeMB:   c.v.GetInt("logging.max_size_mb"),
		MaxAgeDays:  c.v.GetInt("logging.max_age_days"),
		MaxBackups:  c.v.GetInt("logging.max_backups"),
		Compress:    boolPtr(c.v.GetBool("logging.compress")),
		Otel: OtelConfig{
			Enabled:               boolPtr(c.v.GetBool("logging.otel.enabled")),
			TimeoutSeconds:        c.v.GetInt("logging.otel.timeout_seconds"),
			MaxQueueSize:          c.v.GetInt("logging.otel.max_queue_size"),
			ExportIntervalSeconds: c.v.GetInt("logging.otel.export_interval_seconds"),
		},
	}
}

func (c *configImpl) MonitoringConfig() MonitoringConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return MonitoringConfig{
		OtelCollectorEndpoint: c.v.GetString("monitoring.otel_collector_endpoint"),
		OtelCollectorPort:     c.v.GetInt("monitoring.otel_collector_port"),
		OtelCollectorHost:     c.v.GetString("monitoring.otel_collector_host"),
		OtelCollectorInternal: c.v.GetString("monitoring.otel_collector_internal"),
		OtelGRPCPort:          c.v.GetInt("monitoring.otel_grpc_port"),
		LokiPort:              c.v.GetInt("monitoring.loki_port"),
		PrometheusPort:        c.v.GetInt("monitoring.prometheus_port"),
		JaegerPort:            c.v.GetInt("monitoring.jaeger_port"),
		GrafanaPort:           c.v.GetInt("monitoring.grafana_port"),
		PrometheusMetricsPort: c.v.GetInt("monitoring.prometheus_metrics_port"),
		Telemetry: TelemetryConfig{
			MetricsPath:            c.v.GetString("monitoring.telemetry.metrics_path"),
			LogsPath:               c.v.GetString("monitoring.telemetry.logs_path"),
			MetricExportIntervalMs: c.v.GetInt("monitoring.telemetry.metric_export_interval_ms"),
			LogsExportIntervalMs:   c.v.GetInt("monitoring.telemetry.logs_export_interval_ms"),
			LogToolDetails:         boolPtr(c.v.GetBool("monitoring.telemetry.log_tool_details")),
			LogUserPrompts:         boolPtr(c.v.GetBool("monitoring.telemetry.log_user_prompts")),
			IncludeAccountUUID:     boolPtr(c.v.GetBool("monitoring.telemetry.include_account_uuid")),
			IncludeSessionID:       boolPtr(c.v.GetBool("monitoring.telemetry.include_session_id")),
		},
	}
}

// Get returns the value for a dotted config key using Viper's key lookup.
//
// It returns KeyNotFoundError when the key is not set in the merged
// configuration state (including defaults and environment overrides).
// Access is protected by an RWMutex for safe concurrent reads.
func (c *configImpl) Get(key string) (any, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.v.IsSet(key) {
		return nil, &KeyNotFoundError{Key: key}
	}

	return c.v.Get(key), nil
}

// Set updates a dotted config key in-memory and marks it dirty.
//
// Ownership is resolved via the explicit key→file ownership map so later writes
// can route to the correct file scope.
// Access is protected by an RWMutex for safe concurrent writes.
func (c *configImpl) Set(key string, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, err := scopeForKey(key); err != nil {
		return err
	}

	c.v.Set(key, value)
	c.markDirtyPath(key)
	return nil
}

// Write persists the current in-memory configuration using WriteOptions.
//
// Behavior summary:
//   - Key set: persist only that dirty key (scope inferred/validated).
//   - Scope set: persist only dirty owned roots in that scope.
//   - Path empty: persist dirty roots to owning files across all scopes.
//   - Path set (without Key/Scope): write full merged config to explicit path.
//
// Access is protected by an RWMutex for safe concurrent writes.
func (c *configImpl) Write(opts WriteOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if opts.Key != "" {
		inferredScope, err := scopeForKey(opts.Key)
		if err != nil {
			return err
		}

		scope := inferredScope
		if opts.Scope != "" {
			if opts.Scope != inferredScope {
				return fmt.Errorf("key %q belongs to %q scope, not %q", opts.Key, inferredScope, opts.Scope)
			}
			scope = opts.Scope
		}

		if !c.isDirtyPath(opts.Key) {
			return nil
		}

		targetPath, err := c.resolveTargetPath(scope, opts.Path)
		if err != nil {
			return err
		}

		if !c.v.IsSet(opts.Key) {
			return &KeyNotFoundError{Key: opts.Key}
		}

		value := c.v.Get(opts.Key)
		if err := writeKeyToFile(targetPath, opts.Key, value, opts.Safe); err != nil {
			return err
		}
		c.clearDirtyPath(opts.Key)
		return nil
	}

	if opts.Scope != "" {
		return c.writeDirtyRootsForScope(opts.Scope, opts.Path, opts.Safe)
	}

	if opts.Path == "" {
		scopes := []ConfigScope{ScopeSettings, ScopeRegistry, ScopeProject}
		for _, scope := range scopes {
			if err := c.writeDirtyRootsForScope(scope, "", opts.Safe); err != nil {
				return err
			}
		}
		return nil
	}

	if opts.Safe {
		return c.v.SafeWriteConfigAs(opts.Path)
	}
	return c.v.WriteConfigAs(opts.Path)
}

// Watch enables file watching for the currently loaded config file.
//
// If onChange is non-nil, it is registered with Viper's OnConfigChange hook.
// The caller must ensure config paths/files were configured before watching;
// this method returns an error when no config file is currently in use.
// Access is protected by an RWMutex for safe watcher setup.
func (c *configImpl) Watch(onChange func(fsnotify.Event)) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.v.ConfigFileUsed() == "" {
		return fmt.Errorf("watch config requires a loaded config file")
	}

	if onChange != nil {
		c.v.OnConfigChange(onChange)
	}
	c.v.WatchConfig()
	return nil
}

func scopeForKey(key string) (ConfigScope, error) {
	root := keyRoot(key)
	scope, ok := keyOwnership[root]
	if !ok {
		return "", fmt.Errorf("no ownership mapping for key: %s", key)
	}
	return scope, nil
}

func keyRoot(key string) string {
	parts := strings.SplitN(key, ".", 2)
	return parts[0]
}

func (c *configImpl) resolveTargetPath(scope ConfigScope, overridePath string) (string, error) {
	if overridePath != "" {
		return overridePath, nil
	}

	switch scope {
	case ScopeSettings:
		if c.settingsFile == "" {
			return "", fmt.Errorf("settings file path is not configured")
		}
		return c.settingsFile, nil
	case ScopeRegistry:
		if c.projectRegistryPath == "" {
			return "", fmt.Errorf("project registry path is not configured")
		}
		return c.projectRegistryPath, nil
	case ScopeProject:
		if c.projectConfigFile != "" {
			return c.projectConfigFile, nil
		}

		root, err := c.projectRootFromCurrentDir()
		if err == nil {
			return filepath.Join(root, "clawker.yaml"), nil
		}
		if errors.Is(err, ErrNotInProject) {
			if c.userProjectConfigFile == "" {
				return "", fmt.Errorf("project config path is not configured")
			}
			return c.userProjectConfigFile, nil
		}
		return "", err
	default:
		return "", fmt.Errorf("invalid write scope: %s", scope)
	}
}

func ownedRoots(scope ConfigScope) []string {
	roots := make([]string, 0)
	for root, owner := range keyOwnership {
		if owner == scope {
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots
}

func writeKeyToFile(path, key string, value any, safe bool) error {
	v, exists, err := openConfigForWrite(path)
	if err != nil {
		return err
	}

	v.Set(key, value)
	if safe {
		if exists {
			return fmt.Errorf("config file already exists: %s", path)
		}
		return v.SafeWriteConfigAs(path)
	}
	return v.WriteConfigAs(path)
}

func writeRootsToFile(path string, roots []string, source *viper.Viper, safe bool) error {
	v, exists, err := openConfigForWrite(path)
	if err != nil {
		return err
	}

	for _, root := range roots {
		if source.IsSet(root) {
			v.Set(root, source.Get(root))
		}
	}

	if safe {
		if exists {
			return fmt.Errorf("config file already exists: %s", path)
		}
		return v.SafeWriteConfigAs(path)
	}
	return v.WriteConfigAs(path)
}

func openConfigForWrite(path string) (*viper.Viper, bool, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(path)

	exists := true
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			exists = false
		} else {
			return nil, false, fmt.Errorf("failed to stat config %s: %w", path, err)
		}
	}

	if exists {
		if err := v.ReadInConfig(); err != nil {
			return nil, false, fmt.Errorf("loading config %s: %w", path, err)
		}
	}

	return v, exists, nil
}

func (c *configImpl) GetProjectRoot() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.projectRootFromCurrentDir()
}

func (c *configImpl) projectRootFromCurrentDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting cwd: %w", err)
	}
	cwd = filepath.Clean(cwd)

	projects := c.v.GetStringMap("projects")
	bestMatch := ""
	for key := range projects {
		root := filepath.Clean(c.v.GetString(fmt.Sprintf("projects.%s.root", key)))
		rel, relErr := filepath.Rel(root, cwd)
		if relErr != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			if len(root) > len(bestMatch) {
				bestMatch = root
			}
		}
	}

	if bestMatch == "" {
		return "", fmt.Errorf("%w: %s", ErrNotInProject, cwd)
	}

	return bestMatch, nil
}

type loadOptions struct {
	settingsFile          string
	userProjectConfigFile string
	projectRegistryPath   string
}

func (c *configImpl) load(opts loadOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	files := []struct {
		path   string
		schema any
	}{
		{path: opts.settingsFile, schema: &Settings{}},
		{path: opts.userProjectConfigFile, schema: &Project{}},
		{path: opts.projectRegistryPath, schema: &projectRegistryValidation{}},
	}

	for i, f := range files {
		if err := validateConfigFileExact(f.path, f.schema); err != nil {
			return err
		}

		c.v.SetConfigFile(f.path)
		var err error
		if i == 0 {
			err = c.v.ReadInConfig()
		} else {
			err = c.v.MergeInConfig()
		}
		if err != nil {
			return fmt.Errorf("loading config %s: %w", f.path, err)
		}
	}

	return c.mergeProjectConfigUnsafe()
}

func (c *configImpl) mergeProjectConfig() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mergeProjectConfigUnsafe()
}

func (c *configImpl) mergeProjectConfigUnsafe() error {
	root, err := c.projectRootFromCurrentDir()
	if err != nil {
		if errors.Is(err, ErrNotInProject) {
			c.projectConfigFile = ""
			return nil
		}
		return err
	}

	projectFile := filepath.Join(root, "clawker.yaml")
	c.v.SetConfigFile(projectFile)
	if err := validateConfigFileExact(projectFile, &Project{}); err != nil {
		return err
	}
	if err := c.v.MergeInConfig(); err != nil {
		return fmt.Errorf("loading project config for root %s: %w", root, err)
	}
	c.projectConfigFile = projectFile

	return nil
}

// ConfigDir returns the clawker config directory.
func ConfigDir() string {
	if a := os.Getenv(clawkerConfigDirEnv); a != "" {
		return a
	}
	if b := os.Getenv(xdgConfigHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv(appData); c != "" {
			return filepath.Join(c, "clawker")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".config", "clawker")
}

func settingsConfigFile() string {
	return filepath.Join(ConfigDir(), "settings.yaml")
}

func userProjectConfigFile() string {
	return filepath.Join(ConfigDir(), "clawker.yaml")
}

func projectRegistryPath() string {
	return filepath.Join(ConfigDir(), "projects.yaml")
}

func boolPtr(v bool) *bool {
	b := v
	return &b
}

func validateProjectYAMLString(str string) error {
	if str == "" {
		return nil
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(str)); err != nil {
		return fmt.Errorf("parsing config from string: %w", err)
	}

	if err := v.UnmarshalExact(&readFromStringValidation{}); err != nil {
		return fmt.Errorf("invalid project config: %s", formatDecodeError(err))
	}

	return nil
}

func validateConfigFileExact(path string, schema any) error {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("loading config %s: %w", path, err)
	}

	if err := v.UnmarshalExact(schema); err != nil {
		return fmt.Errorf("invalid config %s: %s", path, formatDecodeError(err))
	}

	return nil
}

func formatDecodeError(err error) string {
	msg := err.Error()
	match := invalidKeysRe.FindStringSubmatch(msg)
	if len(match) != 3 {
		return msg
	}

	parent := strings.TrimSpace(match[1])
	keys := strings.Split(match[2], ",")
	paths := make([]string, 0, len(keys))
	for _, key := range keys {
		k := strings.TrimSpace(key)
		if parent == "" || parent == "<root>" {
			paths = append(paths, k)
			continue
		}
		paths = append(paths, parent+"."+k)
	}

	return "unknown keys: " + strings.Join(paths, ", ")
}

// readFromStringValidation is a permissive root schema for ReadFromString.
// It validates unknown keys for known config roots while allowing ad-hoc
// projects maps used in tests.
type readFromStringValidation struct {
	Version      string           `mapstructure:"version"`
	Project      string           `mapstructure:"project"`
	DefaultImage string           `mapstructure:"default_image"`
	Build        BuildConfig      `mapstructure:"build"`
	Agent        AgentConfig      `mapstructure:"agent"`
	Workspace    WorkspaceConfig  `mapstructure:"workspace"`
	Security     SecurityConfig   `mapstructure:"security"`
	Loop         *LoopConfig      `mapstructure:"loop"`
	Logging      LoggingConfig    `mapstructure:"logging"`
	Monitoring   MonitoringConfig `mapstructure:"monitoring"`
	Projects     map[string]any   `mapstructure:"projects"`
}

// projectRegistryValidation allows legacy worktree values while still
// rejecting unknown keys on registry/project entries.
type projectRegistryValidation struct {
	Projects map[string]projectEntryValidation `mapstructure:"projects"`
}

type projectEntryValidation struct {
	Name      string         `mapstructure:"name"`
	Root      string         `mapstructure:"root"`
	Worktrees map[string]any `mapstructure:"worktrees"`
}
