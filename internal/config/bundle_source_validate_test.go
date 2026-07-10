package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
)

const validSHA = "0123456789abcdef0123456789abcdef01234567"

func TestValidateBundleSource_Valid(t *testing.T) {
	valid := []config.BundleSource{
		{URL: "https://example.com/x.git", Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false},
		{URL: "https://example.com/x.git", Ref: "", SHA: validSHA, Path: "", AutoUpdate: false},
		{URL: "https://example.com/x.git", Ref: "main", SHA: "", Path: "sub/dir", AutoUpdate: true},
		{URL: "", Ref: "", SHA: "", Path: "./vendor/x", AutoUpdate: false}, // project-layer relative
	}
	for _, src := range valid {
		require.NoError(t, config.ValidateBundleSource(src, false), "src=%+v", src)
	}
}

func TestValidateBundleSource_Invalid(t *testing.T) {
	invalid := []config.BundleSource{
		// remote missing ref/sha
		{URL: "https://example.com/x.git", Ref: "", SHA: "", Path: "", AutoUpdate: false},
		// remote with an abbreviated (non-40-hex) sha
		{URL: "https://example.com/x.git", Ref: "", SHA: "0123abc", Path: "", AutoUpdate: false},
		// ref without a url
		{URL: "", Ref: "v1", SHA: "", Path: "./x", AutoUpdate: false},
		// neither url nor path
		{URL: "", Ref: "", SHA: "", Path: "", AutoUpdate: false},
	}
	for _, src := range invalid {
		assert.Error(t, config.ValidateBundleSource(src, false), "src=%+v", src)
	}
}

func TestValidateBundleSource_ConfigDirLayerRequiresAbsolutePath(t *testing.T) {
	relative := config.BundleSource{URL: "", Ref: "", SHA: "", Path: "./vendor/x", AutoUpdate: false}
	require.Error(t, config.ValidateBundleSource(relative, true))

	absolute := config.BundleSource{URL: "", Ref: "", SHA: "", Path: "/opt/bundles/x", AutoUpdate: false}
	require.NoError(t, config.ValidateBundleSource(absolute, true))
}
