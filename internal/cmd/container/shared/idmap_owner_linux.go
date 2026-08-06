//go:build linux

package shared

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// pathOwner reports the on-disk owner IDs of path. It reads the raw stat
// fields rather than going through [os.FileInfo], whose Sys() value is a
// syscall type that silently fails a type assertion to the x/sys shape.
func pathOwner(path string) (uint32, uint32, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return st.Uid, st.Gid, nil
}
