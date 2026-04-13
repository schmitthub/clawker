# Control Plane Initiative — Current Status

## Status: Initiative spec approved, pending `/creview`

**Workflow phase**: review (next: run `/creview` on the initiative spec)
**Branch**: `feat/control-plane` (Branch 1 work — CP primitive)
**Spec**: `.correctless/specs/control-plane-initiative.md`
**Context doc**: `.correctless/specs/cp-initiative/CONTEXT.md` (full decision record for fresh sessions)

## Branch Sequence
1. CP as proper service (current branch) — auth + gRPC, firewall still owns bootstrap
2. Ownership reversal — CP owns firewall, Manager becomes thin client, daemon sunset
3. Daemon consolidation — hostproxy + socketbridge under CP, Docker events
4. clawkerd auth — PKCE registration, per-agent certs
5. Init migration + agent lifecycle — clawkerd replaces init scripts, command channel
6. Monitor + release + hardening — out of alpha

Each branch gets its own `/cspec` kickoff before implementation.

## Key Process Notes
- Highway construction: old stays live until replacement proven
- Living roadmap: branch details decided at kickoff, not upfront
- No backward compat needed: eBPF never shipped in a release
- Alpha project: larger branches OK, no official releases during work
- HIGH intensity: security tool, trust boundaries, auth throughout
