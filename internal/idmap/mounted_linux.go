//go:build linux

package idmap

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// mountInfoPath is the caller's own mount table. The view is attached in the
// host mount namespace, which is where the CLI runs — a mount present here
// is one the daemon's namespace inherits.
const mountInfoPath = "/proc/self/mountinfo"

// mountPointField is the 0-based index of the mount-point column in a
// mountinfo row.
const mountPointField = 4

// maxMountInfoLine caps one mountinfo row. Path fields are PATH_MAX-bounded
// even after octal escaping, but the kernel emits super options verbatim —
// an overlay lowerdir list is the row that grows — and bufio's 64K default
// abandons the ENTIRE scan at the first row past it, hiding every entry
// after the long line. 1 MiB clears any row the kernel can emit.
const maxMountInfoLine = 1 << 20

// initialScanBuf is the starting token buffer; bufio grows it toward
// maxMountInfoLine on demand. Sized so common multi-kilobyte overlay rows
// never reallocate.
const initialScanBuf = 64 << 10

// Mounted reports whether path currently carries a mount. It is the state
// check the create/start paths use to decide whether the privileged one-shot
// must (re)attach the view: after a reboot the directory survives but the
// mount is gone, and binding the bare directory would hand the container an
// empty workspace.
func Mounted(path string) bool {
	f, err := os.Open(mountInfoPath)
	if err != nil {
		return false
	}
	// Read-only descriptor; a close failure loses nothing.
	defer f.Close()

	want := filepath.Clean(path)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, initialScanBuf), maxMountInfoLine)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) <= mountPointField {
			continue
		}
		if unescapeMountPath(fields[mountPointField]) == want {
			return true
		}
	}
	// A scan error truncates the walk partway through the table (a row past
	// maxMountInfoLine, or a read failure); treat as not mounted so the
	// caller re-attaches rather than trusting unknown state.
	return false
}

// unescapeMountPath decodes the octal escapes (\040 and friends) the kernel
// uses for whitespace in mountinfo paths.
func unescapeMountPath(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
