package config

// The required egress floor is no longer defined here: each harness bundle
// declares its own floor in harness.yaml (egress:), composed with the
// project's security.firewall rules by bundler.EgressRules. Security
// knowledge like the .claude.ai UGC path denies travels WITH the harness.

// Programmatic base-layer defaults for project and settings configuration are
// generated from `default` struct tags on schema types via
// storage.GenerateDefaultsYAML[T](). See schema.go for the tag definitions.
// Consumers use storage.WithDefaultsFromStruct[T]() to inject defaults into
// a Store[T] as a merge layer.

// DefaultIgnoreFile is the default .clawkerignore content.
// All entries are commented out — users opt in to what they need.
const DefaultIgnoreFile = `# Clawker Ignore File
#
# In bind mode, listed directories are masked with empty tmpfs overlays
# so the host's platform-specific binaries (e.g. macOS Darwin node_modules/.bin)
# don't bleed into the Linux container. The container installs its own
# dependencies into the tmpfs, which is ephemeral.
#
# In snapshot mode, listed directories are simply excluded from the copy —
# they don't exist in the container at all, allowing it to create its own.
#
# Syntax follows .gitignore:
#   - A pattern without a leading "/" matches at any depth: "build/" masks
#     ./build AND e.g. ./internal/build. Prefix with "/" to anchor it to the
#     workspace root: "/build/" masks only ./build.
#   - Negation ("!pattern") re-includes an earlier match, but a path under an
#     ignored directory cannot be re-included.
# File-level patterns (*.env, *.pem) cannot be enforced in bind mode —
# only directory-level masking works.
#
# Uncomment the lines relevant to your stack:

# ── JavaScript / TypeScript ──
# node_modules/
# .next/
# .nuxt/

# ── Python ──
# .venv/
# __pycache__/
# .mypy_cache/

# ── Go ──
# vendor/

# ── Ruby ──
# vendor/bundle/

# ── Rust ──
# /target/

# ── Java / Kotlin ──
# .gradle/
# build/

# ── .NET ──
# bin/
# obj/

# ── PHP ──
# vendor/

# ── Build outputs (anchored — unanchored "build/" would also mask source
# directories like internal/build) ──
# /dist/
# /build/
# /out/
`
