package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const dottedLabelKeySentinel = "__clawker_dot__"

func newViperConfig() *viper.Viper {
	return newViperConfigWithEnv(true)
}

func newViperConfigWithEnv(enableAutomaticEnv bool) *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("CLAWKER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	if enableAutomaticEnv {
		bindEnvKeysFromSchema(v)
	}
	SetDefaults(v)
	return v
}

// bindEnvKeysFromSchema walks schema structs via reflection to enumerate all leaf
// mapstructure tag paths, then binds each to its corresponding CLAWKER_* env var.
// This replaces the manually maintained supportedEnvKeys list, eliminating the
// entire class of "added field but forgot to update env key list" bugs.
func bindEnvKeysFromSchema(v *viper.Viper) {
	// Map from scope → schema type to walk.
	schemas := map[ConfigScope]reflect.Type{
		ScopeProject:  reflect.TypeOf(Project{}),
		ScopeSettings: reflect.TypeOf(Settings{}),
		// Registry is excluded — its structure ([]ProjectEntry) is not leaf-key overridable.
	}

	replacer := strings.NewReplacer(".", "_")

	for scope, typ := range schemas {
		leafPaths := collectLeafPaths(typ, "")
		for _, flatKey := range leafPaths {
			root := keyRoot(flatKey)
			owner, ok := keyOwnership[root]
			if !ok {
				// Field's root is not in keyOwnership — skip.
				continue
			}
			if owner != scope {
				// Field appears in this struct but is owned by another scope
				// (e.g. default_image in Project{} is owned by ScopeSettings).
				// It will be bound when that scope's schema is processed.
				continue
			}
			nsKey := string(scope) + "." + flatKey
			envVar := "CLAWKER_" + strings.ToUpper(replacer.Replace(flatKey))
			if err := v.BindEnv(nsKey, envVar); err != nil {
				panic(fmt.Sprintf("config: BindEnv(%q, %q) failed: %v", nsKey, envVar, err))
			}
		}
	}
}

// collectLeafPaths walks a struct type via reflection and returns all dotted
// paths for leaf fields (non-struct, non-embedded). Struct fields are recursed
// into with their mapstructure tag as the path prefix.
func collectLeafPaths(t reflect.Type, prefix string) []string {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	var paths []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("mapstructure")
		if tag == "" || tag == "-" {
			continue
		}

		fullPath := tag
		if prefix != "" {
			fullPath = prefix + "." + tag
		}

		ft := field.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		if ft.Kind() == reflect.Struct {
			// Recurse into struct fields, but skip time.Duration (it's a leaf).
			if ft == reflect.TypeOf(time.Duration(0)) {
				paths = append(paths, fullPath)
			} else {
				paths = append(paths, collectLeafPaths(ft, fullPath)...)
			}
		} else {
			paths = append(paths, fullPath)
		}
	}
	return paths
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
	if err := ensureDefaultConfigFiles(opts); err != nil {
		return nil, err
	}
	c.settingsFile = opts.settingsFile
	c.userProjectConfigFile = opts.userProjectConfigFile
	c.projectRegistryPath = opts.projectRegistryPath
	if err := c.load(opts); err != nil {
		return nil, err
	}
	return c, nil
}

func NewBlankConfig() (Config, error) {
	c := newConfig(newViperConfigWithEnv(false))
	return c, nil
}

// ReadFromString takes a YAML string and returns a Config.
// Useful for testing or constructing configs programmatically.
// Top-level keys are grouped by scope via keyOwnership and merged under their
// namespace prefix (project.*, settings.*, registry.*).
func ReadFromString(str string) (Config, error) {
	rewritten, err := rewriteDottedLabelKeysForViper(str)
	if err != nil {
		return nil, err
	}

	v := viper.New()

	if rewritten != "" {
		if err := checkDuplicateTopLevelKeys(rewritten); err != nil {
			return nil, err
		}

		flat := map[string]any{}
		if err := yaml.Unmarshal([]byte(rewritten), &flat); err != nil {
			return nil, fmt.Errorf("parsing config from string: %w", err)
		}

		// Group top-level keys by owning scope.
		grouped := map[ConfigScope]map[string]any{}
		for key, val := range flat {
			scope, sErr := scopeForKey(key)
			if sErr != nil {
				return nil, fmt.Errorf("unknown config key %q: %w", key, sErr)
			}
			if grouped[scope] == nil {
				grouped[scope] = map[string]any{}
			}
			grouped[scope][key] = val
		}

		for scope, m := range grouped {
			scopeYAML, mErr := yaml.Marshal(m)
			if mErr != nil {
				return nil, fmt.Errorf("marshalling %s config for validation: %w", scope, mErr)
			}
			if err := validateYAMLStrict(string(scopeYAML), schemaForScope(scope)); err != nil {
				return nil, fmt.Errorf("invalid %s config: %w", scope, err)
			}
			if err := v.MergeConfigMap(namespaceMap(m, scope)); err != nil {
				return nil, fmt.Errorf("merging %s config from string: %w", scope, err)
			}
		}
	}

	return newConfig(v), nil
}

