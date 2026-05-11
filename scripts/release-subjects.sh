#!/usr/bin/env bash
#
# release-subjects.sh — Emit the 16-subject sha256sum-style file consumed by
# actions/attest@v4's `subject-checksums:` input.
#
# Subjects:
#   1. 4 goreleaser archives (lifted from dist/checksums.txt)
#   2. 4 unpacked `clawker` CLI binaries, one per archive (extracted via
#      `tar -xzOf` so we never touch the tarball's mtimes/permissions)
#   3. 8 embedded Linux binaries under embeds/<arch>/ (clawkerd, clawker-cp,
#      ebpf-manager, coredns-clawker × {amd64, arm64})
#
# The output format is sha256sum-style (`<hex>␣␣<name>`) so the file can be
# verified with `sha256sum --check` against the unpacked artifacts in a
# reproducer.
#
# Usage: bash scripts/release-subjects.sh <version>
#   version   Release version tag without leading 'v' (e.g. "0.1.0").
#             Used to construct goreleaser's archive names.
#
# Inputs:
#   dist/checksums.txt          — goreleaser-generated archive digests
#   dist/clawker_<v>_<os>_<a>.tar.gz × 4 — goreleaser archives
#   embeds/<arch>/<binary>      — staged linux embeds (8 files)
#
# Output:
#   dist/attestation-subjects.txt
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "Usage: $0 <version-without-leading-v>" >&2
  exit 1
fi
VERSION="$1"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

DIST=dist
EMBEDS=embeds
OUT="$DIST/attestation-subjects.txt"

# 4 expected archives — fail fast if any are missing so the attestation
# never silently ships fewer subjects than promised.
ARCHIVES=(
  "clawker_${VERSION}_linux_amd64.tar.gz"
  "clawker_${VERSION}_linux_arm64.tar.gz"
  "clawker_${VERSION}_darwin_amd64.tar.gz"
  "clawker_${VERSION}_darwin_arm64.tar.gz"
)
for a in "${ARCHIVES[@]}"; do
  if [ ! -f "$DIST/$a" ]; then
    echo "ERROR: missing $DIST/$a" >&2
    exit 1
  fi
done

# 8 expected embed binaries.
ARCHES=(amd64 arm64)
EMBEDS_LIST=(clawkerd clawker-cp ebpf-manager coredns-clawker)
for arch in "${ARCHES[@]}"; do
  for bin in "${EMBEDS_LIST[@]}"; do
    if [ ! -f "$EMBEDS/$arch/$bin" ]; then
      echo "ERROR: missing $EMBEDS/$arch/$bin" >&2
      exit 1
    fi
  done
done

: > "$OUT"

# 1. Archives — copy the four archive lines verbatim from goreleaser's
# checksums.txt. Goreleaser emits `<hex>  <name>` (two spaces), the same
# sha256sum format actions/attest expects.
for a in "${ARCHIVES[@]}"; do
  if ! grep -E "  ${a}\$" "$DIST/checksums.txt" >> "$OUT"; then
    echo "ERROR: $a not in $DIST/checksums.txt" >&2
    exit 1
  fi
done

# 2. Bare `clawker` binary inside each archive. `tar -xzOf` streams the
# member to stdout without extracting; sha256sum reads from stdin. Subject
# name uses os-arch suffix so the verifier sees one entry per (os, arch).
for a in "${ARCHIVES[@]}"; do
  # Names look like clawker_<v>_<os>_<arch>.tar.gz — strip the prefix/suffix
  # to recover the os-arch component.
  osArch="${a#clawker_${VERSION}_}"
  osArch="${osArch%.tar.gz}"
  digest=$(tar -xzOf "$DIST/$a" clawker | sha256sum | awk '{print $1}')
  printf '%s  clawker-%s\n' "$digest" "$osArch" >> "$OUT"
done

# 3. 8 embed binaries.
for arch in "${ARCHES[@]}"; do
  for bin in "${EMBEDS_LIST[@]}"; do
    digest=$(sha256sum "$EMBEDS/$arch/$bin" | awk '{print $1}')
    printf '%s  %s-linux-%s\n' "$digest" "$bin" "$arch" >> "$OUT"
  done
done

count=$(wc -l < "$OUT")
if [ "$count" -ne 16 ]; then
  echo "ERROR: expected 16 subjects, got $count" >&2
  cat "$OUT" >&2
  exit 1
fi

echo "Wrote $count subjects to $OUT"
