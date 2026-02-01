#!/bin/bash

set -euo pipefail

goda graph 'github.com/schmitthub/clawker/...' -short | dot -Tpng -o internal-deps.png
