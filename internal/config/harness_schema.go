package config

// This file owns the harness bundle manifest schema (harness.yaml) — the
// data a harness bundle declares for the Go engine to consume outside
// template rendering (version resolver, volumes, seeds, staging, egress
// floor, stack declarations). The loader that reads and validates a bundle
// lives in internal/bundler; config owns only the persisted shape, keeping
// the manifest vocabulary in the same package as the project-config egress
// and path-semantics types it composes with.

// Manifest is the parsed harness.yaml.
type Manifest struct {
	Version VersionSpec  `yaml:"version"`
	Volumes []VolumeSpec `yaml:"volumes,omitempty"`
	Seeds   []Seed       `yaml:"seeds,omitempty"`
	Staging Staging      `yaml:"staging,omitempty"`
	Egress  []EgressRule `yaml:"egress,omitempty"`

	// Stacks declares the stack definitions this harness's blocks
	// require, by name. Names resolve per lineage at generation time —
	// project stacks: registry > this bundle's stacks/ subdir > shipped —
	// and the resolved fragments always render in the harness image, even
	// when the project also declares the same name in the shared base
	// (fragment self-guards own any interaction).
	Stacks []string `yaml:"stacks,omitempty"`
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
	// Resolver is one of ResolverNPM, ResolverGitHubRelease, or ResolverNone.
	Resolver string `yaml:"resolver"`
	// Package is the npm package name or GitHub owner/repo, per resolver.
	Package string `yaml:"package,omitempty"`
	// TagPrefix (github-release only) is the release-tag prefix stripped
	// to obtain the bare version (e.g. "rust-v" turns tag rust-v0.50.0
	// into 0.50.0). A latest tag missing the prefix fails resolution.
	TagPrefix string `yaml:"tag_prefix,omitempty"`
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

// Version resolvers — the closed vocabulary a manifest's version.resolver
// accepts. npm resolves the package's latest dist-tag from the npm
// registry; github-release resolves the repo's latest release tag via the
// GitHub API (tag_prefix stripped); none renders the floating default.
const (
	ResolverNPM           = "npm"
	ResolverGitHubRelease = "github-release"
	ResolverNone          = "none"
)

// Seed apply strategies — the closed vocabulary a manifest's seeds[].apply
// accepts. The master Dockerfile template writes these tokens into the
// image's seed manifest and CP's generic seed-apply script switches on
// them at first boot.
const (
	SeedApplyCopyIfMissing        = "copy-if-missing"
	SeedApplyCopyIfMissingOrEmpty = "copy-if-missing-or-empty"
	SeedApplyJSONMerge            = "json-merge"
)

// JSON rewrite kinds — the closed vocabulary a manifest's
// staging.copy[].json_rewrites[].rewrite accepts. prefix-swap maps the
// host tree prefix onto the in-container config-dir tree prefix;
// replace-with-workdir substitutes the whole value with the container
// workspace path.
const (
	RewritePrefixSwap         = "prefix-swap"
	RewriteReplaceWithWorkdir = "replace-with-workdir"
)
