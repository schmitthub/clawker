package build

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
)

// ContentHash computes a SHA-256 hash of the rendered Dockerfile bytes and
// include file contents, returning a 12-character hex prefix. This provides
// a content-addressed identifier for detecting when a rebuild is needed.
func ContentHash(dockerfile []byte, includes []string, workDir string) (string, error) {
	h := sha256.New()

	// Hash the rendered Dockerfile (captures all template-driven changes)
	h.Write(dockerfile)

	// Hash include files in sorted order for determinism
	if len(includes) > 0 {
		sorted := make([]string, len(includes))
		copy(sorted, includes)
		sort.Strings(sorted)

		for _, include := range sorted {
			path := include
			if !filepath.IsAbs(path) {
				path = filepath.Join(workDir, path)
			}

			content, err := os.ReadFile(path)
			if err != nil {
				// Include file missing — use the same framing as present files
				// plus a sentinel so missing files never collide with existing ones.
				h.Write([]byte("\x00" + include + "\x00MISSING\x00"))
				continue
			}

			// Write a separator + filename to avoid collisions between
			// files with identical content but different names.
			h.Write([]byte("\x00" + include + "\x00"))
			h.Write(content)
		}
	}

	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:12], nil
}
