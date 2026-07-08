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
// Shipped bundles are embedded in internal/bundler assets and load straight
// from the embedded FS. Custom harnesses are the same shape: a bundle
// directory registered via a harnesses.<name>.path entry in the project's
// clawker.yaml.
//
// The manifest schema types ([config.Manifest] and its members) live in
// internal/config, the schema-contract owner; this package owns the loader,
// validators, and template composition that turn a bundle directory into a
// loaded [Bundle].
package harness
