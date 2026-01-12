#!/usr/bin/env bash
set -Eeuo pipefail

# Config globals

SCRIPT_DIR="$(dirname "$(readlink -f "$BASH_SOURCE")")"
TEMPLATES_DIR="$SCRIPT_DIR/build/templates"
DOCKERFILES_DIR="$SCRIPT_DIR/dockerfiles"

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

[ -f versions.json ] # run "versions.sh" first

jqt='.jq-template.awk'
if [ -n "${BASHBREW_SCRIPTS:-}" ]; then
	jqt="$BASHBREW_SCRIPTS/jq-template.awk"
elif [ "$BASH_SOURCE" -nt "$jqt" ]; then
	# https://github.com/docker-library/bashbrew/blob/master/scripts/jq-template.awk
	wget -qO "$jqt" 'https://github.com/docker-library/bashbrew/raw/9f6a35772ac863a0241f147c820354e4008edf38/scripts/jq-template.awk'
fi

if [ "$#" -eq 0 ]; then
	versions="$(jq -r 'keys | map(@sh) | join(" ")' versions.json)"
	eval "set -- $versions"
fi

debug_log "versions to process: $*"

generated_warning() {
	cat <<-EOH
		#
		# NOTE: THIS DOCKERFILE IS GENERATED VIA "apply-templates.sh"
		#
		# PLEASE DO NOT EDIT IT DIRECTLY.
		#

	EOH
}

cd "$(dirname "$(readlink -f "$BASH_SOURCE")")"

# collect list of existing top level version directories in ./dockerfiles/$1 for cleanup if that version is no longer in versions.json
existingVersionDirs=()
while IFS= read -r -d '' dir; do
  existingVersionDirs+=( "$(basename "$dir")" )
done < <(find ./dockerfiles -mindepth 1 -maxdepth 1 -type d -print0)


debug_log "existing version dirs: ${existingVersionDirs[*]}"

for version; do
  debug_log "processing version: $version"

	export version

	rm -rf "$DOCKERFILES_DIR/$version/"

	variants="$(jq -r '.["'"$version"'"].variants | keys | map(@sh) | join(" ")' versions.json)"
  debug_log "variants for $version: $variants"

	eval "variants=( $variants )"

	for dir in "${variants[@]}"; do
		mkdir -p "$DOCKERFILES_DIR/$version/$dir"

		variant="$(basename "$dir")" # "buster", "windowsservercore-1809", etc
		export variant

		template="$TEMPLATES_DIR/Dockerfile.template"

		echo "processing $version/$dir ..."

		{
			generated_warning
			gawk -f "$jqt" "$template"
		} > "$DOCKERFILES_DIR/$version/$dir/Dockerfile"

  # remove this version from existingVersionDirs (it still exists in versions.json)
  for i in "${!existingVersionDirs[@]}"; do
    if [[ "${existingVersionDirs[i]}" == "$version" ]]; then
      unset 'existingVersionDirs[i]'
    fi
  done

	done
done

# cleanup any existing version directories that are no longer in versions.json
# TODO: this could be risky need to revisit this logic
for dir in "${existingVersionDirs[@]}"; do
  debug_log "removing obsolete version dir: $dir"
  rm -rf "$DOCKERFILES_DIR/$dir"
done
