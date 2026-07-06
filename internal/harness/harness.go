// Package harness defines the file-backed harness bundle format that makes
// clawker agent-harness agnostic. A harness bundle is a directory of:
//
//   - harness.yaml — slim manifest holding data the Go engine consumes
//     outside template rendering (version resolver, seed and staging
//     manifests, egress floor).
//   - Dockerfile.harness.tmpl — all harness build surface, expressed as
//     {{define}} overrides for the block slots declared by the master
//     Dockerfile template. The master owns instruction ordering and cache
//     architecture; a harness can only fill the declared slots.
//   - assets/ — every file the bundle contributes to the docker build
//     context (config seeds, scripts, instruction files). The whole tree is
//     staged verbatim; the template's COPY instructions (and seed `file:`
//     entries) reference assets/-prefixed paths and alone decide what lands
//     in the image and where.
//
// Shipped bundles are embedded in internal/bundler assets and materialized
// into the user config directory, where they are user-owned and editable.
// Custom harnesses are the same shape: a bundle directory plus a registry
// entry in settings.
package harness

// Manifest is the parsed harness.yaml.
type Manifest struct {
	Version VersionSpec  `yaml:"version"`
	Volumes []VolumeSpec `yaml:"volumes,omitempty"`
	Seeds   []Seed       `yaml:"seeds,omitempty"`
	Staging Staging      `yaml:"staging,omitempty"`
	Egress  []EgressRule `yaml:"egress,omitempty"`
}

// VolumeSpec declares one persisted directory: a named volume
// (clawker.<project>.<agent>-<name>) mounted at Path under the container
// home. Every persisted dir is an explicit declaration — clawker assumes
// nothing about where a harness keeps state. Name is the volume-name
// suffix; Path is container-home-relative.
type VolumeSpec struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// VersionSpec declares how the harness version rendered into the build
// template is resolved.
type VersionSpec struct {
	// Resolver is one of "npm", "github-release", or "none".
	Resolver string `yaml:"resolver"`
	// Package is the npm package name or GitHub owner/repo, per resolver.
	Package string `yaml:"package,omitempty"`
}

// Seed is one entry of the runtime volume seed manifest, applied by the
// generic CP init script on first boot. File is the bundle-relative source
// path and must live under the assets/ tree; Dest is the
// container-home-relative target path and must fall under a declared
// volume (seeding a non-persisted path is a config error).
type Seed struct {
	File  string `yaml:"file"`
	Dest  string `yaml:"dest"`
	Apply string `yaml:"apply"` // copy-if-missing | copy-if-missing-or-empty | json-merge
}

// Staging describes the create-time host→container copy job for harness
// state that lives OUTSIDE the workspace — the workspace itself arrives via
// bind mount or snapshot and is never staged. Every entry is an explicit,
// deliberate src→dest directive; nothing is copied by naming convention.
type Staging struct {
	Copy   []CopySpec  `yaml:"copy,omitempty"`
	Mounts []MountSpec `yaml:"mounts,omitempty"`
}

// CopySpec is one explicit host→container copy directive. Src is a host
// path or doublestar glob; `~`, `$VAR`/`${VAR}`, and shell-style
// `${VAR:-fallback}` defaults expand before matching. Dest is
// container-home-relative and must fall under a declared volume — copies
// land in the volume at create time, so a non-persisted dest is a config
// error, caught at load. A src resolving to a directory copies
// recursively; a glob matching multiple entries lands each under dest as a
// directory. Missing sources skip. Sources inside the project workspace are
// rejected at stage time — the workspace is mounted, never staged.
type CopySpec struct {
	Src          string        `yaml:"src"`
	Dest         string        `yaml:"dest"`
	JSONKeys     []string      `yaml:"json_keys,omitempty"`
	Skip         []string      `yaml:"skip,omitempty"`
	JSONRewrites []JSONRewrite `yaml:"json_rewrites,omitempty"`
}

// JSONRewrite rewrites one JSON key's path value during tree staging.
type JSONRewrite struct {
	File    string `yaml:"file"`
	Key     string `yaml:"key"`
	Rewrite string `yaml:"rewrite"` // prefix-swap | replace-with-workdir
}

// MountSpec live-binds a host directory into the container instead of
// copying it. Src is host-side and expands like CopySpec.Src; Dest is
// container-home-relative. Src must be a literal path (no globs).
type MountSpec struct {
	Src  string `yaml:"src"`
	Dest string `yaml:"dest"`
}

// EgressRule is the harness's required firewall floor. Shape mirrors the
// project-config egress rule; composed with project rules at create time.
type EgressRule struct {
	Dst         string     `yaml:"dst"`
	Proto       string     `yaml:"proto,omitempty"`
	Port        string     `yaml:"port,omitempty"`
	Action      string     `yaml:"action,omitempty"`
	PathRules   []PathRule `yaml:"path_rules,omitempty"`
	PathDefault string     `yaml:"path_default,omitempty"`
}

// PathRule is a path-scoped verdict on an egress rule.
type PathRule struct {
	Path    string   `yaml:"path"`
	Action  string   `yaml:"action"`
	Methods []string `yaml:"methods,omitempty"`
}
