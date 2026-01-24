# Ralph TUI Dashboard - Product Requirements Document

## Overview

### Product Name
Ralph TUI Dashboard

### Purpose
Provide a rich terminal-based user interface for monitoring and controlling ralph autonomous loops, enabling users to observe multiple agents simultaneously, stream logs in real-time, and perform basic control operations without leaving the terminal.

### Target Users
- Developers using clawker to run Claude Code agents
- Users running multiple ralph agents in parallel
- Teams that need visibility into autonomous loop execution

---

## Problem Statement

Currently, ralph provides only text-based output via `--monitor` flag, which:
1. Only shows one agent at a time
2. Requires scrolling through text output to understand state
3. Offers no way to switch between agents without stopping
4. Provides no centralized view of all running agents
5. Cannot be launched independently to observe running agents

Users need a dashboard that provides:
- Multi-agent visibility at a glance
- Real-time log streaming without stopping agents
- Ability to monitor agents started by other processes
- Basic control operations (stop, reset circuit)

---

## Goals & Success Metrics

### Primary Goals
1. **Visibility**: Users can see all ralph agents for their project in one view
2. **Real-time monitoring**: Users can stream logs from any agent without affecting it
3. **Operational control**: Users can perform basic actions (stop, reset) from the TUI
4. **Independence**: TUI works standalone, not tied to ralph run lifecycle

### Success Metrics
- User can discover all running ralph agents within 2 seconds
- Log streaming latency < 2 seconds
- TUI renders correctly on terminals 80x24 and larger
- 100% of existing ralph functionality remains working

---

## Scope

### In Scope (MVP)
- `clawker ralph tui` standalone command
- `clawker ralph run --tui` integrated mode
- Multi-agent list view with session state
- Agent detail view with full session/circuit info
- Real-time log streaming (read-only)
- Basic actions: stop agent, reset circuit
- Keyboard navigation
- Terminal resize handling
- Project-scoped agent discovery

### Out of Scope (Future)
- Interactive terminal attach (typing to agent)
- Multi-project view
- Log search/filtering
- Live charts/graphs
- Prometheus metrics integration
- Batch operations (stop all, reset all)
- Configuration persistence
- Custom color themes

---

## User Stories

### US1: Monitor Running Agents
**As a** developer running multiple ralph agents
**I want to** see all agents and their status in one view
**So that** I can quickly understand what's happening across my project

**Acceptance Criteria:**
- Agent list shows: name, state (running/stopped), loop count, task count
- Circuit breaker status visible (progress toward trip threshold)
- Auto-refreshes every 2 seconds
- Shows agents from current project only

### US2: View Agent Details
**As a** developer troubleshooting an agent
**I want to** see detailed session and circuit information
**So that** I can understand why an agent is behaving a certain way

**Acceptance Criteria:**
- Shows full session data: started at, loops completed, total tasks, total files
- Shows circuit state: no progress count, tripped status, trip reason
- Shows last error if any
- Can navigate back to list view

### US3: Stream Logs
**As a** developer debugging an agent
**I want to** see real-time log output
**So that** I can follow what Claude is doing

**Acceptance Criteria:**
- Logs stream in real-time (< 2 second latency)
- Last 1000 lines kept in buffer
- Can scroll up/down through history
- View is read-only (no interaction with agent)
- Can return to main view without stopping stream

### US4: Reset Circuit Breaker
**As a** developer with a tripped circuit
**I want to** reset the circuit from the TUI
**So that** I can retry without leaving the dashboard

**Acceptance Criteria:**
- Confirmation prompt before reset
- Shows success/failure feedback
- Agent list updates to reflect new state

### US5: Stop Agent
**As a** developer wanting to stop an agent
**I want to** stop it from the TUI
**So that** I don't need to switch to another terminal

**Acceptance Criteria:**
- Confirmation prompt before stop
- Container stops gracefully
- Agent list updates to show stopped state

### US6: Standalone Monitoring
**As a** developer with agents started by scripts
**I want to** launch the TUI independently
**So that** I can monitor agents I didn't start from this terminal

