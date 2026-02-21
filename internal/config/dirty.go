package config

import (
	"sort"
	"strings"
)

type dirtyNode struct {
	direct   bool
	children map[string]*dirtyNode
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

// dirtyOwnedRoots returns the file-level root keys that are dirty under the
// given scope. With namespaced dirty tracking, the scope is the top-level node
// in the dirty tree and its children are the file-level roots.
func (c *configImpl) dirtyOwnedRoots(scope ConfigScope) []string {
	scopeNode := c.dirtyNodeForPath([]string{string(scope)})
	if scopeNode == nil {
		return nil
	}
	roots := make([]string, 0, len(scopeNode.children))
	for root, node := range scopeNode.children {
		if node.isDirty() {
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots
}
