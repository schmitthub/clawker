package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveVersion(t *testing.T) {
	t.Run("manifest version wins over sha", func(t *testing.T) {
		v, err := resolveVersion("1.2.0", "abc123")
		require.NoError(t, err)
		assert.Equal(t, "1.2.0", v)
	})

	t.Run("falls back to resolved sha", func(t *testing.T) {
		v, err := resolveVersion("", "abc123")
		require.NoError(t, err)
		assert.Equal(t, "abc123", v)
	})

	t.Run("no version and no sha errors", func(t *testing.T) {
		_, err := resolveVersion("", "")
		require.Error(t, err)
	})

	// A bundle-authored version is constrained to one path segment, so a
	// hostile manifest can never become a traversal or a dot-entry in a
	// consumer that names something after it.
	rejects := []string{
		"1.0/../../x", // separator
		`a\b`,         // windows separator
		"..",          // traversal
		".tmp",        // dot-prefixed
		".hidden",     // dot-prefixed
		// A version is a single line of a YAML document. A newline makes
		// yaml.Marshal emit a block scalar, so the bundle author gets to write
		// raw lines into the receipt — which a fragment aliasing the receipt
		// then serves as Dockerfile directives.
		"1.0.0\nRUN echo pwned",
		"1.0.0\rRUN echo pwned",
		"1.0.0\x00",
		"1.0.0\x1b[31m", // escape sequence into any surface that prints it
	}
	for _, bad := range rejects {
		t.Run("rejects/"+bad, func(t *testing.T) {
			_, err := resolveVersion(bad, "abc123")
			assert.Error(t, err)
		})
	}
}

// The cache commit replaces an entry that may be SERVING: readers hold no
// lock, and a declared entry that vanishes resolves ErrNotCached and hard-fails
// every build declaring it. So the commit must never destroy the prior entry
// before the replacement is in place — if the swap cannot complete, the old
// content must still be there.
func TestCommitContent_FailedCommitKeepsPriorEntry(t *testing.T) {
	cacheDir := t.TempDir()
	stageBase := filepath.Join(cacheDir, tmpDir)
	require.NoError(t, os.MkdirAll(stageBase, cacheDirPerm))

	entryDir := filepath.Join(cacheDir, "acme", "tools", "key")
	require.NoError(t, os.MkdirAll(entryDir, cacheDirPerm))
	served := filepath.Join(entryDir, "stacks", "node", "stack.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(served), cacheDirPerm))
	require.NoError(t, os.WriteFile(served, []byte("description: serving\n"), 0o600))

	// A staged tree that cannot be renamed into place stands in for any
	// failure of the swap itself (a vanished stage, a full or read-only
	// filesystem): the outcome under test is that the entry keeps serving.
	missingStage := filepath.Join(stageBase, "content-gone")

	err := commitContent(stageBase, missingStage, entryDir)
	require.Error(t, err)

	raw, readErr := os.ReadFile(served)
	require.NoError(t, readErr, "a failed commit must leave the previously serving entry in place")
	assert.Equal(t, "description: serving\n", string(raw))
}

// sanitizeStagedLinks decides safety by RESOLVING each link, and it has two
// drop branches. A dangling link hits the unresolvable branch (that is what the
// receipt-alias install test exercises — the receipt does not exist yet). This
// pins the OTHER one: a link that resolves, through an in-tree directory link,
// to a real file that EXISTS outside the stage. Its own spelling never leaves
// the stage, so only real resolution catches it — and the test asserts the link
// IS resolvable, so a pass here cannot be the unresolvable branch in disguise.
func TestSanitizeStagedLinks_DropsLinkResolvingOutsideStage(t *testing.T) {
	base := t.TempDir()
	stage := filepath.Join(base, "stage")
	require.NoError(t, os.MkdirAll(filepath.Join(stage, "sub"), cacheDirPerm))

	// Real content outside the stage — what a leak would serve.
	secret := filepath.Join(base, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("host content"), 0o600))

	// sub/up -> the stage root: in-tree, legitimate, must SURVIVE.
	require.NoError(t, os.Symlink("..", filepath.Join(stage, "sub", "up")))
	// sub/esc reaches out THROUGH it. Lexically "up/../secret.txt" cleans to
	// <stage>/sub/secret.txt — inside the stage — so no text check objects.
	require.NoError(t, os.Symlink("up/../secret.txt", filepath.Join(stage, "sub", "esc")))

	esc := filepath.Join(stage, "sub", "esc")
	resolved, err := filepath.EvalSymlinks(esc)
	require.NoError(t, err, "the link must RESOLVE, or this test would be pinning the unresolvable branch")
	realSecret, err := filepath.EvalSymlinks(secret)
	require.NoError(t, err)
	require.Equal(t, realSecret, resolved, "the link resolves to the real file outside the stage")

	dropped, err := sanitizeStagedLinks(stage)
	require.NoError(t, err)

	assert.Contains(t, dropped, filepath.Join("sub", "esc"),
		"a link resolving to a real file outside the stage must be dropped")
	assert.NoFileExists(t, esc, "the escaping link must be gone from the staged tree")

	assert.NotContains(t, dropped, filepath.Join("sub", "up"),
		"an in-tree directory link is legitimate content and must survive")
	upInfo, err := os.Lstat(filepath.Join(stage, "sub", "up"))
	require.NoError(t, err)
	assert.NotZero(t, upInfo.Mode()&os.ModeSymlink)

	raw, err := os.ReadFile(secret)
	require.NoError(t, err)
	assert.Equal(t, "host content", string(raw), "sanitize drops the link, never the file it pointed at")
}

