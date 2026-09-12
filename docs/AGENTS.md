# Mintlify Documentation Site (docs.clawker.dev)

## File Conventions

- `docs/docs.json` — Mintlify config (theme, nav, colors, integrations). **Not** `mint.json` (legacy name)
- `docs/index.mdx` — Homepage. `mode: frame` landing page in the Product Guide template layout (hero, feature card, product cards, common tasks grid) with the prose sections below in `.product-guide-prose`
- `docs/*.mdx` — Hand-authored pages (quickstart, installation, etc.). Exception: `docs/configuration.mdx` is **auto-generated** (from `cmd/gen-docs/configuration.mdx.tmpl` + schema struct tags — never edit directly)
- `docs/cli-reference/*.md` — Auto-generated CLI reference (**never edit directly**). Generated via Makefile, checked in, freshness verified in CI (covers `docs/cli-reference/` and `docs/configuration.mdx`)
- `docs/architecture.mdx`, `docs/design.mdx`, `docs/testing.md` — Developer docs with Mintlify frontmatter
- `docs/custom.css` — Mintlify Product Guide template `style.css` (sidebar anchor styling, frame-mode landing page, feature card) with amber palette
- `docs/favicon.svg` — `>_` terminal prompt icon (amber `#f59e0b` on dark `#09090b`)
- `docs/assets/` — Image assets directory

### Extensions

- Hand-authored pages: `.mdx`
- Auto-generated CLI reference: `.md`
- Frontmatter required on all pages (`title:` minimum)

### Regenerating CLI Reference

```bash
go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas
```

Source: `internal/docs/markdown.go` (`GenMarkdownTreeWebsite`, `EscapeMDXProse`) + `cmd/gen-docs/main.go` (`--website` flag)

## MDX Parsing

Mintlify parses **all** `.md`/`.mdx` files as MDX — there is no per-file way to disable this. Bare `<word>` angle brackets cause JSX parse errors. The `EscapeMDXProse()` function escapes `<word>` → `` `<word>` `` in prose while leaving fenced code blocks untouched.

## Theming

- Layout: Mintlify **Product Guide** template (`mintlify/templates/product-guide`): theme `almond`, lucide icons, global sidebar anchors (Home, GitHub, Releases), `navigation.directory: card`, groups with icons and `expanded: true` (except CLI Reference), navbar links empty with GitHub star-count button (`navbar.primary` must be an external URL; it opens a new tab)
- Palette (canonical, do not change): amber (`#f59e0b` primary, `#fbbf24` light, `#d97706` dark); background `#09090b` with grid decoration; dark-only (`appearance.strict: true`)
- Fonts: theme default (no `fonts` key)
- Feature card on the homepage uses an amber gradient instead of the template's leaf images
- Icons: `icons.library` is `lucide`; use lucide names in `icon=` props (`settings`, `zap`, `layers`, `boxes`, `box`, `package`, `shield`, `key`, `terminal`)

## Navigation Structure

Sidebar groups: Get started, Running Agents, Security, Building images, Extensions, Operations, Under the hood, Developer guide, CLI Reference (collapsible sub-groups per command family). Only top-level groups carry icons; pages never set `icon:` in frontmatter. A group's overview page uses `sidebarTitle: "Overview"` so the group name is not repeated (see `index.mdx`, `security.mdx`).
Navbar: GitHub star-count button only. Sidebar global anchors: Home, GitHub, Releases.

## Architecture

- Generation: `--website` flag on `cmd/gen-docs` produces MDX-safe output with Mintlify frontmatter
- Deployment: Mintlify-hosted, GitHub App auto-deploy on push
- Custom domain: `docs.clawker.dev` via Cloudflare CNAME → `cname.vercel-dns.com`
- Local preview: `npx mintlify dev --docs-directory docs` (requires Node.js)
- deepwiki MCP (`mintlify/docs` repo) is the go-to for Mintlify questions
