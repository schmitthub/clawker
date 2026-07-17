# Multi-harness follow-ups — open items

Post-PR-#416 follow-up queue for the multi-harness initiative. Distinct from the
plugin-migration initiative (skill distribution rework — see
`multi-harness/skill-distribution-native-install-research`). Add new items to
the list; mark items DONE with the shipping commit rather than deleting them.

## Open items

1. **Embedded stacks for every init preset** (user, 2026-07-15) — FILED as
   github issue #421 (2026-07-17, post-#416-merge). The init
   wizard presets (`internal/config/presets.go`) cover python, go, rust,
   typescript, java, ruby, cpp, and dotnet, but the embedded stack floor
   (`internal/bundle/assets/stacks/`) ships only `go`, `node`, `python`,
   `rust`. Add built-in stacks for the rest — java, ruby, cpp, dotnet — so
   every preset's language has a first-class `build.stacks` entry (typescript
   rides `node`). Presets' `build.instructions`/`packages` blocks that
   hand-roll those toolchains should then select the stack instead. Follow the
   existing stack conventions: `stack.yaml` + root/user Dockerfile fragments,
   self-guarding installs (skip when the runtime is already present), and
   runtimes usable by the unprivileged user.
