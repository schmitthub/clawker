//go:build !linux

package delegation

// Mounted reports whether path carries a BPF filesystem. Only Linux has one;
// everywhere else — the darwin CLI building a container spec — the answer is
// simply no, which selects the default deployment's bind source.
func Mounted(string) bool { return false }
