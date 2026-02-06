# No GCC in default dockerfile template

## Status: DONE

## Summary

Added `gcc` + `musl-dev` (Alpine) and `gcc` + `libc6-dev` (Debian) to the default Dockerfile template
in `internal/bundler/assets/Dockerfile.tmpl`. This ensures C compilation toolchains are available
for clawker agents that need to build native extensions or C code.
