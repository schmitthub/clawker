# Image Build Output Redesign

## Context
Iterative bug-fixing on the TTY progress display for `clawker image build` revealed that the current sliding-window design for step visibility is fundamentally mismatched with how BuildKit delivers progress events. Rather than continuing to patch individual symptoms, a redesign is warranted.

## Branch
`a/presentation-integration`

## Problems Fixed (completed patches, may be superseded by redesign)

1. **Duplicate collapsed group headings** — Interleaved build stages created duplicate collapsed entries. Added `mergeCollapsed()` helper that scans by name instead of consecutive-only.
2. **Viewport border collapse from `\r`** — Build tools emit CR-based progress bars. Added `\r` stripping in `drainProgress()` (pkg/whail/buildkit/progress.go).
3. **Active group falsely collapsed** — Hidden complete steps from a still-active group showed "✓ group ── N steps". Added `activeGroups` set to suppress.
4. **Running step hidden by tail window** — BuildKit registers all vertices upfront (e.g., 29 steps). Tail window showed pending step 29/29 while step 2/29 was running. Added re-anchor at first hidden running step + maxVisible cap.

## Root Issue: Design Doesn't Match Reality

The current `visibleProgressSteps()` design assumes steps arrive roughly in execution order and the "interesting" steps are at the tail. But BuildKit:
- Registers ALL vertices upfront (29 pending steps appear before any start running)
- Interleaves stages (stage-2 → builder-a → stage-2 → builder-a)
- Sends CR-based progress bars in log data
- Can have wide gaps between the running step and the pending tail

Each patch adds complexity (group boundary snapping, running-step re-anchoring, active-group suppression, visible cap) to a tail-window model that doesn't fit. The function is now ~70 lines of edge-case handling.

## Redesign Complete

The sliding-window approach (`visibleProgressSteps`, `collapseCompleteGroups`, `mergeCollapsed`, `renderStepSection`, `renderProgressViewport`) has been fully replaced with a tree-based display.

### New Architecture
- **`buildStageTree()`** — O(n) pass groups steps by `ParseGroup` callback into `stageNode` structs
- **`stageNode.stageState()`** — Aggregate: Error > Running > Complete > Pending
- **`renderTreeSection()`** — Orchestrates: collapsed stages, expanded active stage with tree connectors, per-stage child window
- **`renderStageChildren()`** — Tree connectors (`├─`/`└─`), centered MaxVisible window, inline log lines (`⎿`)
- **Per-step `logBuf`** — Log lines routed to per-step ring buffers instead of global viewport

### Deleted
- `visibleProgressSteps()`, `collapseCompleteGroups()`, `mergeCollapsed()`, `collapsedGroup` type
- `renderProgressViewport()`, `renderStepSection()`
- Global `logBuf` field on `progressModel`
- 13 `TestVisibleSteps_*` tests, `TestProgressModel_View_SlidingWindow`, `TestProgressModel_View_Viewport`, `TestProgressModel_View_GroupHeadings`

### Added
- `stageNode`, `stageTree`, `buildStageTree()`, tree connector constants
- `renderCollapsedStage()`, `renderTreeStepLine()`, `renderTreeLogLines()`, `renderStageChildren()`, `renderStageNode()`, `renderTreeSection()`
- `stepLineParts()` — extracted from `renderProgressStepLine()` for shared use
- 27 new tests covering tree types, rendering, and model integration

## Status: DONE
This memory can be deleted if no longer needed.
