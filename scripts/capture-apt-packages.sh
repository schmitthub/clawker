#!/usr/bin/env bash
#
# capture-apt-packages.sh — Build the bpf-builder stage of
# Dockerfile.controlplane and dump its `dpkg-query` package closure to a
# file. Used by the release pipeline to enrich SLSA v1 provenance with the
# transitive apt package set (not just the four ARG'd direct installs).
#
# Usage: bash scripts/capture-apt-packages.sh <out-file>
#
# Output format (one package per line):
#   <pkg>=<version>|<arch>
#
# This format is consumed by cmd/gen-provenance as one
# pkg:deb/debian/<pkg>@<version>?arch=<arch> ResourceDescriptor per line.
#
# Runs `docker buildx build --target bpf-builder --load` which reuses the
# buildx cache populated by `make release-embeds` (the bpf-builder stage is
# upstream of every embed target), so the marginal cost over an existing
# release-embeds run is just the `dpkg-query` invocation in a throwaway
# container.
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "Usage: $0 <out-file>" >&2
  exit 1
fi
OUT="$1"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# Local-only tag — never pushed. The image survives until the runner shuts
# down; on a fresh GitHub-hosted runner that's automatic cleanup.
TAG=clawker-bpf-builder:provenance

docker buildx build \
  -f Dockerfile.controlplane \
  --target bpf-builder \
  --load \
  -t "$TAG" \
  .

mkdir -p "$(dirname "$OUT")"
docker run --rm "$TAG" \
  dpkg-query -W -f '${Package}=${Version}|${Architecture}\n' \
  > "$OUT"

count=$(wc -l < "$OUT")
if [ "$count" -lt 5 ]; then
  echo "ERROR: apt package closure has only $count entries (expected at least 5 — clang/llvm/libbpf-dev/linux-libc-dev/ca-certificates)" >&2
  cat "$OUT" >&2
  exit 1
fi

echo "Captured $count apt packages to $OUT"
