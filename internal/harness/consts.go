package harness

import (
	"io/fs"
	"path/filepath"
	"slices"
)

// ManifestFile is the manifest filename inside a harness bundle directory.
const ManifestFile = "harness.yaml"

// TemplateFile is the Dockerfile fragment filename inside a harness bundle
// directory. Its {{define}} bodies override the master template's block
// slots.
const TemplateFile = "Dockerfile.harness.tmpl"

// AssetsDir is the bundle subdirectory holding every file the bundle
// contributes to the docker build context. The whole tree is staged
// verbatim under the same assets/ prefix; the template's COPY instructions
// and seeds[].file entries reference assets/-relative paths.
const AssetsDir = "assets"

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

// File modes for staged build-context files.
const (
	plainFileMode  = fs.FileMode(0o644)
	scriptFileMode = fs.FileMode(0o755)
)

// FileMode returns the on-disk mode for a bundle file written outside the
// bundle (build-context staging dirs): scripts stay executable.
func FileMode(name string) fs.FileMode {
	if filepath.Ext(name) == ".sh" {
		return scriptFileMode
	}
	return plainFileMode
}

// DeclaredBlocks returns the slot names the master Dockerfile template
// declares. A harness template may define any subset; defining any other
// template name is a validation error. Names are positional opportunities
// in the master's instruction ordering, never content-prescriptive.
//
// NOTE: placeholder generic names — final event-centric names TBD.
func DeclaredBlocks() []string {
	return []string{
		"block_1", // root scope, after system packages + Docker CLI, before user context — heavy stack installs
		"block_2", // root scope, after base tooling, before USER ${USERNAME}
		"block_3", // user scope, after the master's static-env section
		"block_4", // user scope, after user_run renders — version-ARG cache zone
		"block_5", // root scope, after trailing USER root, before clawker assets
		"block_6", // final instruction — CMD position
	}
}

// isReservedDefine reports whether name is a template name a harness may
// never define: the master template's own name plus the project-config
// inject-point keys, which must stay disjoint from block names forever.
func isReservedDefine(name string) bool {
	switch name {
	case "Dockerfile",
		"after_from",
		"after_packages",
		"after_user_setup",
		"after_user_switch",
		"after_claude_install",
		"after_harness_install",
		"before_entrypoint":
		return true
	}
	return false
}

func isDeclaredBlock(name string) bool {
	return slices.Contains(DeclaredBlocks(), name)
}
