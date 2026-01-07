#!/usr/bin/env bash
set -Eeuo pipefail


cd "$(dirname "$(readlink -f "$BASH_SOURCE")")"

versions=( "$@" )
if [ ${#versions[@]} -eq 0 ]; then
	versions=( */ )
	json='{}'
else
	json="$(< versions.json)"
fi
versions=( "${versions[@]%/}" )

echo "json: $json"
echo "versions: $versions"

ccVersions="$(
  npm view @anthropic-ai/claude-code versions --json | jq -c
)"

ccTagedVersions="$(
  npm view @anthropic-ai/claude-code dist-tags --json | jq -c
)"

echo "ccVersions: $ccVersions"
echo "ccTagedVersions: $ccTagedVersions"

for version in "${versions[@]}"; do
	export version

  case "$version" in
    latest|stable|next)
      if \
        ! fullVersion="$(jq -r --arg version "$version" '.[$version]' <<<"$ccTagedVersions")" \
        || [ -z "$fullVersion" ] \
      ; then
        echo >&2 "warning: cannot find full version for $version"
        continue
      fi
      ;;
    *)
      # Validate version pattern
      if ! [[ "$version" =~ ^[0-9]+(\.[0-9]+)*$ ]]; then
        echo >&2 "warning: invalid version format '$version' (must be a valid semantic version: numbers and dots or the tags 'latest', 'stable', 'next')"
        exit 1
      fi

      # Find best matching version from ccVersions array
      if \
        ! fullVersion="$(jq -r --arg version "$version" '
          # Filter to versions that start with the user input
          map(select(startswith($version))) |
          # Sort by semantic version (convert to numbers for proper sorting)
          sort_by(split(".") | map(tonumber)) |
          # Get the last (highest) version, or empty if no matches
          last // empty
        ' <<<"$ccVersions")" \
        || [ -z "$fullVersion" ] \
      ; then
        echo >&2 "warning: cannot find version matching '$version'"
        continue
      fi
      ;;
  esac

  echo "Full version for $version: $fullVersion"

done

