# README Design Direction

## Overview
The README was overhauled in January 2025 to follow a story-driven structure that hooks readers with the problem, shows immediate value, and progressively reveals depth.

## Design Principles

1. **Problem-first intro** - Lead with pain points (YOLO mode dangers, Docker tedium, broken OAuth, missing git creds) before the solution
2. **Progressive disclosure** - Quick Start first, details later
3. **Practical over technical** - Workflows section shows real usage patterns
4. **Visual architecture** - ASCII diagrams for auth flow and system overview
5. **whail at the end** - Technical foundation explained after users understand the value

## Structure

1. Header + badges (Go, License, macOS)
2. Problem statement (conversational, polished)
3. Quick Start (minimal steps to running)
4. Seamless Authentication & Git (host proxy explained with flow diagram)
5. Workflows (containers, Claude options, detach/reattach, management)
6. Monitoring (brief, optional)
7. System Overview (ASCII architecture diagram)
8. Configuration (full clawker.yaml example)
9. Security Defaults (compact table)
10. The whail Engine (label isolation explanation with docker vs clawker example)
11. Known Issues (Claude TUI redraw)
12. Contributing + License

## Key Decisions

- **No logo yet** - Text-only header with shields.io badges
- **GitHub URL**: `schmitthub/clawker`
- **Tone**: Fun intro with problem statement, then practical workflows
- **Auth flow diagram**: Simple ASCII showing Container → Host Proxy → Browser flow
- **System diagram**: Shows all components (CLI, Host Proxy, Container scripts, Monitoring, Docker)
- **whail featured prominently** - Moved to dedicated section at end as reusable foundation

## Commit
`1e5d99c` on branch `a/readme-update` - "Overhaul README with story-driven structure and system diagram"
