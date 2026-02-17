# Brainstorm: clawker.dev Website Setup

> **Status:** Active
> **Created:** 2026-02-16
> **Last Updated:** 2026-02-16 00:08

## Problem / Topic
Setting up web presence for clawker.dev using Cloudflare domain. Need a fancy splash/landing page at `clawker.dev` / `www.clawker.dev` and a markdown-based documentation site at `docs.clawker.dev`. Exploring hosting options (CF Pages/Workers, GitHub Pages) and static site generators for both.

## Open Items / Questions
- Repo directory structure (where does Next.js app + Mintlify docs source live?)
- install.sh Worker: separate Worker or integrated into Next.js edge function?
- CI/CD: auto-deploy splash on push to CF Pages? Mintlify auto-deploys from GitHub?
- Splash page content/design: what sections, what vibe?
- Mintlify OSS program application — does clawker qualify?


## Decisions Made
- **Subdomain split for independent hosting**: `clawker.dev` → CF Pages (Astro), `docs.clawker.dev` → Mintlify. Cloudflare DNS routes to different providers.
- **Splash page: Astro on CF Pages**: Hand-coded, full design freedom. Separate repo (`clawker.dev` or similar). Own deps, own CI.
- **Docs platform: Mintlify**: Auto llms.txt, copy-as-markdown per page, free OSS tier. CNAME from docs.clawker.dev → Mintlify.
- **Docs source in main clawker repo**: `docs/` directory. Mintlify reads from here. gen-docs output feeds in. Atomic PRs for CLI + docs changes.
- **Splash page in separate repo**: Keeps Node/Astro deps out of the Go project. Low-churn site, minimal overhead.
- **LLM-friendly pattern**: Every page dual-format (HTML + .md), llms.txt manifest, no "LLM" nav section. Same as code.claude.com.
- **Docs content = mix**: Auto-gen CLI ref + fresh quickstart/guides + adapted .claude/docs content.
- **CF Worker for /install.sh**: Dynamic install script on clawker.dev.


## Research Findings
- **Astro**: Zero client JS by default, first-party CF Pages adapter, can use React/Svelte/Vue components via islands
- **Next.js**: Static export mode works on any host. React-based, heavier output but mature ecosystem
- **MkDocs Material**: Python-based, gold standard for CLI tool docs (uv, ruff, FastAPI). Great search, admonitions, code blocks
- **VitePress**: Node/Vue, fast, clean. Used by Vue/Vite ecosystem
- **Docusaurus**: React, built-in versioning/i18n. Heavier. Custom landing page support built-in
- **Starlight**: Astro integration, can do custom splash + docs in one site. Newer but growing
- **Hugo**: Go-native, fastest builds, but theming is rough
- **uv's approach**: MkDocs in same repo, deploys to separate docs repo, CF Pages hosts it
- **CF Pages supports monorepo**: Multiple projects can point to different root directories in same repo


## Conclusions / Insights
- **Astro > Next.js for static splash pages**: Zero JS by default, smaller bundles, first-party CF Pages adapter. But user wants full hand-coded control — framework choice is secondary to DX preference.
- **Starlight can do BOTH**: But user explicitly wants separate sites for separate experiences.
- **MkDocs Material is the CLI tool docs gold standard**: Used by uv, ruff, FastAPI. Has llms.txt plugin ecosystem.
- **LLM-friendliness strongly favors MkDocs**: mkdocs-llmstxt and mkdocs-llmstxt-md plugins exist. No equivalent in VitePress/Hugo/Docusaurus ecosystem.
- **Three-layer docs vision**: (1) Human HTML docs, (2) LLM prompt pages — purpose-written markdown for agents (like external CLAUDE.md), (3) llms.txt index manifest. This is novel and forward-thinking.
- **CLAUDE.md is the pattern**: The project already has LLM-optimized docs internally (.claude/, CLAUDE.md). The docs site extends this pattern to external users' agents.
- **The subdomain split is increasingly rare in OSS**: But justified here by the fundamentally different experiences (visual splash vs docs+LLM content).


## Gotchas / Risks
- Mintlify is a hosted service — if they go down or shut down, docs go down. But content (MDX source) lives in your repo so migration is straightforward.
- Mintlify OSS program requires application/approval — need to confirm clawker qualifies.
- MDX source files in repo are Mintlify-specific (components like Tabs, Accordion). If migrating away, need to strip MDX components or convert to pure markdown.


## Unknowns
- (none yet)

## Next Steps
- Research how popular OSS CLI tools handle landing + docs split
