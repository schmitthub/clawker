//go:build darwin

package ebpf

import "context"

// The control plane only ever runs on Linux, but this package compiles into
// the CLI, which builds for darwin too. The BPF filesystem calls below have
// no darwin equivalent — unix.Fsopen and friends do not exist in x/sys
// there — so the platform pair is forced. These are the darwin half: every
// entry point reports the platform rather than pretending.

// CheckKernelSupport reports that darwin has no BPF filesystem.
func CheckKernelSupport() error { return ErrUnsupportedPlatform }

// DelegatedFS is the darwin half of the delegation filesystem context: it
// exists so the package compiles, and every method reports the platform.
type DelegatedFS struct{}

// OpenForDelegation reports that darwin has no BPF filesystem.
func OpenForDelegation() (*DelegatedFS, error) { return nil, ErrUnsupportedPlatform }

// Delegate reports that darwin has no BPF filesystem.
func (t *DelegatedFS) Delegate(_ context.Context, _ string) error { return ErrUnsupportedPlatform }

// Close releases nothing.
func (t *DelegatedFS) Close() {}
