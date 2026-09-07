Project instruction files use `AGENTS.md` as the regular source file. Every `CLAUDE.md` must be a symbolic link with the relative target `AGENTS.md`, in the same directory. Never write instructions into a regular `CLAUDE.md` or use an absolute link target. The root `AGENTS.md` states this rule.

The 2026-08-17 conversion covered the main repository and excluded `.claude/` and Git submodules at that time. These historical exclusions do not change the user's rule.

PR 519 check, 2026-09-07: `controlplane/sdscerts/CLAUDE.md` was the only regular `CLAUDE.md` added or changed by the PR. Its contents now reside in the sibling `AGENTS.md`; `CLAUDE.md` is a relative link to that file. The PR diff check passed.
