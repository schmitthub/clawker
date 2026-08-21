# Socket Bridge — Open Review Findings

Overwritten each review round. Open items only.

## N3 [LOW] Orphaned `<containerID>.sockets.json`
`startBridge` writes it; cleanup removes only the PID file. Remove the
sockets file alongside the PID file.

## N4 [in progress] Finish the lint sweep
Touched packages still fail golangci-lint (wrapcheck 16, usetesting 3,
unparam 2, tparallel 1, unconvert 1, plus remainder). Per-package clean
runs required; root-cause fixes, no new nolints.
