# PRD: clawker.dev Splash Site

> **Status:** Ready for implementation
> **Created:** 2026-02-17
> **Target repo:** schmitthub/clawker.dev (separate from main clawker repo)

## Overview

A standalone marketing/landing page for the clawker CLI tool, hosted at `clawker.dev` (and `www.clawker.dev`). This is a hand-coded, visually polished splash site — not a docs site. Documentation lives separately at `docs.clawker.dev` (powered by Mintlify, managed in the main clawker repo).

## What is Clawker?

Clawker is a CLI tool for Claude container orchestration — it builds Docker images, manages containers, and orchestrates Claude Code agent environments. Think of it as a developer tool that wraps Docker with opinionated defaults for running AI coding agents in containers. It's a Go CLI distributed via Homebrew and a `curl | bash` installer.

- **GitHub:** github.com/schmitthub/clawker
- **Domain:** clawker.dev (Cloudflare-managed)
- **Docs (separate):** docs.clawker.dev (Mintlify)

## Architecture Decisions

### Framework: Astro
- **Astro** static site generator (not Next.js, not Starlight)
- Zero client-side JavaScript by default — opt-in interactivity via islands if needed
- Hand-coded pages using `.astro` files, custom CSS, full design freedom
- No template or theme lock-in — this is a from-scratch build

### Hosting: Cloudflare Pages + Workers
- Static site deployed to **Cloudflare Pages**
- **CF Worker** route for `/install.sh` — dynamic install script endpoint (see below)
- Domain `clawker.dev` is already registered on Cloudflare
- DNS managed in Cloudflare dashboard

### Reference Implementation
OpenClaw's splash site (`openclaw.ai` / repo: `openclaw/openclaw.ai`) uses the exact same pattern:
- Astro framework
- Custom CSS with CSS custom properties (design system)
- Custom fonts via CDN
- Lucide icons + simple-icons
- Install scripts served from `public/` directory
- Separate repo from the main tool

Study their repo structure for inspiration: `src/pages/`, `src/components/`, `src/layouts/`, `src/data/`, `public/`.

## Requirements

### Pages & Content

**Homepage (`/`)**
- Hero section with tagline and value proposition for clawker
- Terminal demo or animation showing clawker in action (e.g. `clawker run @` workflow)
- Key features/benefits grid
- Installation instructions (Homebrew + curl | bash)
- Link to docs (`docs.clawker.dev`)
- Link to GitHub repo
- Footer with links

**Install Script (`/install.sh`)**
- Dynamic endpoint served by a Cloudflare Worker (not a static file)
- Detects platform/architecture from request headers
- Resolves latest version from GitHub releases API (cached)
- Returns a shell script tailored to the user's platform
- Analytics: track install counts, OS distribution
- Fallback: if GitHub API is down, serve a static fallback script
- Must work with: `curl -fsSL https://clawker.dev/install.sh | bash`
- Reference: the existing `scripts/install.sh` in the main clawker repo

### Design
- Fully hand-coded — no UI framework, no component library
- Custom CSS with CSS custom properties for a design system
- Dark theme primary (CLI tool aesthetic)
- Responsive (desktop + mobile)
- Fast — minimal/zero client JS, leverage Astro's static output
- Custom fonts welcome (see OpenClaw's use of Clash Display + Satoshi via Fontshare)

### Deployment
- **Cloudflare Pages** for the static site
  - Auto-deploy on push to `main` branch
  - Build command: `npm run build` (or `bun run build`)
  - Output directory: `dist/`
- **Cloudflare Worker** for `/install.sh` route
  - Can be a separate Worker or integrated via `@astrojs/cloudflare` adapter with server endpoints
- DNS: `clawker.dev` and `www.clawker.dev` → CF Pages

### SEO & Meta
- Proper Open Graph tags, Twitter cards
- `robots.txt` and `sitemap.xml`
- Canonical URL handling for www vs non-www
- Fast Core Web Vitals (static Astro makes this easy)

## Non-Goals
- This site does NOT serve documentation — that's `docs.clawker.dev` (Mintlify)
- No blog (for now)
- No user accounts, dashboards, or dynamic app features
- No SSR beyond the install.sh Worker endpoint

## Repo Structure (Suggested)

```
clawker.dev/
├── src/
│   ├── pages/          # Astro pages (index.astro, etc.)
│   ├── components/     # Reusable .astro components
│   ├── layouts/        # Page layout wrappers
│   ├── styles/         # Global CSS, design tokens
│   └── data/           # JSON data files (features, testimonials, etc.)
├── public/             # Static assets (images, favicons)
├── workers/            # CF Worker for /install.sh (if separate from Astro)
├── astro.config.mjs
├── package.json
└── wrangler.toml       # CF Workers config (if using wrangler)
```

## Integration Points
- **GitHub Releases API:** Worker fetches latest clawker release version for install script
- **docs.clawker.dev:** Link to documentation (no technical integration, just hyperlinks)
- **GitHub repo:** Link to source code
- **Homebrew:** Reference `brew install schmitthub/tap/clawker` in install instructions

## Open Questions (for splash site agent to explore)
- Exact visual design direction — terminal-aesthetic dark theme? Minimal clean? Colorful?
- Whether to use Astro's CF adapter for the Worker or keep it as a separate Wrangler-managed Worker
- Analytics: Cloudflare Web Analytics (free, privacy-friendly) vs something else
- Whether to add a blog later (would affect initial architecture choices)
