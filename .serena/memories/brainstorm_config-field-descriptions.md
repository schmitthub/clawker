# Brainstorm: Deterministic Field Descriptions for Config/Settings Schema

> **Status:** Completed — All tasks implemented
> **Created:** 2026-03-21
> **Last Updated:** 2026-03-21 01:00
> **Issue:** #178
> **Implementation:** Serena memory `initiative_storage-schema-contract`

## Problem
Config field descriptions scattered across 4+ locations. Need single source of truth consumable by TUI, docs, CLI introspection, validation, LLM context.

## Final Design

**Storage is the authority.** It defines the contract as interfaces — `Field`, `FieldSet`, `Schema`. Concrete implementations are unexported. `NormalizeFields[T]()` reads struct tags (`yaml`, `desc`, `label`) and produces `FieldSet`. Domain types implement `Schema` by calling the normalizer. Consumers program against interfaces.

**Key decisions:**
- Interfaces all the way down: `Field`, `FieldSet`, `Schema`
- Storage owns the contract types + normalizer (toolkit, not registry)
- Struct tags are the source of truth (co-located with fields)
- `Store[T any]` → `Store[T Schema]` — compile-time enforcement
- storeui becomes a consumer/plugin — Override keeps only TUI concerns (Hidden, Order, ReadOnly, Kind, Options)
- `FieldSet` is queryable: `All()`, `Get(path)`, `Group(prefix)` — callers don't loop+string-match

**Rejected approaches:**
- Struct tags only (no interface) — no contract enforcement
- Central registry — drift risk, storage shouldn't hold data
- Code generation — overkill for this use case
- gh-style `[]ConfigOption` — too flat, no type safety
- kubectl OpenAPI — too heavy
