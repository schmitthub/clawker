package bundler_test

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
)

func mapFile(data string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(data), Mode: 0, ModTime: time.Time{}, Sys: nil}
}

func bundleFS(tmpl string) fstest.MapFS {
	return fstest.MapFS{
		bundler.HarnessManifestFile: mapFile(`
version: { resolver: none }
volumes: [{ name: config, path: .test }]
`),
		bundler.HarnessTemplateFile: mapFile(tmpl),
		"assets/seed.sh":            mapFile("#!/bin/sh\n"),
	}
}

func manifestFS(manifest string) fstest.MapFS {
	return fstest.MapFS{
		bundler.HarnessManifestFile: mapFile(manifest),
		bundler.HarnessTemplateFile: mapFile(`{{define "block_6"}}CMD ["x"]{{end}}`),
	}
}

// Conformance: E7 — a floor rule requesting TLS-verification-skip is rejected at bundle load.
// A harness bundle is third-party content; the insecure_skip_tls_verify knob is
// reserved for the machine owner's own project security.firewall.rules, so a floor
// rule that sets it is a hard load error — the same manifest loads cleanly with the
// field absent.
func TestLoadBundle_EgressFloorRejectsTLSSkip(t *testing.T) {
	_, err := bundler.LoadBundle("codex", manifestFS(`
version: { resolver: none }
egress:
  - dst: api.example.com
    insecure_skip_tls_verify: true
`))
	require.ErrorContains(t, err, "codex")
	require.ErrorContains(t, err, "api.example.com")
	require.ErrorContains(t, err, "insecure_skip_tls_verify")

	b, err := bundler.LoadBundle("codex", manifestFS(`
version: { resolver: none }
egress:
  - dst: api.example.com
`))
	require.NoError(t, err)
	require.Len(t, b.Manifest.Egress, 1)
	assert.False(t, b.Manifest.Egress[0].InsecureSkipTLSVerify)
}

func TestLoadBundle_StackDeclarations(t *testing.T) {
	b, err := bundler.LoadBundle("test", manifestFS(`
version: { resolver: none }
stacks: [node, nvm]
`))
	require.NoError(t, err)
	assert.Equal(t, []string{"node", "nvm"}, b.Manifest.Stacks)
}

// Conformance: E18 — intra-manifest duplicate stack declarations are rejected at bundle load.
func TestLoadBundle_StackDeclarations_DuplicateRejected(t *testing.T) {
	_, err := bundler.LoadBundle("test", manifestFS(`
version: { resolver: none }
stacks: [node, node]
`))
	require.ErrorContains(t, err, `duplicate stack declaration "node"`)
}

func TestLoadBundle_StackDeclarations_InvalidNameRejected(t *testing.T) {
	_, err := bundler.LoadBundle("test", manifestFS(`
version: { resolver: none }
stacks: ["bad/name"]
`))
	require.ErrorContains(t, err, "bad/name")
}

// A qualified stack dependency (a bundled harness referencing its shipped
// sibling stack by its self-address) is a valid stacks: entry at load; whether
// it resolves is a generation-time concern for the resolver.
func TestLoadBundle_StackDeclarations_QualifiedAccepted(t *testing.T) {
	b, err := bundler.LoadBundle("test", manifestFS(`
version: { resolver: none }
stacks: [node, acme.tools.node]
`))
	require.NoError(t, err)
	assert.Equal(t, []string{"node", "acme.tools.node"}, b.Manifest.Stacks)
}

// Conformance: E14 — block slots are stable reserved surfaces. E20 — a bundle fragment fills declared slots without disturbing master ordering.
func TestCompose_OverridesDeclaredBlock(t *testing.T) {
	b, err := bundler.LoadBundle("test", bundleFS(`{{define "block_6" -}}
CMD ["testtool"]
{{- end}}`))
	require.NoError(t, err)

	tmpl, err := bundler.Compose("FROM scratch\n{{block \"block_6\" .}}{{end}}\n", b)
	require.NoError(t, err)

	var out strings.Builder
	require.NoError(t, tmpl.Execute(&out, nil))
	assert.Equal(t, "FROM scratch\nCMD [\"testtool\"]\n", out.String())
}

// Conformance: E14 — defining a master/inject-point name is a hard error. E20 — the master owns ordering; a fragment may only fill declared slots.
func TestCompose_RejectsUnknownAndReservedDefines(t *testing.T) {
	unknown, err := bundler.LoadBundle("test", bundleFS(`{{define "not_a_block"}}RUN true{{end}}`))
	require.NoError(t, err)
	_, err = bundler.Compose("FROM scratch\n", unknown)
	require.ErrorContains(t, err, `unknown block "not_a_block"`)

	reserved, err := bundler.LoadBundle("test", bundleFS(`{{define "after_packages"}}RUN true{{end}}`))
	require.NoError(t, err)
	_, err = bundler.Compose("FROM scratch\n", reserved)
	require.ErrorContains(t, err, `reserved name "after_packages"`)
}

// seedBundleFS builds a loadable bundle whose manifest is supplied verbatim.
func seedBundleFS(manifest string) fstest.MapFS {
	return fstest.MapFS{
		bundler.HarnessManifestFile: mapFile(manifest),
		bundler.HarnessTemplateFile: mapFile(`{{define "block_6"}}CMD ["x"]{{end}}`),
		"assets/statusline.sh":      mapFile("#!/bin/sh\n"),
		"assets/cfg.json":           mapFile("{}\n"),
		"assets/sub/nested.txt":     mapFile("n\n"),
	}
}

