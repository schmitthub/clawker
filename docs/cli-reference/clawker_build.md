---
title: "clawker build"
---

## clawker build

Build the project image

### Synopsis

Build the project image from its clawker configuration.

Tags are harness-keyed: -t NAME builds that registered harness; -t name:NAME
adds an extra ref (tag part must name a registered harness). No -t builds the
default harness and adds the :default alias.

```
clawker build [OPTIONS] [flags]
```

### Examples

```
  # Build the default harness image
  clawker build

  # Build a specific registered harness
  clawker build -t codex

  # Rebuild from scratch
  clawker build --no-cache
```

### Options

```
      --build-arg stringArray   Set build-time variables (format: KEY=VALUE)
  -f, --file string             Path to Dockerfile (overrides build.dockerfile in config)
  -h, --help                    help for build
      --iidfile string          Write the built image's ID/digest to this file (docker buildx --iidfile shape)
      --label stringArray       Set metadata for the image (format: KEY=VALUE)
      --network string          Set the networking mode for the RUN instructions during build
      --no-cache                Do not use cache when building the image
      --progress string         Set type of progress output (auto, plain, tty, none) (default "auto")
      --pull                    Always attempt to pull a newer version of the base image
  -q, --quiet                   Suppress the build output
  -t, --tag stringArray         Registered harness to build, or an extra ref whose tag names one (format: HARNESS or name:HARNESS)
      --target string           Set the target build stage to build
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker) - Run coding agents in secure Docker containers with clawker
