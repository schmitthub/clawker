# Ralph Bubbletea TUI Dashboard Implementation

## End Goal
Implement a Bubbletea-based terminal dashboard for monitoring and controlling ralph autonomous loops with multi-agent support, real-time log streaming, and basic control operations.

## Background Context

### User Requirements (from clarifying questions)
1. **Multi-agent main view** - Show ALL ralph agents for current project using container labels
2. **Real-time streaming** - Select an agent to see live output (read-only), hotkey to return to main view
3. **Standalone command** - `clawker ralph tui` to launch TUI independently
4. **Integrated mode** - `clawker ralph run --tui` to launch into TUI after starting
5. **View + basic actions** - List agents, select to view, start/stop all, show overall task statuses
6. **Full agent controls** - Per-agent view shows live logs, agent-specific controls
7. **Graceful resize** - Adapt to terminal size changes
8. **Project-scoped** - Only show agents from current project (from clawker.yaml)
9. **Attach mode** - Read-only log streaming (not interactive attach)
10. **Standalone command name** - `clawker ralph tui`

### Key Technical Decisions
- Discovery uses existing labels + session file existence (no new labels needed)
- Polling every 2 seconds for agent discovery
- Log streaming via Docker API with goroutine
- Ring buffer for log lines (1000 max)
- Bubbletea MVC pattern with message-driven updates

### Existing Codebase Context
- `internal/ralph/loop.go` - Runner with callbacks (OnLoopStart, OnLoopEnd, OnOutput)
- `internal/ralph/monitor.go` - Current text-based monitor
- `internal/ralph/session.go` - Session persistence at `~/.local/clawker/ralph/sessions/`
- `internal/ralph/circuit.go` - Circuit breaker state
- `internal/ralph/analyzer.go` - Status struct with all display fields
- `internal/cmd/ralph/run.go` - Existing run command to modify

## Implementation Plan

### Package Structure (Two-Package Design)
```
internal/tui/                 # SHARED TUI COMPONENTS (now fully implemented!)
├── tokens.go                 # Design tokens (spacing, breakpoints)
├── text.go                   # Text manipulation (truncate, pad, wrap)
├── time.go                   # Time formatting (relative, duration)
├── styles.go                 # Lipgloss color palette, component styles
├── keys.go                   # Key bindings and input helpers
├── layout.go                 # Layout composition (splits, stacks, grids)
├── components.go             # Stateless renders (headers, badges, tables)
├── spinner.go                # Animated spinner component
├── panel.go                  # Bordered panels with focus management
├── list.go                   # Selectable list with scrolling
├── statusbar.go              # Single-line status bars
└── help.go                   # Help bars with key bindings

internal/ralph/tui/           # RALPH-SPECIFIC TUI
├── model.go                  # Bubbletea model (Init/Update/View)
├── messages.go               # Ralph-specific Msg types
├── commands.go               # tea.Cmd functions (future phases)
├── agent_list.go             # Agent list component (future phases)
├── agent_detail.go           # Agent detail component (future phases)
├── log_viewer.go             # Log streaming component (future phases)
├── keybindings.go            # Key handlers (future phases)
└── discovery.go              # Container/session discovery (future phases)

internal/cmd/ralph/
├── ralph.go                  # Modified: registers tui subcommand
└── tui.go                    # NEW: `clawker ralph tui` command
```

### Files to Modify
- `internal/cmd/ralph/ralph.go` - Add tui subcommand ✅
- `internal/cmd/ralph/run.go` - Add --tui flag (Phase 6)
- `go.mod` - Add bubbletea, lipgloss, bubbles ✅

## Step-by-Step TODOs

### Phase 1: Foundation ✅ COMPLETE
- [x] Add Bubbletea + Lipgloss dependencies to go.mod
- [x] Create `internal/tui/` shared TUI components package
- [x] Create `internal/ralph/tui/` ralph-specific TUI package
- [x] Implement `internal/tui/styles.go` - Color palette, common styles
- [x] Implement `internal/tui/keys.go` - Common key bindings
- [x] Implement `internal/ralph/tui/messages.go` - Msg type definitions
- [x] Implement `internal/ralph/tui/model.go` - Model with Init/Update/View
- [x] Create `internal/cmd/ralph/tui.go` - Cobra command
- [x] Modify `internal/cmd/ralph/ralph.go` - Register tui subcommand
- [x] Add unit tests for all new packages
- [x] Update CLI-VERBS.md documentation
- [x] Test: `clawker ralph tui` shows TUI, press q to quit

