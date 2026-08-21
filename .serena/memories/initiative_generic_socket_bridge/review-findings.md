# Socket Bridge — Open Review Findings

Overwritten each review round. Open items only.

## N2 [MED] Untrusted Purpose text rendered raw into the security prompt
`promptForSocketGrant` prints harness-authored `Purpose` (and listener
Owner/Group names) with plain `%s` into the allow/deny prompt. Newlines
or ANSI escapes can forge prompt lines or hide the firewall-bypass
warning. Sanitize before display: strip control characters and escape
sequences, clamp to one line.

## N3 [LOW] Orphaned `<containerID>.sockets.json`
`startBridge` writes it; cleanup removes only the PID file. Remove the
sockets file alongside the PID file.

## N4 [in progress] Finish the lint sweep
Touched packages still fail golangci-lint (wrapcheck 16, usetesting 3,
unparam 2, tparallel 1, unconvert 1, plus remainder). Per-package clean
runs required; root-cause fixes, no new nolints.
