## clawker build

Build an image from a clawker project

### Synopsis

Builds a container image from a clawker project configuration.

The image is built from the project's clawker.yaml configuration,
generating a Dockerfile and building the image. Alternatively,
use -f/--file to specify a custom Dockerfile.

Multiple tags can be applied to the built image using -t/--tag.
Build-time variables can be passed using --build-arg.

```
clawker build [OPTIONS] [flags]
```

### Examples

```
  # Build the project image
  clawker build

  # Build without Docker cache
  clawker build --no-cache

  # Build using a custom Dockerfile
  clawker build -f ./Dockerfile.dev

  # Build with multiple tags
  clawker build -t myapp:latest -t myapp:v1.0

  # Build with build arguments
  clawker build --build-arg NODE_VERSION=20

  # Build a specific target stage
  clawker build --target builder

  # Build quietly (suppress output)
  clawker build -q

  # Always pull base image
  clawker build --pull
```

### Options

```
      --build-arg stringArray   Set build-time variables (format: KEY=VALUE)
  -f, --file string             Path to Dockerfile (overrides build.dockerfile in config)
  -h, --help                    help for build
      --label stringArray       Set metadata for the image (format: KEY=VALUE)
      --network string          Set the networking mode for the RUN instructions during build
      --no-cache                Do not use cache when building the image
      --progress string         Set type of progress output (auto, plain, tty, none) (default "auto")
      --pull                    Always attempt to pull a newer version of the base image
  -q, --quiet                   Suppress the build output
  -t, --tag stringArray         Name and optionally a tag (format: name:tag)
      --target string           Set the target build stage to build
```

### Options inherited from parent commands

```
  -D, --debug   Enable debug logging
```

### See also

* [clawker](clawker.md) - Manage Claude Code in secure Docker containers with clawker
