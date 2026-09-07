#!/usr/bin/env bash
# Run govulncheck with the caller's Go toolchain settings.

set -euo pipefail

exec govulncheck ./...
