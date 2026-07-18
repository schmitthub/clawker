# Agent Sandbox Comparison — Research Plan (rev 5, 2026-07-18)

Goal: marketing comparison for README + splash site (clawker.dev, separate repo). Clawker first column/row. Honest, cited, assessment-not-scoring. Criteria doc: `agent-sandbox-research/assessment-guidelines` (canonical — verdict+justification+source; capability=Yes/No/Partial/Unknown/NA, spectrum=qualitative opinion, prose axes for identity/pricing).

## Execution decisions (LOCKED)
- Single pass. One Opus agent per candidate (`model: 'opus'`). No verify wave — sources enable later verification at condensation.
- Agents read `assessment-guidelines` memory, research via WebSearch/WebFetch (firewall bypass must be active), write prose writeup to `agent-sandbox-research/providers/<slug>`.
- NO launch without explicit user "go".
- Condensation (maintainers): verdict→cell mapping (yes→✅, partial→⚠️, no→❌, unknown→?, na→—; ✅ downgradeable to ⚠️ on significant caveats), footnote copy, README table + compact splash variant.

## Report structure — tech-first
Six RAW TECH tiers, each gets overview section: how complicated used raw, positives/negatives, isolation properties ("VMs offer full kernel isolation but ..."). Then candidates presented with `built_on` mapping to a tier (chains allowed: Modal → gVisor → containers). No intermediate taxonomy layers — a thing is either raw tech used directly or a product built on tech.

Tiers:
1. Bare host (baseline, no isolation)
2. OS-level sandboxing (namespaces/seccomp/Landlock/seatbelt)
3. Containers (shared kernel, runc-class)
4. MicroVMs (hardware virt, minimal device model, ms boot — distinct from full VMs: ergonomics/perf/density)
5. Full VMs (hardware virt, full guest OS)
6. Remote VPS / separate machine

## Candidates
Raw-tech-direct candidates (assessed as "what competent dev gets out of the box"; glue-only capability = No):
- Bare host baseline
- OS-level DIY (bubblewrap/nsjail/firejail/systemd-nspawn)
- Plain Docker container DIY
- Raw gVisor (runsc as Docker/containerd runtime; Linux-only)
- Raw Kata Containers (containerd runtime)
- DIY Firecracker (raw API server; realistically SDK/wrapper territory — assess honestly)
- Local full VM DIY (Vagrant/Lima/UTM/VirtualBox/QEMU)
- Remote VPS DIY (Hetzner/EC2/DO box + ssh/tmux)

Products (built_on provisional where marked ¹ — confirming is part of research):
- clawker (containers) — assessed by maintainers from repo knowledge, NOT by subagent
- Docker Sandboxes (containers)
- Dagger container-use (containers)
- devcontainers (containers; baseline product)
- Cloudflare Sandbox SDK (containers¹)
- Daytona (containers¹)
- Modal Sandboxes (gVisor → containers)
- K8s agent-sandbox SIG (orchestration over containers, runtime-pluggable)
- Anthropic sandbox-runtime srt (OS-level)
- OpenAI Codex CLI sandbox (OS-level: seatbelt/Landlock)
- Claude Code sandbox-environments (OS-level¹)
- E2B (microVM: Firecracker)
- Microsandbox (microVM: libkrun)
- Vercel Sandbox (microVM¹)
- Morph (microVM¹)
- SmolVM (microVM¹; identity itself needs confirming)
- OpenSandbox (unknown¹; identity needs confirming)
- Runloop (VPS/cloud machines¹)
- OpenAI API sandboxes (cloud¹)
- Discovered 2026-07-18 (Sonnet sweep, user approved six): Northflank Sandboxes (microVM: Kata/gVisor¹), Blaxel (microVM¹), CodeSandbox SDK / Together Code Sandbox (microVM: Firecracker), Sculptor by Imbue (containers, local), agent-sandbox [agent-sandbox/agent-sandbox org — disambiguate from kubernetes-sigs] (K8s/containers, E2B-API-compatible), Beam/beta9 (containers). Rejected: Superset (worktree-only, no isolation), OpenHands (agent platform, not sandbox), WebContainers/WASM tier (scope).

## Fan-out scope (rev 6, 2026-07-18)
- DEEP RESEARCH = PROVIDERS ONLY. Raw-tech-direct candidates get NO research agents — tier overviews + raw-usage assessment written by maintainers at condensation ("additive later").
- Researchers: OFFICIAL DOCS ALWAYS; deepwiki BANNED (devin hallucinates). In guidelines.
- TRIAL-FIRST PROTOCOL: one trial agent on a single provider (E2B) → user reviews writeup and signs off → only then full fleet launch. Nothing autonomous.

~25 provider fan-out targets (clawker excluded — maintainer-assessed).

## History
- rev 1-2: initial 25-dim rubric, two workflows launched prematurely (wf_fa7333ae named providers, wf_8a252c15 generic tech) — STOPPED by user before completion; partial cached results in run journals, resumable but likely superseded (prompts changed completely since).
- rev 3: no scoring → booleans+caveats. rev 4: assessment framing, verdict+justification+ref, partial re-allowed, spectrum criteria added.
- rev 5: tech-first report structure, comingled raw-tech + product candidates, no sub-taxonomy (gVisor = candidate usable raw AND foundation others build on).

Seed URLs: `agent-sandbox-research/sources`.
