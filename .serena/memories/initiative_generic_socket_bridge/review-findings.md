# Socket Bridge — Open Review Findings

Overwritten each review round. Open items only.

## N4 [in progress] Finish the lint sweep
Touched packages still fail golangci-lint (wrapcheck 16, usetesting 3,
unparam 2, tparallel 1, unconvert 1, plus remainder). Per-package clean
runs required; root-cause fixes, no new nolints.
