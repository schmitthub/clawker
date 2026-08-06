//go:build !linux

// The BPF filesystem is a Linux facility and this program is only ever built
// for Linux (see the Makefile target). The non-Linux half exists so the
// package still compiles when the repository is built on a developer's
// machine — `go build ./...` on darwin would otherwise fail with "build
// constraints exclude all Go files".
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "bpffs-delegate: the BPF filesystem is Linux-only")
	os.Exit(1)
}
