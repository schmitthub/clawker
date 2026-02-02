---
paths:
  - "internal/**"
  - "cmd/**"
---

# Dependency Placement Decision Tree

When adding a new heavy dependency or command helper, use this decision tree:

```
"Where does my heavy dependency go?"
              │
              ▼
Can it be constructed at startup,
before any command runs?
              │
       ┌──────┴──────┐
       YES            NO (needs CLI args, runtime context)
       │              │
       ▼              ▼
  3+ commands?    Lives in: internal/<package>/
       │          Constructed in: run function
  ┌────┴────┐     Tested via: inject mock on Options
  YES       NO
  │         │
  ▼         ▼
FACTORY   OPTIONS STRUCT
FIELD     (command imports package directly)
```

## Rules

- Implementation always lives in `internal/<package>/` — never in `cmdutil/`
- The only question is **who constructs it**: `factory.New()` at startup, or each command's run function
- `cmdutil/` contains only: Factory struct (DI container), output utilities, arg validators
- Heavy command helpers (resolution, building, registration) live in their own `internal/` packages

## Current Package Layout

| Package | Contains |
|---------|----------|
| `internal/cmdutil/` | Factory struct, output utilities, arg validators (lightweight, no docker import) |
| `internal/build/` | Dockerfile generation, flavor selection, content hashing, version management (leaf — no docker import) |
| `internal/project/` | Project registration in user registry |
| `internal/docker/` | Container naming, image resolution, image building (`Builder`, `BuildDefaultImage`), Docker middleware |
