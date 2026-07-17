package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/config"
)

// src builds a Source with every field explicit — test shorthand.
func src(url, ref, sha, path string) Source {
	return Source{URL: url, Ref: ref, SHA: sha, Path: path}
}

func TestSource_IsLocal(t *testing.T) {
	assert.True(t, src("", "", "", "./vendor/b").IsLocal())
	assert.False(t, src("https://x/y.git", "v1", "", "").IsLocal())
	assert.False(t, src("https://x/y.git", "", "", "sub").IsLocal(),
		"a url with a subdir path is a remote monorepo source, not local")
}

func TestSource_Canonical(t *testing.T) {
	t.Run("sha beats ref", func(t *testing.T) {
		withRef := src("https://x/y.git", "main", "", "")
		withSHA := src("https://x/y.git", "main", "abc", "")
		assert.NotEqual(t, withRef.Canonical(), withSHA.Canonical())
		assert.Contains(t, withSHA.Canonical(), "sha:abc")
		assert.NotContains(t, withSHA.Canonical(), "ref:main",
			"a sha pin supersedes ref in the identity key")
	})

	t.Run("subdir distinguishes monorepo siblings", func(t *testing.T) {
		a := src("https://x/mono.git", "v1", "", "bundles/a")
		b := src("https://x/mono.git", "v1", "", "bundles/b")
		assert.NotEqual(t, a.Canonical(), b.Canonical())
	})

	t.Run("local path display form is cleaned", func(t *testing.T) {
		// The local branch is a display form only — resolver claims key by the
		// resolved absolute dir — but it still normalizes cosmetic spellings.
		assert.Equal(t, "path:dir", src("", "", "", "./dir").Canonical())
		assert.Equal(t, "path:dir", src("", "", "", "dir/").Canonical())
	})

	t.Run("identical remotes match", func(t *testing.T) {
		a := src("https://x/y.git", "v1", "", "")
		b := src("https://x/y.git", "v1", "", "")
		assert.Equal(t, a.Canonical(), b.Canonical())
	})
}

func TestSourceFromConfig(t *testing.T) {
	got := SourceFromConfig(config.BundleSource{
		URL: "https://x/y.git", Ref: "v1", SHA: "deadbeef", Path: "sub", AutoUpdate: false,
	})
	assert.Equal(t, src("https://x/y.git", "v1", "deadbeef", "sub"), got)
}
