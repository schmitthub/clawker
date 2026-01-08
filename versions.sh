#!/usr/bin/env bash
set -Eeuo pipefail

# Logging
DEBUG_MODE=false
debug_log() {
    if [[ "$DEBUG_MODE" == "true" ]]; then
        printf "[DEBUG] %s\n" "$*" >&2
    fi
}

# Parse options and collect non-option arguments
ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -d|--debug)
      DEBUG_MODE=true
      echo "Debug mode enabled"
      shift
      ;;
    *)
      ARGS+=("$1")
      shift
      ;;
  esac
done

# Restore arguments for versions
set -- "${ARGS[@]}"

# Prepare versions
cd "$(dirname "$(readlink -f "$BASH_SOURCE")")"

versions=( "$@" )
if [ ${#versions[@]} -eq 0 ]; then
	versions=( */ )
	json='{}'
else
	json="$(< versions.json)"
fi
versions=( "${versions[@]%/}" )

debug_log "versions to process: ${versions[*]}"

# Variants configuration
debianDefault="trixie"
alpineDefault="alpine3.23"
debug_log "debianDefault: $debianDefault"
debug_log "alpineDefault: $alpineDefault"

# Define supported variants and their suffixes, leave empty array for no suffixes
supportedVariants="$(jq -c '.' <<< '{
    "trixie": ["-slim"],
    "bookworm": ["-slim"],
    "alpine3.23": [],
    "alpine3.22": []
}')"

debug_log "supportedVariants raw: $supportedVariants"

supportedArches=(
  amd64
  arm64v8
)

debug_log "supportedArches: ${supportedArches[*]}"

# Convert supportedArches array to JSON
supportedArchesJson="$(printf '%s\n' "${supportedArches[@]}" | jq -R . | jq -s -c .)"

# Generate variants JSON structure
variants="$(jq -c --argjson arches "$supportedArchesJson" '
  # Convert object to array of entries and process each
  to_entries | map(
    .key as $base |
    .value as $suffixes |

    # Always create base variant
    [{key: $base, value: $arches}] +

    # Create suffixed variants if suffixes exist
    (if ($suffixes | length) > 0 then
      $suffixes | map({key: ($base + .), value: $arches})
    else
      []
    end)
  ) |
  # Flatten and convert back to object
  flatten |
  from_entries
' <<<"$supportedVariants")"

debug_log "variants: $variants"

debug_log "supportedVariants: $supportedVariants"

debug_log "json: $json"

ccVersions="$(
  npm view @anthropic-ai/claude-code versions --json | jq -c
)"

ccTagedVersions="$(
  npm view @anthropic-ai/claude-code dist-tags --json | jq -c
)"

debug_log "ccVersions: $ccVersions"
debug_log "ccTagedVersions: $ccTagedVersions"

# Initialize empty object for matched versions
ccJson='{}'

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

      # Parse the fullVersion into semverMatch
      if ! semverMatch="$(parse_semver "$fullVersion")"; then
        echo >&2 "warning: invalid fullVersion format '$fullVersion'"
        continue
      fi

      debug_log "semverMatch for $version ($fullVersion): $semverMatch"
      ;;
    *)
      # Validate and parse semver pattern (allows partial versions)
      if ! semverMatch="$(parse_semver "$version")"; then
        echo >&2 "warning: invalid version format '$version'"
        continue
      fi

      debug_log "semverMatch for $version: $semverMatch"

      # Find best matching version from ccVersions array
      if \
        ! fullVersion="$(jq -r -L . --arg target "$version" 'include "semver"; match_semver($target)' <<< "$ccVersions")" \
        || [ -z "$fullVersion" ] \
      ; then
        echo >&2 "warning: cannot find version matching '$version'"
        continue
      fi
      ;;
  esac

  echo "Full version for $version: $fullVersion"

  # Extract major.minor version (e.g., "2.1.1" -> "2.1")
  # minorVersion="$(echo "$fullVersion" | cut -d'.' -f1-2)"

  # Add fullVersion to the appropriate minor version key, sorted from highest to lowest
  # ccJson="$(jq -c --arg minorVersion "$minorVersion" --arg fullVersion "$fullVersion" --arg debianDefault "$debianDefault" --argjson semverGroup "$semverMatch" --arg alpineDefault "$alpineDefault" --argjson variants "$variants" '
  #   # Ensure the key exists as an array
  #   if .[$minorVersion] == null then
  #     .[$minorVersion] = []
  #   else
  #     .
  #   end |
  #   # Add the new version as an object and sort descending by semantic version
  #   .[$minorVersion] += [
  #     {
  #       fullVersion: $fullVersion,
  #       version: $semverGroup,
  #       "debian-default": $debianDefault,
  #       "alpine-default": $alpineDefault,
  #       variants: $variants
  #     }
  #   ] |
  #   .[$minorVersion] |= (
  #     unique_by(.fullVersion) |
  #     sort_by(.fullVersion | split(".") | map(tonumber)) |
  #     reverse
  #   )
  # ' <<<"$ccJson")"

  # Add fullVersion as its own key in ccJson and sorty from highest semver to lowest
  ccJson="$(jq -c --arg fullVersion "$fullVersion" --argjson semverGroup "$semverMatch" --arg debianDefault "$debianDefault" --argjson variants "$variants" --arg alpineDefault "$alpineDefault" '
    .[$fullVersion] = {
      fullVersion: $fullVersion,
      version: $semverGroup,
      "debian-default": $debianDefault,
      "alpine-default": $alpineDefault,
      variants: $variants
    }
  ' <<<"$ccJson")"

done

debug_log "ccJson: $ccJson"

# Sort keys by semantic version (highest to lowest) and write to versions.json
jq 'to_entries | sort_by(.key | split(".") | map(tonumber)) | reverse | from_entries' <<<"$ccJson" > versions.json
