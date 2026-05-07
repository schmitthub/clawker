#!/usr/bin/env bash
# Run govulncheck against the Go toolchain pinned in go.mod, not whatever
# happens to be on PATH.
#
# Why: govulncheck reports stdlib vulnerabilities for the version of Go
# it analyzes. Locally, that defaults to whatever is on PATH (often a
# newer toolchain). CI uses actions/setup-go with go-version-file: go.mod,
# so it analyzes exactly the version in go.mod. Without this pin, the
# pre-commit hook can pass on a developer's newer-than-go.mod toolchain
# while CI fails on the version go.mod requests — exactly the drift this
# script exists to prevent.
#
# Setting GOTOOLCHAIN=goX.Y.Z forces Go to use that exact toolchain
# (downloading it if needed) regardless of what's installed locally.

set -euo pipefail

GO_DIRECTIVE=$(awk '/^go /{print $2; exit}' go.mod)
if [[ -z "${GO_DIRECTIVE}" ]]; then
    echo "govulncheck.sh: could not parse 'go' directive from go.mod" >&2
    exit 1
fi

exec env GOTOOLCHAIN="go${GO_DIRECTIVE}" govulncheck ./...