### Phase 2: Agent Discovery
- [ ] Implement `discovery.go` - container + session discovery
- [ ] Implement `commands.go` - discoverAgentsCmd, loadSessionCmd, tickCmd
- [ ] Implement `agent_list.go` - list component with formatting
- [ ] Update `model.go` - handle discovery messages
- [ ] Update `views.go` - render agent list
- [ ] Test: TUI shows list of agents with session state

### Phase 3: Navigation
- [ ] Implement `keybindings.go` - key handlers per view
- [ ] Implement `agent_detail.go` - detail component
- [ ] Update `model.go` - view navigation (main ↔ detail)
- [ ] Test: Navigate list, Enter for details, Esc to go back

### Phase 4: Log Streaming
- [ ] Implement `log_viewer.go` - component with ring buffer
- [ ] Update `commands.go` - streamLogsCmd with goroutine
- [ ] Update `model.go` - handle LogChunkMsg, LogStreamEndedMsg
- [ ] Add scrolling support
- [ ] Test: View logs in real-time, scroll up/down

### Phase 5: Window Resize
- [ ] Update `model.go` - handle WindowSizeMsg
- [ ] Update all components - respect width/height
- [ ] Add responsive layout helpers
- [ ] Test: Resize terminal, TUI adapts

### Phase 6: Integration with `ralph run`
- [ ] Modify `internal/cmd/ralph/run.go` - add --tui flag
- [ ] Launch TUI in foreground after runner starts in background
- [ ] Test: `clawker ralph run --agent dev --tui`

### Phase 7: Basic Actions
- [ ] Add stopAgentCmd, resetCircuitCmd to `commands.go`
- [ ] Add confirmation prompts for destructive actions
- [ ] Update keybindings and views
- [ ] Test: Reset circuit from TUI

### Phase 8: Polish
- [ ] Add loading indicators
- [ ] Add error display (status bar)
- [ ] Add help view (press `?`)
- [ ] Update README.md and CLI-VERBS.md
- [ ] Write unit tests for components
- [ ] Test: Full workflow

## Key Types Reference

```go
type AgentInfo struct {
    ContainerName string
    ContainerID   string
    Project       string
    Agent         string
    State         string           // running, exited, created
    StartedAt     time.Time
    Session       *ralph.Session
    Circuit       *ralph.CircuitState
}

type Model struct {
    currentView   ViewType         // main|agentDetail|logs
    project       string
    agents        []AgentInfo
    selectedIdx   int
    logLines      []string         // Ring buffer (1000 lines max)
    width, height int
    client        *docker.Client
    store         *ralph.SessionStore
}
```

## Key Bindings
- Main view: j/k navigate, Enter details, l logs, s stop, r reset, q quit
- Agent detail: l logs, s stop, r reset, Esc back, q quit
- Log view: ↑/↓ scroll, g/G top/bottom, Esc back, q quit

## Plan File Reference
**Claude Code Plan File**: `/Users/andrew/.claude/plans/synchronous-bubbling-teacup.md`

## Dependencies Added ✅
```
github.com/charmbracelet/bubbletea v1.3.10
github.com/charmbracelet/lipgloss v1.1.0
github.com/charmbracelet/bubbles v0.21.0
```

## Lessons Learned
- User wanted significantly larger scope than original memory plan (multi-agent, not single-agent)
- Standalone TUI is critical - must work independently of ralph run
- Discovery via session files + existing labels works (no new labels needed)
- Read-only log streaming preferred over interactive attach
- **internal/tui/ is now fully implemented** - Use `tui.ListModel` for agent list, `tui.PanelModel` for details, `tui.RenderStatusBar` for status, `tui.RenderHelpBar` for help. See `tui_components_package.md` memory for full API reference.

## Available TUI Components for Ralph
The `internal/tui/` package now provides ready-to-use components:
- **tui.ListModel** - Use for agent list with `SetItems()`, `SelectNext()`, `SelectedItem()`
- **tui.PanelModel** - Use for agent detail panels with borders and focus states
- **tui.SpinnerModel** - Use for loading indicators
- **tui.StatusBarModel** / **tui.RenderStatusBar** - Use for status display
- **tui.RenderHelpBar** - Use for key binding help
- **tui.SplitHorizontal/Vertical** - Use for layout splitting
- **tui.Stack/Row** - Use for composing views
- **tui.FormatRelative/Duration/Uptime** - Use for time display
- **tui.IsUp/IsDown/IsEnter/IsQuit** - Use for key handling in Update()

---

**IMPERATIVE**: Always check with the user before proceeding with the next TODO item. If all work is complete, ask the user if they want to delete this memory.