**Acceptance Criteria:**
- `clawker ralph tui` works without running ralph first
- Discovers agents started by any method
- Quitting TUI doesn't affect running agents

### US7: Integrated Launch
**As a** developer starting a new agent
**I want to** launch directly into the TUI
**So that** I can immediately monitor the agent I'm starting

**Acceptance Criteria:**
- `clawker ralph run --agent dev --tui` starts agent then launches TUI
- Agent appears in TUI within 2 seconds
- TUI shows all project agents, not just the one started

---

## Functional Requirements

### FR1: Agent Discovery
- Discover containers with labels `com.clawker.project={project}`
- Filter to agents with session files in `~/.local/clawker/ralph/sessions/`
- Refresh every 2 seconds
- Handle containers in all states: running, exited, created

### FR2: Session/Circuit Loading
- Load session from `~/.local/clawker/ralph/sessions/{project}.{agent}.json`
- Load circuit from `~/.local/clawker/ralph/circuit/{project}.{agent}.json`
- Handle missing files gracefully (show "no session")
- Display all session fields: loops, tasks, files, status, error

### FR3: Log Streaming
- Use Docker logs API with `follow=true, tail=100`
- Demultiplex Docker stream format (strip headers)
- Maintain ring buffer of 1000 lines
- Support scrolling through buffer
- Cancel stream when leaving view

### FR4: Navigation
- Three views: main list, agent detail, logs
- Keyboard-based navigation (no mouse)
- Consistent back navigation (Esc)
- Quit from any view (q)

### FR5: Actions
- Stop agent: `docker stop` via Docker API
- Reset circuit: delete circuit state file
- Confirmation prompts for destructive actions
- Success/error feedback

### FR6: Display
- Responsive layout (adapt to terminal size)
- Minimum functional size: 60x20
- Show truncation indicators when content doesn't fit
- Color-coded status (green=running, red=stopped/tripped)

---

## Non-Functional Requirements

### NFR1: Performance
- Agent discovery < 500ms
- Log streaming latency < 2 seconds
- TUI renders at 30fps minimum
- Memory usage < 50MB for 10 agents with logs

### NFR2: Reliability
- Graceful handling of Docker connection errors
- Session file read errors don't crash TUI
- Container state changes detected within 4 seconds
- Clean shutdown on Ctrl+C or q

### NFR3: Compatibility
- Works on macOS terminals (Terminal.app, iTerm2)
- Works on Linux terminals (gnome-terminal, xterm)
- Supports UTF-8 characters
- Handles SIGWINCH (resize) correctly

### NFR4: Maintainability
- < 2000 lines of new code
- Unit tests for components
- Follows existing clawker code style
- Uses existing docker.Client, ralph.SessionStore

---

## Technical Architecture

### Dependencies
```
github.com/charmbracelet/bubbletea v0.25.0
github.com/charmbracelet/lipgloss v0.10.0
```

### Package Structure
```
internal/ralph/tui/
├── model.go          # Root Bubbletea model
├── messages.go       # Message types
├── commands.go       # Async commands
├── views.go          # View rendering
├── agent_list.go     # List component
├── agent_detail.go   # Detail component
├── log_viewer.go     # Log component
├── styles.go         # Styling
├── keybindings.go    # Key handlers
└── discovery.go      # Agent discovery
```

### Data Flow
1. TUI starts → discovers agents via Docker API
2. For each agent → loads session/circuit from filesystem
3. User navigates → updates model state
4. User views logs → starts Docker log stream
5. Tick every 2s → rediscovers agents, refreshes state

---

## UI Mockup

