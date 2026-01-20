## clawker generate

Generate Dockerfiles for Claude Code releases

### Synopsis

Fetches Claude Code versions from npm and generates Dockerfiles.

Generates versions.json and Dockerfiles for each version/variant combination.
Files are saved to ~/.clawker/build/ (or use --output to specify a directory).

If no versions are specified, displays current versions.json.
If versions are specified, fetches them from npm and generates Dockerfiles.

Version patterns:
  latest, stable, next   Resolve via npm dist-tags
  2.1                    Match highest 2.1.x release
  2.1.2                  Exact version match

```
clawker generate [versions...] [flags]
```

### Examples

```
  # Generate Dockerfiles for latest version
  clawker generate latest

  # Generate for multiple versions
  clawker generate latest 2.1

  # Output to specific directory
  clawker generate --output ./build latest

  # Show existing versions.json
  clawker generate
```

### Options

```
      --cleanup         Remove obsolete version directories (default true)
  -h, --help            help for generate
  -o, --output string   Output directory for generated files
      --skip-fetch      Skip npm fetch, use existing versions.json
```

### Options inherited from parent commands

```
  -D, --debug            Enable debug logging
  -w, --workdir string   Working directory (default: current directory)
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
