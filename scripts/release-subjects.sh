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

# assert_digest validates a sha256sum capture is exactly 64 lowercase hex.
# Defence-in-depth against tar/sha256sum producing the empty-string digest
# `e3b0c4...` (zero-byte input) — the wc -l guard would still report 16
# subjects but one of them would be garbage.
assert_digest() {
  local d="$1" ctx="$2"
  if ! [[ "$d" =~ ^[0-9a-f]{64}$ ]]; then
    echo "ERROR: bad sha256 digest for ${ctx}: ${d:-<empty>}" >&2
    exit 1
  fi
}

# 2. Bare `clawker` binary inside each archive. `tar -xzOf` streams the
# member to stdout without extracting; sha256sum reads from stdin. Subject
# name uses os-arch suffix so the verifier sees one entry per (os, arch).
for a in "${ARCHIVES[@]}"; do
  # Names look like clawker_<v>_<os>_<arch>.tar.gz — strip the prefix/suffix
  # to recover the os-arch component.
  osArch="${a#clawker_${VERSION}_}"
  osArch="${osArch%.tar.gz}"
  digest=$(tar -xzOf "$DIST/$a" clawker | sha256sum | awk '{print $1}')
  assert_digest "$digest" "$a/clawker"
  printf '%s  clawker-%s\n' "$digest" "$osArch" >> "$OUT"
done

# 3. 8 embed binaries.
for arch in "${ARCHES[@]}"; do
  for bin in "${EMBEDS_LIST[@]}"; do
    digest=$(sha256sum "$EMBEDS/$arch/$bin" | awk '{print $1}')
    assert_digest "$digest" "$EMBEDS/$arch/$bin"
    printf '%s  %s-linux-%s\n' "$digest" "$bin" "$arch" >> "$OUT"
  done
done

count=$(wc -l < "$OUT")
if [ "$count" -ne 16 ]; then
  echo "ERROR: expected 16 subjects, got $count" >&2
  cat "$OUT" >&2
  exit 1
fi

# Subject names (column 2) must all be distinct. wc -l catches an under-count
# but a duplicate line would still produce 16 entries with one subject double-
# attested and another missing.
unique_names=$(awk '{print $2}' "$OUT" | sort -u | wc -l)
if [ "$unique_names" -ne 16 ]; then
  echo "ERROR: subject names not unique (got $unique_names distinct out of 16)" >&2
  awk '{print $2}' "$OUT" | sort | uniq -c | sort -rn >&2
  exit 1
fi

echo "Wrote $count subjects to $OUT"
