#!/usr/bin/env bash
set -Eeuo pipefail

supportedArches=(
  linux/amd64
  linux/arm64
)

supportedArches="$(jq -sRc <<<"${supportedArches[*]}" 'rtrimstr("\n") | split(" ")')"

echo "$supportedArches"

# cd "$(dirname "$(readlink -f "$BASH_SOURCE")")"

claudeCodeVersions="$(
  npm view @anthropic-ai/claude-code versions --json | jq -c
)"
echo "$claudeCodeVersions"
