package storage

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// layer holds the parsed node tree of a single discovered file. The node — not
// a map — is the layer's representation, so a layer carries its own comments
// and a write back to its file preserves them.
type layer struct {
	path     string     // absolute path to the source file (empty for the virtual layer)
	filename string     // which filename matched (e.g., "clawker.yaml")
	node     *yaml.Node // root mapping node from this file only (comments intact)
	virtual  bool       // true for the lowest-priority defaults/seed layer (no backing file)
}

// parseNodeFile reads a YAML file into its root mapping node, preserving
// comments. A read error (including a missing file) is returned to the caller;
// an empty file yields an empty mapping node.
func parseNodeFile(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("storage: reading %s: %w", path, err)
	}
	node, err := rootMapping(data)
	if err != nil {
		return nil, fmt.Errorf("storage: parsing %s: %w", path, err)
	}
	return node, nil
}

// loadNode reads a file into its root mapping node. Migrations are applied
// later, on the store, via Store.applyMigrations — not here.
func loadNode(path string) (*yaml.Node, error) {
	return parseNodeFile(path)
}

// decodeNode deserializes the merged node tree into a typed struct T. An empty
// tree yields the zero value of T.
func decodeNode[T Schema](node *yaml.Node) (*T, error) {
	var result T
	if node == nil || len(node.Content) == 0 {
		return &result, nil
	}
	if err := node.Decode(&result); err != nil {
		return nil, fmt.Errorf("storage: decoding merged node to struct: %w", err)
	}
	return &result, nil
}
