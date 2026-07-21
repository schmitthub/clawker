# Plugin Issue Tracking

- [ ] Support plugin seems to prefer adding runtime dep installs instead of using package injections / run as instructions
- [ ] General agent prompt seems to avoid asking for firewall assistance (open for me), and avoids adding proper granular egress rules
- [ ] Support plugin doesn't seem to attempt to check for installed stacks or honor their env. like for a project with nvm installed, it will still try to globally install npm packages as root 

  Issue Summary: Thrashing on a simple .clawker.yaml language-server setup

  What the task actually was

  Add three Serena language servers (bash, markdown, lua) + shellcheck to a project's clawker build, and set serena ls_path so they don't
  download at runtime. ~15 lines of YAML. It took ~an hour and 6+ full reversals.

  Root cause (one sentence)

  I never ran the skill's mandatory research phase before editing. I treated clawker build config as generic Docker/shell knowledge and
  edited from memory + web research, when the skill explicitly requires reading Dockerfile.tmpl, the stack definitions, the sample configs,
  and the schema first. I only opened Dockerfile.tmpl after the build was already broken.

  Every reversal, and the template line that would have prevented it

  ┌─────┬────────────────────────────────────┬─────────────────────────────────────────────────────────────────────────────────────────┐
  │  #  │              Reversal              │                           Fact I'd have known from grounding                            │
  ├─────┼────────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
  │ 1   │ root npm -g → user --prefix        │ Dockerfile.tmpl:497-506: user_run runs under /bin/zsh with nvm pre-sourced; :448        │
  │     │ ~/.local → back to root → nvm      │ ~/.local/bin is on PATH                                                                 │
  ├─────┼────────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
  │ 2   │ . "$NVM_DIR/nvm.sh" re-init        │ Redundant — zsh sources .zshenv (nvm) every invocation                                  │
  ├─────┼────────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
  │ 3   │ root_run for gh/binaries touching  │ :317 root_run runs before useradd — $HOME doesn't exist yet                             │
  │     │ $HOME                              │                                                                                         │
  ├─────┼────────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
  │ 4   │ lua crash → chown hack →           │ lua-language-server writes cache into its own install dir; root-owned tree = EACCES.    │
  │     │ user-install                       │ Discovered by trial + a rebuild, not reasoning                                          │
  ├─────┼────────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
  │ 5   │ redundant mkdir -p ~/.local/bin    │ ~/.local/bin pre-created and on PATH per template                                       │
  ├─────┼────────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
  │ 6   │ stale version pins (marksman       │ Current were 2026-02-08 / 3.18.2 — I had GitHub tools + gh and didn't check releases    │
  │     │ 2024-12-18, lua 3.15.0)            │                                                                                         │
  ├─────┼────────────────────────────────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
  │ 7   │ never inventoried stacks; then     │ node stack already bakes Node LTS + nvm + TypeScript; python bakes uv + CPython.        │
  │     │ read the wrong ones                │ Embedded defaults live at internal/bundle/assets/stacks/, not the test-bundle           │
  └─────┴────────────────────────────────────┴─────────────────────────────────────────────────────────────────────────────────────────┘

  What actually made me confused

  1. The skill's "Step 3: Research — mandatory, never skip" reads as advice, not a hard gate. Under an implicit "just make the edit"
  pressure I deprioritized it. Nothing forced me to confirm I'd read the template before touching build.instructions.
  2. The build execution model (who/where/what-shell each instruction runs as) is only in a reference file, not surfaced in the skill body.
  So I speculated about root-vs-user, PATH, and nvm instead of knowing.
  3. I never captured the actual clawker build error — kept editing speculatively instead of reading the failure.
  4. I conflated three different install mechanics ("it's a binary") when they're genuinely different: npm package (needs nvm), static
  single binary (marksman), stateful tarball that writes to its own dir (lua-ls).

  Suggested skill changes to prevent this

  1. Make Step 3 a hard precondition with a checklist. Before any edit to build.instructions / stacks / packages, require confirming: read
  Dockerfile.tmpl, read the resolved stacks, read a sample, checked the schema.
  2. Inline a "Build Execution Model" cheat-sheet in the skill body:
    - root_run → runs before user creation, as root, target /usr/local, no $HOME.
    - user_run → runs as ${USERNAME} under /bin/zsh, nvm already sourced, ~/.local/bin on PATH (XDG installs land on PATH automatically).
    - packages → apt/apk as root at build.
  3. Add an explicit stacks step: "Inventory the resolved stacks (embedded defaults at internal/bundle/assets/stacks/<name> unless a bundle
  overrides) before adding anything — node already provides Node+nvm+tsc, python provides uv+CPython." Clarify embedded-default vs bundle
  resolution so the model reads the right file.
  4. Troubleshooting: "capture the actual clawker build output before proposing edits."
  5. Tooling note: for dev-tool/language-server versions, query current releases (GitHub MCP / gh), never pin from training data.
  6. Add a Serena ls_path recipe noting the stateful-server gotcha (lua-language-server writes log/cache into its install dir → must be
  user-writable), so it's known, not rediscovered.