```
╭─────────────────────────────────────────────────────────────────────────╮
│  🤖 RALPH DASHBOARD (myapp)                                  12:34:56  │
╰─────────────────────────────────────────────────────────────────────────╯
 Agents                              Status
 ─────────────────────────────────────────────────────────────────────────
 > ralph        [running]   3 loops   5 tasks   Circuit: 1/3
   worker       [running]   1 loop    0 tasks   Circuit: 0/3
   sandbox      [stopped]   -         -         -

 Session Details (ralph)
 ─────────────────────────────────────────────────────────────────────────
   Started:     2 hours ago
   Last Status: IN_PROGRESS
   Last Error:  (none)

╭─ Keys ──────────────────────────────────────────────────────────────────╮
│ j/k navigate │ Enter details │ l logs │ s stop │ r reset │ q quit      │
╰─────────────────────────────────────────────────────────────────────────╯
```

---

## Key Bindings

| View | Key | Action |
|------|-----|--------|
| Main | j/k, ↑/↓ | Navigate agent list |
| Main | Enter | View agent details |
| Main | l | View logs for selected agent |
| Main | s | Stop selected agent |
| Main | r | Reset circuit for selected agent |
| Main | R | Refresh agent list |
| Main | q | Quit |
| Detail | l | View logs |
| Detail | s | Stop agent |
| Detail | r | Reset circuit |
| Detail | Esc | Back to main |
| Detail | q | Quit |
| Logs | ↑/↓ | Scroll |
| Logs | g | Go to top |
| Logs | G | Go to bottom |
| Logs | Esc | Back |
| Logs | q | Quit |

---

## Implementation Phases

### Phase 1: Foundation (Day 1-2)
- Dependencies, package structure
- Basic Bubbletea model
- `clawker ralph tui` command
- Empty TUI that quits with q

### Phase 2: Agent Discovery (Day 3-4)
- Container discovery via Docker API
- Session/circuit loading
- Agent list view with state

### Phase 3: Navigation (Day 5)
- Agent detail view
- Key bindings
- View transitions

### Phase 4: Log Streaming (Day 6-7)
- Docker log streaming
- Ring buffer
- Scroll support

### Phase 5: Resize & Polish (Day 8)
- Window resize handling
- Responsive layouts
- Error display

### Phase 6: Integration (Day 9)
- `--tui` flag on `ralph run`
- Background runner with foreground TUI

### Phase 7: Actions (Day 10)
- Stop agent
- Reset circuit
- Confirmation prompts

### Phase 8: Testing & Docs (Day 11-12)
- Unit tests
- Integration tests
- Documentation updates

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Log streaming goroutine leaks | Memory, zombie processes | Context cancellation, cleanup on view change |
| Docker connection failures | TUI unusable | Graceful error display, retry button |
| Large log buffers | Memory bloat | Ring buffer with 1000 line limit |
| Terminal compatibility | Rendering issues | Test on major terminals, fall back to ASCII |
| Session file conflicts | Stale data | Eventual consistency acceptable |

---

## Open Questions (Resolved)

| Question | Decision |
|----------|----------|
| Command name | `clawker ralph tui` |
| Attach mode | Read-only log streaming |
| New labels needed | No, use existing + session files |
| Multi-project support | No, current project only |
| Interactive shell | No, read-only only |

---

## Acceptance Criteria Summary

The feature is complete when:
1. ✅ `clawker ralph tui` launches dashboard showing all project agents
2. ✅ Agent list shows name, state, loops, tasks, circuit status
3. ✅ User can navigate with keyboard (j/k, Enter, Esc)
4. ✅ User can view agent details with full session/circuit info
5. ✅ User can stream logs in real-time
6. ✅ User can scroll through log history
7. ✅ User can stop agents with confirmation
8. ✅ User can reset circuits with confirmation
9. ✅ TUI handles terminal resize gracefully
10. ✅ `clawker ralph run --tui` launches TUI after starting agent
11. ✅ Quitting TUI doesn't affect running agents
12. ✅ All existing tests pass
13. ✅ Documentation updated (README, CLI-VERBS)

---

## Related Documents

- **Plan File**: `/Users/andrew/.claude/plans/synchronous-bubbling-teacup.md`
- **Memory**: `ralph_bubbletea_tui_implementation`
- **Existing Design**: `ralph_command_implementation` memory (Phase 2 section)
