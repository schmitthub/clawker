#!/usr/bin/env bash
#
# Generate third-party license information for embedding in the binary.
#
# Usage:
#   scripts/licenses.sh <GOOS> <GOARCH>   Generate licenses for one platform
#   scripts/licenses.sh --check           Verify generation for all release platforms
#
# goreleaser pre-build hooks call the single-platform mode; output goes to
# internal/cmd/licenses/embed/<goos>-<goarch> and is embedded via go:embed.
# CI runs --check to catch generation failures before a release.
#
# go-licenses loads every package, so the go:embed binaries and bpf2go
# wrappers must exist first (the Makefile licenses-check target builds them).
set -euo pipefail

cd "$(dirname "$0")/.."

# Build the tool for the host, even when the caller sets a target GOOS/GOARCH.
env -u GOOS -u GOARCH GOBIN="$PWD/bin" go install github.com/google/go-licenses/v2@v2.0.1
GO_LICENSES="$PWD/bin/go-licenses"

# Release platforms. Keep in sync with .goreleaser.yaml.
PLATFORMS=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64)

# go-licenses v2.0.1 cannot classify the short Apache-2.0 notice in these
# modules' LICENSE files (google/go-licenses#186), and save fails on them.
# They are ignored by go-licenses and added by hand below.
APACHE_OVERRIDES=(github.com/in-toto/attestation github.com/in-toto/in-toto-golang)

generate_licenses() {
    local goos="$1" goarch="$2" out="$3"
    export GOOS="$goos" GOARCH="$goarch"
    echo "Generating licenses for ${goos}/${goarch}..." >&2

    local ignores=()
    for mod in "${APACHE_OVERRIDES[@]}"; do
        ignores+=(--ignore "$mod")
    done

    mkdir -p "$out"
    rm -rf "$out/third-party" "$out/report.txt"

    local rows
    rows=$("$GO_LICENSES" report ./... --template scripts/licenses.tmpl "${ignores[@]}")
    "$GO_LICENSES" save ./... --save_path="$out/third-party" "${ignores[@]}" --force

    # save copies clawker's own license files; vendored code below them stays.
    find "$out/third-party/github.com/schmitthub/clawker" -maxdepth 1 -type f -delete

    local modules
    modules=$(go list -deps -f '{{with .Module}}{{.Path}} {{.Version}} {{.Dir}}{{end}}' ./... | sort -u)
    for mod in "${APACHE_OVERRIDES[@]}"; do
        local line path version dir
        line=$(awk -v m="$mod" '$1 == m' <<<"$modules")
        if [[ -z "$line" ]]; then
            echo "ERROR: ${mod} is not a ${goos}/${goarch} dependency; remove it from APACHE_OVERRIDES" >&2
            exit 1
        fi
        read -r path version dir <<<"$line"
        mkdir -p "$out/third-party/$path"
        cp "$dir/LICENSE" "$out/third-party/$path/LICENSE"
        rows+=$'\n'"${path} (${version}) - Apache-2.0 - https://${path}/blob/${version}/LICENSE"
    done

    {
        echo "clawker third-party dependencies"
        echo "================================"
        echo
        echo "The following open source dependencies are used to build clawker."
        echo
        LC_ALL=C sort <<<"$rows"
    } > "$out/report.txt"

    # save copies full source for some licenses (e.g. MPL-2.0), and go:embed
    # rejects directories that contain go.mod or .go files. Keep only
    # license and notice files.
    find "$out/third-party" -type f \
        ! -iname 'LICENSE*' \
        ! -iname 'LICENCE*' \
        ! -iname 'NOTICE*' \
        ! -iname 'COPYING*' \
        ! -iname 'PATENTS*' \
        -delete
    find "$out/third-party" -type d -empty -delete
}

if [[ "${1:-}" == "--check" && $# -eq 1 ]]; then
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    for platform in "${PLATFORMS[@]}"; do
        goos="${platform%/*}" goarch="${platform#*/}"
        (generate_licenses "$goos" "$goarch" "$tmp/${goos}-${goarch}")
    done
    echo "License generation verified for all platforms." >&2
elif [[ $# -eq 2 ]]; then
    generate_licenses "$1" "$2" "internal/cmd/licenses/embed/${1}-${2}"
    echo "Licenses written to internal/cmd/licenses/embed/${1}-${2}" >&2
else
    echo "Usage: $0 <GOOS> <GOARCH>" >&2
    echo "       $0 --check" >&2
    exit 1
fi