// TestLoadBundle_SeedsReferenceAssets: seed sources must be declared as explicit
// assets/-relative paths — the assets/ tree is what rides the build context.
func TestLoadBundle_SeedsReferenceAssets(t *testing.T) {
	b, err := bundler.LoadBundle("t", seedBundleFS(`
version: { resolver: none }
volumes: [{ name: config, path: .test }]
seeds:
  - { file: assets/statusline.sh, dest: .test/statusline.sh, apply: copy-if-missing }
  - { file: assets/cfg.json, dest: .test/sub/.config.json, apply: copy-if-missing-or-empty }
`))
	require.NoError(t, err)
	assert.Equal(t, "assets/statusline.sh", b.Manifest.Seeds[0].File)
}

// TestWalkAssets pins the build-context staging contract: every file under
// assets/ is visited with its assets/-prefixed path, and a bundle without
// an assets/ dir is a valid no-op.
func TestWalkAssets(t *testing.T) {
	b, err := bundler.LoadBundle("t", seedBundleFS("version: { resolver: none }\n"))
	require.NoError(t, err)

	var got []string
	require.NoError(t, b.WalkAssets(func(relPath string, content []byte) error {
		require.NotEmpty(t, content)
		got = append(got, relPath)
		return nil
	}))
	assert.ElementsMatch(t, []string{"assets/statusline.sh", "assets/cfg.json", "assets/sub/nested.txt"}, got)

	noAssets, err := bundler.LoadBundle("t", fstest.MapFS{
		bundler.HarnessManifestFile: mapFile("version: { resolver: none }\n"),
		bundler.HarnessTemplateFile: mapFile(`{{define "block_6"}}CMD ["x"]{{end}}`),
	})
	require.NoError(t, err)
	require.NoError(t, noAssets.WalkAssets(func(string, []byte) error {
		t.Fatal("must not be called for a bundle without assets/")
		return nil
	}))
}

func TestLoadBundle_SeedValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name: "unknown apply strategy",
			manifest: `
seeds:
  - { file: assets/cfg.json, dest: .test/cfg.json, apply: overwrite }
`,
			wantErr: "unknown apply strategy",
		},
		{
			name: "seed file outside assets tree",
			manifest: `
seeds:
  - { file: cfg.json, dest: .test/cfg.json, apply: copy-if-missing }
`,
			wantErr: "under assets/",
		},
		{
			name: "seed file escaping bundle root",
			manifest: `
seeds:
  - { file: ../cfg.json, dest: .test/cfg.json, apply: copy-if-missing }
`,
			wantErr: "under assets/",
		},
		{
			name: "seed dest outside every declared volume",
			manifest: `
seeds:
  - { file: assets/cfg.json, dest: .elsewhere/cfg.json, apply: copy-if-missing }
`,
			wantErr: "not under any declared volume",
		},
		{
			name: "seed file missing from bundle",
			manifest: `
seeds:
  - { file: assets/nope.json, dest: .test/cfg.json, apply: copy-if-missing }
`,
			wantErr: "nope.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := bundler.LoadBundle("t", seedBundleFS("volumes: [{ name: config, path: .test }]\n"+tt.manifest))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestLoadBundle_StagingValidationErrors pins the load front door: volumes are
// explicit and well-formed, every directive names src and dest
// deliberately, dests fall under a declared volume, and filter verbs match
// their shapes.
func TestLoadBundle_StagingValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name: "copy missing dest",
			manifest: `
volumes: [{ name: config, path: .test }]
staging:
  copy:
    - src: ~/.test/a.json
`,
			wantErr: "require explicit src and dest",
		},
		{
			name: "copy dest outside every volume",
			manifest: `
volumes: [{ name: config, path: .test }]
staging:
  copy:
    - { src: ~/.test/a.json, dest: .other/a.json }
`,
			wantErr: "not under any declared volume",
		},
		{
			name: "copy dest with no volumes declared",
			manifest: `
staging:
  copy:
    - { src: ~/.test/a.json, dest: .test/a.json }
`,
			wantErr: "not under any declared volume",
		},
		{
			name: "json_keys on a glob src",
			manifest: `
volumes: [{ name: config, path: .test }]
staging:
  copy:
    - { src: "~/.test/*.json", dest: .test/, json_keys: [k] }
`,
			wantErr: "json_keys requires a single-file src",
		},
		{
			name: "glob mount src",
			manifest: `
volumes: [{ name: config, path: .test }]
staging:
  mounts:
    - { src: "~/.test/proj*", dest: .test/projects }
`,
			wantErr: "must be a literal path, not a glob",
		},
		{
			name: "volume name reserved for infrastructure",
			manifest: `
volumes: [{ name: history, path: .test }]
`,
			wantErr: "reserved for clawker infrastructure",
		},
		{
			name: "volume name reserved for clawker lifecycle volume",
			manifest: `
volumes: [{ name: clawker, path: .test }]
`,
			wantErr: "reserved for clawker infrastructure",
		},
		{
			name: "volume name invalid for docker",
			manifest: `
volumes: [{ name: "bad name", path: .test }]
`,
			wantErr: "must match",
		},
		{
			name: "duplicate volume name",
			manifest: `
volumes:
  - { name: config, path: .a }
  - { name: config, path: .b }
`,
			wantErr: "duplicate volume name",
		},
		{
			name: "duplicate volume path",
			manifest: `
volumes:
  - { name: a, path: .test }
  - { name: b, path: .test }
`,
			wantErr: "duplicate volume path",
		},
		{
			name: "volume path escaping home",
			manifest: `
volumes: [{ name: config, path: ../up }]
`,
			wantErr: "container-home-relative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := bundler.LoadBundle("t", seedBundleFS("version: { resolver: none }\n"+tt.manifest))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
