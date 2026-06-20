export const meta = {
  name: 'docs-staleness-sweep',
  description: 'Audit + fix docs/comments against current code: stale, inaccurate, redundant. Per-file fix then adversarial verify.',
  phases: [
    { title: 'Skill docs' },
    { title: 'User docs' },
    { title: 'CLAUDE.md' },
    { title: '.claude docs/rules' },
    { title: 'Code comments' },
    { title: 'Verify' },
  ],
}

// ---- args ----
const data = {"claude_md": ["CLAUDE.md", "claude-plugin/CLAUDE.md", "claude-plugin/clawker-support/CLAUDE.md", "clawkerd/CLAUDE.md", "internal/clawkerd/CLAUDE.md", "cmd/coredns-clawker/CLAUDE.md", "cmd/coredns-clawker/plugins/otel/CLAUDE.md", "internal/auth/CLAUDE.md", "internal/build/CLAUDE.md", "internal/bundler/CLAUDE.md", "internal/bundler/registry/CLAUDE.md", "internal/bundler/semver/CLAUDE.md", "internal/clawker/CLAUDE.md", "internal/cmd/bridge/CLAUDE.md", "internal/cmd/container/attach/CLAUDE.md", "internal/cmd/container/CLAUDE.md", "internal/cmd/container/exec/CLAUDE.md", "internal/cmd/container/shared/CLAUDE.md", "internal/cmd/container/start/CLAUDE.md", "internal/cmd/controlplane/CLAUDE.md", "internal/cmd/factory/CLAUDE.md", "internal/cmd/firewall/CLAUDE.md", "internal/cmd/generate/CLAUDE.md", "internal/cmd/hostproxy/CLAUDE.md", "internal/cmd/image/CLAUDE.md", "internal/cmd/init/CLAUDE.md", "internal/cmd/monitor/CLAUDE.md", "internal/cmd/network/CLAUDE.md", "internal/cmd/project/CLAUDE.md", "internal/cmd/root/CLAUDE.md", "internal/cmd/settings/CLAUDE.md", "internal/cmd/skill/CLAUDE.md", "internal/cmdutil/CLAUDE.md", "internal/cmd/version/CLAUDE.md", "internal/cmd/volume/CLAUDE.md", "internal/cmd/worktree/CLAUDE.md", "internal/config/CLAUDE.md", "internal/containerfs/CLAUDE.md", "internal/controlplane/agent/CLAUDE.md", "internal/controlplane/CLAUDE.md", "internal/controlplane/manager/CLAUDE.md", "internal/controlplane/firewall/CLAUDE.md", "internal/controlplane/firewall/ebpf/CLAUDE.md", "internal/controlplane/firewall/ebpf/cmd/CLAUDE.md", "internal/controlplane/firewall/ebpf/netlogger/CLAUDE.md", "internal/controlplane/infracerts/CLAUDE.md", "internal/controlplane/otelcerts/CLAUDE.md", "internal/controlplane/overseer/CLAUDE.md", "internal/dnsbpf/CLAUDE.md", "internal/docker/CLAUDE.md", "internal/docs/CLAUDE.md", "internal/git/CLAUDE.md", "internal/hostproxy/CLAUDE.md", "internal/hostproxy/internals/CLAUDE.md", "internal/iostreams/CLAUDE.md", "internal/keyring/CLAUDE.md", "internal/logger/CLAUDE.md", "internal/monitor/CLAUDE.md", "internal/project/CLAUDE.md", "internal/prompter/CLAUDE.md", "internal/signals/CLAUDE.md", "internal/socketbridge/CLAUDE.md", "internal/storage/CLAUDE.md", "internal/storeui/CLAUDE.md", "internal/term/CLAUDE.md", "internal/testenv/CLAUDE.md", "internal/text/CLAUDE.md", "internal/tui/CLAUDE.md", "internal/update/CLAUDE.md", "internal/workspace/CLAUDE.md", "pkg/whail/CLAUDE.md", "test/adversarial/CLAUDE.md", "test/CLAUDE.md"], "claude_dir": [".claude/docs/ARCHITECTURE.md", ".claude/docs/DESIGN.md", ".claude/docs/KEY-CONCEPTS.md", ".claude/docs/MONITORING-REFERENCE.md", ".claude/docs/REPO-STRUCTURE.md", ".claude/docs/STOREUI-REFERENCE.md", ".claude/docs/TESTING-REFERENCE.md", ".claude/rules/code-style.md", ".claude/rules/container-commands.md", ".claude/rules/dependency-placement.md", ".claude/rules/docker-client.md", ".claude/rules/envoy.md", ".claude/rules/firewall-uat.md", ".claude/rules/git.md", ".claude/rules/hostproxy.md", ".claude/rules/iostreams.md", ".claude/rules/mintlify-docs.md", ".claude/rules/monitoring.md", ".claude/rules/storage-schema.md", ".claude/rules/storeui.md", ".claude/rules/testing.md", ".claude/rules/tui.md"], "userdocs": ["README.md", "pkg/whail/README.md", "docs/architecture.mdx", "docs/configuration.mdx", "docs/container-internals.mdx", "docs/control-plane.mdx", "docs/credentials.mdx", "docs/custom-images.mdx", "docs/design.mdx", "docs/docker-hygiene.mdx", "docs/firewall.mdx", "docs/index.mdx", "docs/installation.mdx", "docs/monitoring.mdx", "docs/observability.mdx", "docs/quickstart.mdx", "docs/sadboy.md", "docs/security.mdx", "docs/testing.md", "docs/threat-model.mdx", "docs/workflow-history.md", "docs/worktrees.mdx"], "skill": ["claude-plugin/clawker-support/README.md", "claude-plugin/clawker-support/skills/clawker-support/reference/claude-code.md", "claude-plugin/clawker-support/skills/clawker-support/reference/docker-hygiene.md", "claude-plugin/clawker-support/skills/clawker-support/reference/firewall-security.md", "claude-plugin/clawker-support/skills/clawker-support/reference/known-issues.md", "claude-plugin/clawker-support/skills/clawker-support/reference/mcp-recipes.md", "claude-plugin/clawker-support/skills/clawker-support/reference/monitoring.md", "claude-plugin/clawker-support/skills/clawker-support/reference/project-config.md", "claude-plugin/clawker-support/skills/clawker-support/reference/settings.md", "claude-plugin/clawker-support/skills/clawker-support/reference/troubleshooting.md", "claude-plugin/clawker-support/skills/clawker-support/SKILL.md"], "comment_dirs": ["cmd/clawker", "cmd/clawkercp", "cmd/clawkerd", "cmd/coredns-clawker", "cmd/coredns-clawker/plugins/otel", "internal/auth", "internal/build", "internal/bundler", "internal/bundler/registry", "internal/bundler/semver", "internal/clawker", "clawkerd", "clawkerd/embed", "internal/clawkerd", "internal/cmd/bridge", "internal/cmd/container", "internal/cmd/container/attach", "internal/cmd/container/exec", "internal/cmd/container/shared", "internal/cmd/container/start", "internal/cmd/controlplane", "internal/cmd/factory", "internal/cmd/firewall", "internal/cmd/generate", "internal/cmd/hostproxy", "internal/cmd/image", "internal/cmd/init", "internal/cmd/monitor", "internal/cmd/network", "internal/cmd/project", "internal/cmd/root", "internal/cmd/settings", "internal/cmd/skill", "internal/cmd/version", "internal/cmd/volume", "internal/cmd/worktree", "internal/cmdutil", "internal/config", "internal/consts", "internal/containerfs", "internal/controlplane", "internal/controlplane/adminclient", "internal/controlplane/agent", "internal/controlplane/manager", "internal/controlplane/dockerevents", "internal/controlplane/firewall", "internal/controlplane/firewall/ebpf", "internal/controlplane/firewall/ebpf/cmd", "internal/controlplane/firewall/ebpf/netlogger", "internal/controlplane/infracerts", "internal/controlplane/otelcerts", "internal/controlplane/overseer", "internal/dnsbpf", "internal/docker", "internal/docs", "internal/git", "internal/hostproxy", "internal/hostproxy/internals", "internal/iostreams", "internal/keyring", "internal/logger", "internal/monitor", "internal/project", "internal/prompter", "internal/signals", "internal/socketbridge", "internal/storage", "internal/storeui", "internal/term", "internal/testenv", "internal/text", "internal/tui", "internal/update", "internal/workspace", "pkg/whail", "pkg/whail/buildkit"]}
// Invoke by name with args {clusters:[...]} where each entry is one of:
//   skill | userdocs | claude_md | claude_dir | comments
// (claude_md routes through the claude-md-management:claude-md-improver skill, one file per agent.)
const selected = (args && Array.isArray(args.clusters) && args.clusters.length)
  ? args.clusters
  : ["skill", "userdocs", "claude_md", "claude_dir", "comments"] // default: full sweep

