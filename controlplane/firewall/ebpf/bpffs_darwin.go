//go:build darwin

package ebpf

import "context"

// The control plane only ever runs on Linux, but this package compiles into
// the CLI, which builds for darwin too. The BPF filesystem calls below have
// no darwin equivalent — unix.Fsopen, unix.Mount and friends do not exist
// in x/sys there — so the platform pair is forced. These are the darwin
// half: every entry point reports the platform rather than pretending.

// CheckKernelSupport reports that darwin has no BPF filesystem.
func CheckKernelSupport() error { return ErrUnsupportedPlatform }

// PinFSMounted reports false: there is no BPF filesystem to mount.
func PinFSMounted(_ string) bool { return false }

// MountPinFS reports that darwin has no BPF filesystem.
func MountPinFS(_ string, _, _ int) error { return ErrUnsupportedPlatform }

// AwaitPinFS reports that darwin has no BPF filesystem.
func AwaitPinFS(_ context.Context, _ string) error { return ErrUnsupportedPlatform }

// TokenFS is the darwin half of the token filesystem: it exists so the
// package compiles, and every method reports the platform.
type TokenFS struct{}

// OpenTokenFS reports that darwin has no BPF filesystem.
func OpenTokenFS(_ string) (*TokenFS, error) { return nil, ErrUnsupportedPlatform }

// Delegate reports that darwin has no BPF filesystem.
func (t *TokenFS) Delegate(_ context.Context, _ string) error { return ErrUnsupportedPlatform }

// Mount reports that darwin has no BPF filesystem.
func (t *TokenFS) Mount() error { return ErrUnsupportedPlatform }

// Delegated reports false: nothing was ever delegated.
func (t *TokenFS) Delegated() bool { return false }

// Close releases nothing.
func (t *TokenFS) Close() {}