// A retired tree stranded by a double fault (restore failed too) may be the
// only copy of a previously-serving entry, and its holding dir is a random
// MkdirTemp name — without a record of WHERE it came from, no later sweep can
// put it back. retireEntry must record the origin entry path beside the tree.
func TestRetireEntry_RecordsOrigin(t *testing.T) {
	cacheDir := t.TempDir()
	stageBase := filepath.Join(cacheDir, tmpDir)
	require.NoError(t, os.MkdirAll(stageBase, cacheDirPerm))
	entryDir := filepath.Join(cacheDir, "acme", "tools", "key")
	require.NoError(t, os.MkdirAll(entryDir, cacheDirPerm))

	retired, err := retireEntry(stageBase, entryDir)
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(retired), retiredOriginFile))
	require.NoError(t, err, "the holding dir must record which entry the retired tree came from")
	assert.Equal(t, entryDir, string(raw))
}

// A failed swap can strand the retired entry when the entry's PARENT vanished
// between retire and rename-in (a sibling-key GC emptied and removed the
// shared identity directory). The restore must recreate the parent and put the
// old entry back — otherwise commit's retry succeeds against an empty slot and
// the previously-serving tree is orphaned in staging forever.
func TestRestoreEntry_RecreatesVanishedParent(t *testing.T) {
	cacheDir := t.TempDir()
	stageBase := filepath.Join(cacheDir, tmpDir)
	require.NoError(t, os.MkdirAll(stageBase, cacheDirPerm))

	entryDir := filepath.Join(cacheDir, "acme", "tools", "key")
	require.NoError(t, os.MkdirAll(entryDir, cacheDirPerm))
	served := filepath.Join(entryDir, "stacks", "node", "stack.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(served), cacheDirPerm))
	require.NoError(t, os.WriteFile(served, []byte("description: serving\n"), 0o600))

	retired, err := retireEntry(stageBase, entryDir)
	require.NoError(t, err)
	require.NotEmpty(t, retired)
	// The sibling-key GC window: identity and namespace dirs are gone.
	require.NoError(t, os.RemoveAll(filepath.Join(cacheDir, "acme")))

	restoreEntry(retired, entryDir)

	raw, err := os.ReadFile(served)
	require.NoError(t, err, "restore must recreate the vanished parent, not strand the retired tree")
	assert.Equal(t, "description: serving\n", string(raw))
	assert.NoDirExists(t, filepath.Dir(retired), "a successful restore discards its holding dir")
}

// The happy path of the same swap: the staged tree lands wholesale and the
// prior entry's content is gone (replaced, not merged).
func TestCommitContent_ReplacesEntry(t *testing.T) {
	cacheDir := t.TempDir()
	stageBase := filepath.Join(cacheDir, tmpDir)
	require.NoError(t, os.MkdirAll(stageBase, cacheDirPerm))

	entryDir := filepath.Join(cacheDir, "acme", "tools", "key")
	require.NoError(t, os.MkdirAll(entryDir, cacheDirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(entryDir, "old.txt"), []byte("old\n"), 0o600))

	// The staged tree must sit on the cache filesystem beside the entry — that
	// is what makes the commit a rename — so it is staged where production
	// stages it, not in an unrelated temp dir.
	stage := filepath.Join(stageBase, contentStagePrefix+"new")
	require.NoError(t, os.MkdirAll(stage, cacheDirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(stage, "new.txt"), []byte("new\n"), 0o600))

	require.NoError(t, commitContent(stageBase, stage, entryDir))
	assert.FileExists(t, filepath.Join(entryDir, "new.txt"))
	assert.NoFileExists(t, filepath.Join(entryDir, "old.txt"), "the entry is replaced, never merged")
	// The retired tree is not left behind as cache litter.
	leftovers, err := os.ReadDir(stageBase)
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func TestSubdirRoot(t *testing.T) {
	clone := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(clone, "pkg", "bundle"), 0o755))

	t.Run("empty subdir is the clone root", func(t *testing.T) {
		root, err := subdirRoot(clone, "")
		require.NoError(t, err)
		assert.Equal(t, clone, root)
	})

	t.Run("nested subdir resolves", func(t *testing.T) {
		root, err := subdirRoot(clone, "pkg/bundle")
		require.NoError(t, err)
		resolved, evalErr := filepath.EvalSymlinks(filepath.Join(clone, "pkg", "bundle"))
		require.NoError(t, evalErr)
		assert.Equal(t, resolved, root)
	})

	t.Run("spelled traversal rejected", func(t *testing.T) {
		_, err := subdirRoot(clone, "../outside")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes")
	})

	// A repo shipping its declared subdir as a symlink pointing out of the
	// clone must be refused: IsLocal only checks the spelled path, so the
	// symlink resolution guard is what closes the escape.
	t.Run("symlink escape rejected", func(t *testing.T) {
		outside := t.TempDir()
		linkClone := t.TempDir()
		require.NoError(t, os.Symlink(outside, filepath.Join(linkClone, "evil")))

		_, err := subdirRoot(linkClone, "evil")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
	})
}
