---
description: Mintlify documentation site conventions
paths: ["docs/**"]
---

# Mintlify Documentation Site (docs.clawker.dev)

## File Conventions

- `docs/docs.json` — Mintlify config (theme, nav, colors, integrations). **Not** `mint.json` (legacy name)
- `docs/index.mdx` — Homepage
- `docs/*.mdx` — Hand-authored pages (quickstart, installation, configuration)
- `docs/cli-reference/*.md` — Auto-generated CLI reference (82 files, **never edit directly**). Generated via Makefile, checked in, freshness verified separately in CI
- `docs/architecture.md`, `docs/design.md`, `docs/testing.md` — Developer docs with Mintlify frontmatter
- `docs/custom.css` — Dark terminal theme overrides (surface colors, glassmorphism navbar, amber hover glow)
- `docs/favicon.svg` — `>_` terminal prompt icon (amber `#f59e0b` on dark `#09090b`)
- `docs/assets/` — Image assets directory

### Extensions

- Hand-authored pages: `.mdx`
- Auto-generated CLI reference: `.md`
- Frontmatter required on all pages (`title:` minimum)

### Regenerating CLI Reference

```bash
go run ./cmd/gen-docs --doc-path docs --markdown --website
```

Source: `internal/docs/markdown.go` (`GenMarkdownTreeWebsite`, `EscapeMDXProse`) + `cmd/gen-docs/main.go` (`--website` flag)

## MDX Parsing

Mintlify parses **all** `.md`/`.mdx` files as MDX — there is no per-file way to disable this. Bare `<word>` angle brackets cause JSX parse errors. The `EscapeMDXProse()` function escapes `<word>` → `` `<word>` `` in prose while leaving fenced code blocks untouched.

## Theming

- Theme: `maple`, dark-only (`appearance.strict: true`)
- Palette: amber (`#f59e0b` primary, `#fbbf24` light, `#d97706` dark)
- Background: `#09090b` with grid decoration
- Fonts: Fontshare CDN — Clash Display (headings, weight 600) + Satoshi (body, weight 400)
- `custom.css` surfaces: `#111113` (sidebar, cards, footer), `#1a1a1f` (code blocks)
- Glassmorphism navbar: `rgba(9,9,11,0.85)` with `backdrop-filter: blur(12px)`
- Card hover: amber border glow (`rgba(245,158,11,0.4)`)
- Code font: SF Mono → Cascadia Code → Fira Code → JetBrains Mono → monospace

## Navigation Structure

Sidebar groups: Getting Started, Guides, CLI Reference (10 collapsible sub-groups), Contributing.
Navbar: Home → clawker.dev, GitHub link, Install button → /installation.

## Architecture

- Generation: `--website` flag on `cmd/gen-docs` produces MDX-safe output with Mintlify frontmatter
- Deployment: Mintlify-hosted, GitHub App auto-deploy on push
- Custom domain: `docs.clawker.dev` via Cloudflare CNAME → `cname.vercel-dns.com`
- Local preview: `npx mintlify dev --docs-directory docs` (requires Node.js)
- deepwiki MCP (`mintlify/docs` repo) is the go-to for Mintlify questions
