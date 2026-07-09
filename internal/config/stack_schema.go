package config

// StackManifest is the parsed stack.yaml — the metadata half of a
// file-backed stack definition. The Dockerfile fragments that accompany it
// are loaded and rendered by internal/bundler; config owns only the
// persisted manifest shape.
type StackManifest struct {
	Description string `yaml:"description" label:"Description" desc:"Human-readable description of what the stack provisions."`
}
