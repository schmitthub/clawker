package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
)

// srcOpts builds a fully-populated InstallOptions for classifySource tests
// (only the source-shaping fields vary).
func srcOpts(source, ref string) *InstallOptions {
	return &InstallOptions{
		IOStreams:     nil,
		Config:        nil,
		BundleManager: nil,
		Source:        source,
		Ref:           ref,
		SHA:           "",
		Subdir:        "",
		AutoUpdate:    false,
		User:          false,
		Project:       false,
		Local:         false,
	}
}

func TestClassifySource_GitURL(t *testing.T) {
	got, err := classifySource(srcOpts("https://example.com/x.git", "v1.0.0"))
	require.NoError(t, err)
	assert.Equal(t, config.BundleSource{
		URL: "https://example.com/x.git", Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false,
	}, got)
}

func TestClassifySource_SSHURL(t *testing.T) {
	got, err := classifySource(srcOpts("git@github.com:acme/x.git", "main"))
	require.NoError(t, err)
	assert.Equal(t, config.BundleSource{
		URL: "git@github.com:acme/x.git", Ref: "main", SHA: "", Path: "", AutoUpdate: false,
	}, got)
}

func TestClassifySource_OwnerRepoExpands(t *testing.T) {
	// The repo segment is taken verbatim — no suffix normalization — so a repo
	// literally named "tools.git" stays addressable.
	for arg, wantURL := range map[string]string{
		"acme/tools":     "https://github.com/acme/tools.git",
		"acme/my.tools":  "https://github.com/acme/my.tools.git",
		"acme/tools.git": "https://github.com/acme/tools.git.git",
	} {
		got, err := classifySource(srcOpts(arg, "v1.0.0"))
		require.NoError(t, err)
		assert.Equal(t, config.BundleSource{
			URL: wantURL, Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false,
		}, got, "arg %q", arg)
	}
}

func TestClassifySource_LocalPath(t *testing.T) {
	got, err := classifySource(srcOpts("./vendor/x", ""))
	require.NoError(t, err)
	assert.Equal(t, config.BundleSource{
		URL: "", Ref: "", SHA: "", Path: "./vendor/x", AutoUpdate: false,
	}, got)
}

func TestClassifySource_Errors(t *testing.T) {
	bad := []*InstallOptions{
		srcOpts("./vendor/x", "v1"), // local path with ref
		srcOpts("node", ""),         // bare word, not a source
		srcOpts("a/b/c", ""),        // three segments, not owner/repo
	}
	for _, o := range bad {
		_, err := classifySource(o)
		assert.Error(t, err, "source %q should be rejected", o.Source)
	}
}

func TestUnderRoot(t *testing.T) {
	assert.True(t, underRoot("/root/sub/x", "/root"))
	assert.True(t, underRoot("/root", "/root"))
	assert.False(t, underRoot("/other/x", "/root"))
}

// rewriteLocalPath re-anchors the cwd-relative CLI argument to the directory of
// the yaml file it lands in, since a stored relative path resolves against its
// declaring file. Committed layers keep a portable relative spelling; an
// absolute argument round-trips through Rel and back to the same directory.
func TestRewriteLocalPath(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "proj", "clawker.yaml")
	require.NoError(t, os.MkdirAll(filepath.Join(base, "proj", "sub"), 0o755))

	t.Run("cwd inside the target's directory", func(t *testing.T) {
		t.Chdir(filepath.Join(base, "proj", "sub"))
		got, err := rewriteLocalPath("./vendor/x", target)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("sub", "vendor", "x"), got)
	})

	t.Run("cwd outside the target's directory climbs out", func(t *testing.T) {
		t.Chdir(base)
		got, err := rewriteLocalPath("./elsewhere/x", target)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("..", "elsewhere", "x"), got)
	})

	t.Run("absolute argument becomes relative to the target file", func(t *testing.T) {
		got, err := rewriteLocalPath(filepath.Join(base, "proj", "vendor", "x"), target)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("vendor", "x"), got)
	})
}