const CLUSTERS = {
  skill:      { phase: 'Skill docs',         items: data.skill,        kind: 'skill' },
  userdocs:   { phase: 'User docs',          items: data.userdocs,     kind: 'userdoc' },
  claude_md:  { phase: 'CLAUDE.md',          items: data.claude_md,    kind: 'claudemd' },
  claude_dir: { phase: '.claude docs/rules', items: data.claude_dir,   kind: 'reference' },
  comments:   { phase: 'Code comments',      items: data.comment_dirs, kind: 'comments' },
}

// ---- schemas ----
const FIX_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['target', 'changed', 'change_count', 'changes', 'unsure', 'summary'],
  properties: {
    target: { type: 'string' },
    changed: { type: 'boolean' },
    change_count: { type: 'integer' },
    changes: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['category', 'was', 'now', 'why'],
        properties: {
          category: { type: 'string', description: 'stale|inaccurate|deadref|redundant|optimization|prref|typo' },
          was: { type: 'string', description: 'short quote of old text (<=160 chars)' },
          now: { type: 'string', description: 'short quote of new text (<=160 chars)' },
          why: { type: 'string', description: 'code evidence: symbol/path/fact that justifies the change' },
        },
      },
    },
    unsure: { type: 'array', items: { type: 'string' }, description: 'suspected issues left UNCHANGED for human review' },
    summary: { type: 'string' },
  },
}