func rewriteDottedLabelKeysForViper(str string) (string, error) {
	if str == "" {
		return str, nil
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(str), &root); err != nil {
		return "", fmt.Errorf("parsing config from string: %w", err)
	}

	rewriteDottedLabelKeysInNode(&root)

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", fmt.Errorf("encoding rewritten config: %w", err)
	}

	return string(out), nil
}

func rewriteDottedLabelKeysInNode(node *yaml.Node) {
	if node == nil {
		return
	}

	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]

			if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "labels" && valueNode.Kind == yaml.MappingNode {
				rewriteLabelMapKeys(valueNode)
			}

			rewriteDottedLabelKeysInNode(valueNode)
		}
		return
	}

	for _, child := range node.Content {
		rewriteDottedLabelKeysInNode(child)
	}
}

func rewriteLabelMapKeys(labelsNode *yaml.Node) {
	for i := 0; i+1 < len(labelsNode.Content); i += 2 {
		labelKey := labelsNode.Content[i]
		if labelKey.Kind != yaml.ScalarNode {
			continue
		}
		if !strings.Contains(labelKey.Value, ".") {
			continue
		}

		labelKey.Value = strings.ReplaceAll(labelKey.Value, ".", dottedLabelKeySentinel)
	}
}

// checkDuplicateTopLevelKeys parses YAML as a yaml.Node and checks for
// duplicate top-level mapping keys. yaml.Unmarshal silently uses the last
// value for duplicate keys, which can mask configuration errors.
func checkDuplicateTopLevelKeys(content string) error {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return fmt.Errorf("parsing config from string: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	seen := make(map[string]bool)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keyNode := mapping.Content[i]
		if keyNode.Kind != yaml.ScalarNode {
			continue
		}
		if seen[keyNode.Value] {
			return fmt.Errorf("duplicate key %q in config (line %d)", keyNode.Value, keyNode.Line)
		}
		seen[keyNode.Value] = true
	}
	return nil
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
		scope  ConfigScope
	}{
		{path: opts.settingsFile, schema: &Settings{}, scope: ScopeSettings},
		{path: opts.userProjectConfigFile, schema: &Project{}, scope: ScopeProject},
		{path: opts.projectRegistryPath, schema: &ProjectRegistry{}, scope: ScopeRegistry},
	}

	for _, f := range files {
		if err := validateConfigFileExact(f.path, f.schema); err != nil {
			return err
		}

		raw, err := readFileToMap(f.path)
		if err != nil {
			return fmt.Errorf("loading config %s: %w", f.path, err)
		}

		if err := c.v.MergeConfigMap(namespaceMap(raw, f.scope)); err != nil {
			return fmt.Errorf("merging config %s: %w", f.path, err)
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

	projectFile := filepath.Join(root, clawkerProjectConfigFileName)
	if err := validateConfigFileExact(projectFile, &Project{}); err != nil {
		return err
	}
	raw, err := readFileToMap(projectFile)
	if err != nil {
		return fmt.Errorf("loading project config for root %s: %w", root, err)
	}
	if err := c.v.MergeConfigMap(namespaceMap(raw, ScopeProject)); err != nil {
		return fmt.Errorf("merging project config for root %s: %w", root, err)
	}
	c.projectConfigFile = projectFile

	return nil
}

// readFileToMap reads a YAML file into a flat map[string]any.
// Returns an empty map for empty files.
func readFileToMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	m := map[string]any{}
	if len(data) == 0 {
		return m, nil
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

func ensureDefaultConfigFiles(opts loadOptions) error {
	// All config files share the same parent directory — ensure it exists
	// before attempting file locks or writes.
	dir := filepath.Dir(opts.settingsFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory %s: %w", dir, err)
	}

	files := []struct {
		path    string
		content string
	}{
		{path: opts.settingsFile, content: DefaultSettingsYAML},
		{path: opts.userProjectConfigFile, content: DefaultConfigYAML},
		{path: opts.projectRegistryPath, content: DefaultRegistryYAML},
	}

	for _, file := range files {
		if err := writeIfMissingLocked(file.path, []byte(file.content)); err != nil {
			return fmt.Errorf("ensuring default config file %s: %w", file.path, err)
		}
	}

	return nil
}

// validateYAMLStrict validates YAML content against a Go struct schema using
// yaml.v3 strict decoding. Catches type mismatches (map where list expected),
// unknown fields, and structural violations — all derived from struct tags.
func validateYAMLStrict(yamlContent string, schema any) error {
	dec := yaml.NewDecoder(strings.NewReader(yamlContent))
	dec.KnownFields(true)
	if err := dec.Decode(schema); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func validateConfigFileExact(path string, schema any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config %s: %w", path, err)
	}
	if err := validateYAMLStrict(string(content), schema); err != nil {
		return fmt.Errorf("invalid config %s: %w", path, err)
	}
	return nil
}
