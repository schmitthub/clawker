# Multi-harness follow-ups — open items

Post-PR-#416 follow-up queue for the multi-harness initiative. Distinct from the
plugin-migration initiative (skill distribution rework — see
`multi-harness/skill-distribution-native-install-research`). Add new items to
the list; mark items DONE with the shipping commit rather than deleting them.

## Open items

1. **New aliases to help users** (user, 2026-07-15). Add aliases that ease the
   multi-harness workflow. Scope not yet specified — clarify with the user
   before implementing: which commands, and whether these are shipped built-in
   aliases or documented `clawker alias set` recipes. Context: harness
   selection currently spells `clawker run -it --agent dev @:codex`,
   `clawker build -t <harness>`, etc.; inventory verbs are
   `clawker harness list` / `stack list` / `monitor extensions`.