const VERIFY_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['target', 'ok', 'problems', 'action_taken'],
  properties: {
    target: { type: 'string' },
    ok: { type: 'boolean', description: 'true if all remaining changes are factually correct vs code' },
    problems: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['issue', 'severity', 'resolution'],
        properties: {
          issue: { type: 'string' },
          severity: { type: 'string', description: 'high|medium|low' },
          resolution: { type: 'string', description: 'reverted|corrected|left-flagged' },
        },
      },
    },
    action_taken: { type: 'string', description: 'none|reverted-hunks|corrected' },
  },
}

const GROUND = `Ground EVERY claim in current code before changing it. Prefer Serena symbolic tools — load them once with ToolSearch (query: "select:mcp__serena__get_symbols_overview,mcp__serena__find_symbol,mcp__serena__find_referencing_symbols") to check that symbol names, signatures, file paths, flags, config keys and described behavior still exist as written. Grep/Read are fine for prose-level facts. This session runs INSIDE a clawker agent container: NEVER run \`go test ./...\` or any broad test command (it tears down the host control plane). You do not need to run code at all — read it.`

const RULES = `Hard rules:
- Conservative. Only change text that is DEMONSTRABLY wrong, stale, dead, or redundant vs the current code. If you cannot prove it from code, leave it and list it under "unsure".
- Do NOT rewrite for style/voice. Do NOT reflow paragraphs. Minimal diffs.
- Do NOT touch generated files. STOP and make ZERO edits to the file if its first ~10 lines contain an "AUTO-GENERATED" / "Do not edit" / "Code generated by" banner (e.g. docs/configuration.mdx is generated from cmd/gen-docs/configuration.mdx.tmpl + internal/config/schema.go struct tags). Also skip: docs/cli-reference/*, *_test golden output, mocks (moq), api/*/v1 protobuf, *.pb.go. If such a file is stale, the fix belongs in its template/source — note that under "unsure" with the template path; do not edit the generated artifact.
- No clawker-own PR/issue/commit references (e.g. "PR #347", "commit abc123"). If found, reword to state the constraint in present tense. KEEP upstream tracker refs (e.g. moby/buildkit#2409, rs/zerolog issue #493) — those are legit.
- VERSION-PIN DRIFT TRAP (high priority — hunt for this): a concrete version number stated inline in prose or a comment that merely DESCRIBES a dependency/tool/image/language whose authoritative pin lives elsewhere (go.mod, the \`go\` directive, Makefile, Dockerfile, *.lock, a *_VERSION const). Examples: "moby/moby/client v0.4.1 API", "Go 1.25", "Envoy 1.31", "OpenSearch 3.6.0", "buildpack-deps:bookworm". The fix is to REMOVE the version (keeping the stable identifier — the import path / tool name / image repo) or point to the source of truth ("see go.mod" / "pinned in the Makefile"). NEVER bump it to today's value — that just resets the drift clock and it rots again next week. Do NOT touch the version numbers in the authoritative pin files themselves (go.mod, Makefile, Dockerfile, *.lock, version consts) — those are the source of truth and must keep their exact pins. Only strip the duplicated/descriptive copies. If a version genuinely carries meaning (e.g. "requires API ≥ X" documenting a real minimum), keep it but flag under "unsure".
- Alpha project (small but growing user base): avoid gratuitous migration notes, version-framing ("v1/v2"), or "breaking change" callouts — add them only when a change genuinely warrants warning real users.
- Fix dead links / renamed paths / removed flags / renamed symbols. Remove duplicated content that another file owns (note where it belongs).
- Preserve markdown/MDX/frontmatter structure exactly.`

