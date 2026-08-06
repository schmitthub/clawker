//go:build !linux

package idmap

// Mounted is the non-Linux half: idmapped views exist only on Linux hosts,
// so nothing is ever mounted. The package still compiles into the darwin
// CLI, whose create path never engages the rootless arm.
func Mounted(string) bool { return false }
