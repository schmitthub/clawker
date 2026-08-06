//go:build !linux

package shared

import "errors"

// pathOwner is the non-Linux half. ID-mapped views are Linux-only and the
// caller checks that before reaching here, so this exists to keep the
// package compiling into the darwin CLI.
func pathOwner(string) (uint32, uint32, error) {
	return 0, 0, errors.New("file ownership IDs are a Linux concept")
}