function fixPrompt(kind, target) {
  if (kind === 'comments') {
    return `You are auditing CODE COMMENTS for staleness and inaccuracy in the Go files directly inside the directory \`${target}\` (non-recursive: only \`${target}/*.go\`, do not descend into subdirs).

${GROUND}

Task: read each \`${target}/*.go\` file. For every comment (doc comments, inline // comments, package comments), check it against the adjacent code. Fix comments that:
- describe a symbol/behavior that no longer matches the code (renamed, removed, signature changed, logic changed),
- reference a file/package/path that moved or no longer exists,
- contain clawker-own PR/issue/commit refs (reword to present-tense constraint),
- are redundant restatements of the obvious code with no value AND are misleading.
Do NOT delete useful explanatory comments. Do NOT add comments. Do NOT change code. Edit comment text only.

${RULES}

Use Edit/replace_content for the .go files (you have read them, edits are allowed for comment-only changes). Return the structured result. \`target\` = "${target}". List comments you suspect but cannot prove stale under "unsure".`
  }
  if (kind === 'claudemd') {
    return `You are auditing and improving the CLAUDE.md file \`${target}\` — package/project memory read by future coding agents. It must match the current code: type names, function signatures, file paths, package boundaries, design invariants.

REQUIRED METHOD: invoke the \`claude-md-management:claude-md-improver\` skill (via the Skill tool) and apply its audit criteria and quality template to this file. IMPORTANT scoping override: that skill normally scans ALL CLAUDE.md files in the repo — do NOT. A separate agent owns every other CLAUDE.md concurrently. Read and EDIT ONLY \`${target}\`. Editing any other file will corrupt a peer agent's work.

${GROUND}

Apply the improver's guidance to fix what is stale, inaccurate, dead, or redundant vs the current code, and to tighten quality per the template. Keep it grounded — every factual claim must match code.

${RULES}

Use Edit/replace_content on \`${target}\` ONLY. Return the structured result with \`target\` = "${target}". Suspected-but-unproven issues go in "unsure" UNCHANGED.`
  }
  const kindline = {
    skill: 'a clawker-support Agent Skill reference doc (read by Claude when helping users with clawker). It must accurately describe current clawker config schema, CLI commands, firewall/CP behavior, and known issues.',
    userdoc: 'an end-user documentation page (Mintlify .mdx or README). End users rely on it — command names, flags, config YAML keys, and described workflows must match the shipping CLI exactly.',
    reference: 'an internal .claude reference doc (architecture, design, key-concepts, or a coding rule) for future coding agents. It must match the current code: type names, function signatures, file paths, package boundaries, design invariants.',
  }[kind]

  return `You are auditing the documentation file \`${target}\`. It is ${kindline}

${GROUND}

Task: read \`${target}\` in full, then verify its concrete claims against the current code/CLI:
- command names, subcommands, flags, and defaults (cross-check the cobra command definitions and \`docs/cli-reference/\` if relevant),
- config keys and YAML shape (clawker.yaml / settings.yaml schema in internal/config),
- type/function/symbol names and file paths it references,
- described behavior, architecture, invariants, and feature lists,
- internal cross-links to other docs/files (must resolve).
Fix what is stale, inaccurate, dead, or redundant. Remove content that duplicates what another doc authoritatively owns (note where). Keep scope, tone, and structure.

${RULES}

Use Edit/replace_content to apply fixes to \`${target}\`. Return the structured result with \`target\` = "${target}". Anything you suspect is wrong but cannot prove from code goes in "unsure" UNCHANGED.`
}

