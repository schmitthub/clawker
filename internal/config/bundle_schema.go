package config

// This file owns the bundle-distribution schema: the project-config
// `bundles:` source-entry shape (BundleSource) and the distributed bundle
// manifest shape (BundleManifest, `.clawker-bundle/bundle.yaml`). config owns
// only these persisted shapes — the fetch/cache/resolution engine that reads a
// declared source, clones it, validates the manifest, and resolves components
// lives in internal/bundle. The manifest is kept here alongside the harness,
// stack, and monitoring component manifests so every bundle-model file shape is
// declared in one package.

// BundleSource is one entry of the project-config `bundles:` list — a
// git-generic source spec for a distributed bundle. A source is one of two
// shapes: a remote clone spec (url set, with a subdir path optional) or a
// local in-place spec (path alone, no url — loaded from disk with no cache
// copy, which is the dev loop). A remote source may pin a ref or a sha (sha
// takes precedence when both are given and pins a reproducible fetch); with
// neither, the source is unpinned and tracks the repository's default branch.
// Nothing here is identity-bearing — a bundle's identity comes only from its
// manifest (namespace, name).
type BundleSource struct {
	URL        string `yaml:"url,omitempty"         label:"URL"         desc:"Git clone URL (https or ssh) of the bundle's source repository; set for a remote bundle. Mutually exclusive with a path-only local source."`
	Ref        string `yaml:"ref,omitempty"         label:"Ref"         desc:"Branch or tag to fetch from a remote url; sha takes precedence when both are set. Omitting both ref and sha tracks the repository's default branch. Ignored for a local path source."`
	SHA        string `yaml:"sha,omitempty"         label:"SHA"         desc:"Full 40-character commit SHA pinning a remote url for a reproducible fetch; takes precedence over ref when both are set. Ignored for a local path source."`
	Path       string `yaml:"path,omitempty"        label:"Path"        desc:"With url: a subdirectory of the repository to load the bundle from (monorepo case). Without url: a local directory loaded in place with no cache copy (the dev loop); a relative path resolves against the directory of the file declaring it."`
	AutoUpdate bool   `yaml:"auto_update,omitempty" label:"Auto Update" desc:"Refetch this remote bundle when its source version changes, checked at the start of bundle-consuming commands (build, run, monitor up); off by default. No effect on sha-pinned or local sources."`
}

// BundleManifest is the parsed .clawker-bundle/bundle.yaml — a distributed
// bundle's pure metadata. It carries identity (namespace, name) plus optional
// descriptive fields only; components (harnesses, stacks, monitoring
// extensions) are discovered by convention directory, never declared here. The
// loader that reads and validates a fetched bundle lives in internal/bundle;
// config owns only the persisted shape.
//
// BundleManifest is a file shape, not a storage.Schema — it is never stored in
// a Store, only parsed from a bundle repository. It exists here so gen-docs can
// emit its JSON Schema from the same struct tags as every other config type.
type BundleManifest struct {
	Namespace   string `yaml:"namespace"             label:"Namespace"   desc:"Maintainer branding for the bundle (org, handle, or umbrella); lowercase letters, digits, and internal hyphens. Combines with name to form the bundle's identity. Reserved namespaces (clawker and impersonation forms) are rejected." required:"true"`
	Name        string `yaml:"name"                  label:"Name"        desc:"Bundle name; lowercase letters, digits, and internal hyphens. Combines with namespace to form the bundle's identity — never derived from the source URL."                                                                              required:"true"`
	Version     string `yaml:"version,omitempty"     label:"Version"     desc:"Bundle version used only for update change-detection (no compatibility semantics); when absent, the resolved source commit SHA is the version."`
	Description string `yaml:"description,omitempty" label:"Description" desc:"Human-readable summary of what the bundle ships."`
	Author      string `yaml:"author,omitempty"      label:"Author"      desc:"Bundle author or maintainer."`
	Repository  string `yaml:"repository,omitempty"  label:"Repository"  desc:"URL of the bundle's source repository (informational)."`
	License     string `yaml:"license,omitempty"     label:"License"     desc:"License identifier for the bundle's contents."`
}

// BundleDeclaration pairs one declared bundle source with the config file
// that declared it. The union-merged Project.Bundles list collapses layers and
// loses per-entry provenance; the resolver needs the declaring file so an
// identity-collision error (two sources resolving to the same namespace.name)
// can name both offending entries and the exact file each lives in.
type BundleDeclaration struct {
	// Source is the declared bundle source, decoded through the schema.
	Source BundleSource
	// File is the resolved absolute path of the clawker.yaml layer that
	// declared this source, or "" for the virtual defaults/seed layer (which
	// declares no bundles in practice).
	File string
}
