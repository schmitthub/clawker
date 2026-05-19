# Audit gotcha: `.md` files that contain only `@<path>.html`

`.serena/memories/brainstorm_*.md` files whose entire content is a single line of the form `@.serena/memories/<name>.html` are **pointers to HTML-exported brainstorms** (Excalidraw / web export), not broken placeholders.

The HTML sibling carries the actual brainstorm; the `.md` exists so Serena's memory index surfaces the name. Both files are checked into git.

Do NOT delete these during memory audits. Verify sibling exists with `ls .serena/memories/<basename>.*`.

Known instances:
- `brainstorm_otel-network-monitoring-ebpf.md` → `.html` (active eBPF network-monitoring design, not yet shipped as of 2026-05-19)
