package stack

import "path/filepath"

// deriveName returns the default registry name for a stack directory: its base
// name. The dir name IS the stack name.
func deriveName(absDir string) string {
	return filepath.Base(absDir)
}

// pathKey returns the dotted clawker.yaml path where a stack's registration
// path is written: stacks.<name>.path.
func pathKey(name string) string {
	return "stacks." + name + ".path"
}

// entryKey returns the dotted clawker.yaml path of the whole stack entry:
// stacks.<name>. A stack entry carries only its path, so removing the
// registration drops the entire entry (removing just the .path leaf would
// leave an empty entry that still shows up as registered).
func entryKey(name string) string {
	return "stacks." + name
}