function verifyPrompt(target, fixResult) {
  return `Adversarial verification of doc/comment edits just made to \`${target}\`.

Run \`git diff -- ${target}\` (for a comment directory target, run \`git diff -- ${target}/*.go\`). For EACH changed hunk, independently re-check the new text against the current code (use Grep/Read/Serena). Your default stance is skeptical: assume the editor may have hallucinated a "fix".

For any change that is now WRONG, makes the doc LESS accurate, damaged structure/markdown, or "fixed" something that was actually correct: revert that specific hunk (re-edit the file back, or correct it to the truth). Leave correct improvements in place.

The editor reported these changes for context (verify them, don't trust them):
${JSON.stringify((fixResult && fixResult.changes) || [], null, 0).slice(0, 4000)}

This is an in-container clawker session: never run \`go test ./...\`. Return the structured verdict with \`target\` = "${target}". action_taken = none|reverted-hunks|corrected.`
}

// ---- run selected clusters as fix -> verify pipelines ----
const all = []
for (const key of selected) {
  const c = CLUSTERS[key]
  if (!c) { log(`unknown cluster: ${key}`); continue }
  log(`${c.phase}: ${c.items.length} targets`)
  const results = await pipeline(
    c.items,
    (target) => agent(fixPrompt(c.kind, target), {
      label: `fix:${target}`, phase: c.phase, schema: FIX_SCHEMA, model: 'sonnet',
    }),
    (fixRes, target) => {
      if (!fixRes) return null
      if (!fixRes.changed || fixRes.change_count === 0) return { target, fix: fixRes, verify: null }
      return agent(verifyPrompt(target, fixRes), {
        label: `verify:${target}`, phase: 'Verify', schema: VERIFY_SCHEMA,
      }).then(v => ({ target, fix: fixRes, verify: v }))
    },
  )
  for (const r of results) {
    if (r) all.push(r)
  }
}

// summarize
const touched = all.filter(r => r.fix && r.fix.changed)
const flagged = all.flatMap(r => (r.fix && r.fix.unsure || []).map(u => ({ target: r.fix.target, unsure: u })))
const reverts = all.filter(r => r.verify && r.verify.action_taken && r.verify.action_taken !== 'none')
return {
  clusters: selected,
  targets_processed: all.length,
  files_changed: touched.length,
  total_changes: touched.reduce((n, r) => n + (r.fix.change_count || 0), 0),
  verify_corrections: reverts.map(r => ({ target: r.target, action: r.verify.action_taken, problems: r.verify.problems })),
  unsure: flagged,
  changed_targets: touched.map(r => ({ target: r.fix.target, count: r.fix.change_count, summary: r.fix.summary })),
}
