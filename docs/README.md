# Clawker Developer Documentation

This directory contains documentation for contributors and developers working on Clawker.

## Contents

| Document | Description |
|----------|-------------|
| [Architecture](architecture.md) | System layers, package DAG, key abstractions, dependency injection patterns |
| [Design](design.md) | Design philosophy, security model, core concepts, decision rationale |
| [Testing](testing.md) | Test strategy, how to run tests, golden files, writing new tests |
| [CLI Reference](cli-reference/) | Auto-generated command documentation (all flags, examples, usage) |

## Quick Links

- [CONTRIBUTING.md](../CONTRIBUTING.md) — How to contribute (build, test, PR process)
- [CLAUDE.md](../CLAUDE.md) — AI assistant instructions and project conventions
- [SECURITY.md](../SECURITY.md) — Vulnerability reporting policy

## Regenerating CLI Reference

The CLI reference docs are auto-generated from Cobra command definitions:

```bash
go run ./cmd/gen-docs --doc-path docs --markdown
```

This outputs to `docs/cli-reference/`. Run after adding or modifying CLI commands.
