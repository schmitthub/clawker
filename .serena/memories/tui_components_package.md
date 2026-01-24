# Reusable TUI Components Package

## Status: COMPLETE

## Package: `internal/tui/`

A comprehensive set of reusable BubbleTea components for building terminal user interfaces in clawker. These components provide consistent styling, responsive layouts, and ready-to-use interactive widgets.

## When to Use This Package

Use `internal/tui/` when:
- Building any terminal user interface in clawker (e.g., ralph TUI dashboard)
- Need consistent styling across TUI features
- Want responsive layouts that adapt to terminal size
- Need text formatting (truncation, padding, wrapping) for terminal display
- Building interactive components (lists, panels, spinners)
- Displaying time-based information (durations, relative times)

## File Overview

| File | Category | Purpose |
|------|----------|---------|
| `tokens.go` | Foundation | Design tokens - spacing constants, layout breakpoints, utility functions |
| `text.go` | Foundation | Text manipulation - truncate, pad, wrap, ANSI handling |
| `time.go` | Foundation | Time formatting - relative times, durations, timestamps |
| `styles.go` | Foundation | Lipgloss styles - colors, text styles, component styles |
| `keys.go` | Foundation | Key bindings - KeyMap, Is* helpers for input handling |
| `layout.go` | Layout | Layout composition - splits, stacks, grids, responsive helpers |
| `components.go` | Components | Stateless renders - headers, badges, tables, dividers |
| `spinner.go` | Interactive | Animated spinner with multiple styles |
| `panel.go` | Interactive | Bordered panels with focus states and groups |
| `list.go` | Interactive | Selectable list with scrolling and pagination |
| `statusbar.go` | Navigation | Single-line status bar with left/center/right sections |
| `help.go` | Navigation | Help bar with key binding display |

## API Reference

### Design Tokens (`tokens.go`)

```go
// Spacing constants for consistent margins/padding
const (
    SpaceNone = 0   // No spacing
    SpaceXS   = 1   // Extra small (1 char)
    SpaceSM   = 2   // Small (2 chars)
    SpaceMD   = 4   // Medium (4 chars)
    SpaceLG   = 8   // Large (8 chars)
)

// Layout breakpoints for responsive design
const (
    WidthCompact = 60   // Narrow terminals
    WidthNormal  = 80   // Standard terminals
    WidthWide    = 120  // Wide terminals
)

// Layout mode detection
type LayoutMode int
const (
    LayoutCompact LayoutMode = iota
    LayoutNormal
    LayoutWide
)

func GetLayoutMode(width int) LayoutMode  // Returns mode for width
func GetContentWidth(totalWidth int) int   // Returns usable content width
func GetContentHeight(total, header, footer int) int  // Returns content height

// Utility functions
func MinInt(a, b int) int
func MaxInt(a, b int) int
func ClampInt(value, minVal, maxVal int) int
```

### Text Manipulation (`text.go`)

```go
// Truncation
func Truncate(s string, maxLen int) string        // "hello world" → "hello..."
func TruncateMiddle(s string, maxLen int) string  // "/very/long/path" → "/very.../path"

// Padding
func PadRight(s string, width int) string   // "hi" → "hi    "
func PadLeft(s string, width int) string    // "hi" → "    hi"
func PadCenter(s string, width int) string  // "hi" → "  hi  "

// Wrapping
func WordWrap(s string, width int) string      // Returns wrapped string
func WrapLines(s string, width int) []string   // Returns slice of lines

// ANSI handling
func CountVisibleWidth(s string) int  // Counts chars excluding ANSI codes
func StripANSI(s string) string       // Removes ANSI escape sequences

// Utilities
func Indent(s string, prefix string) string     // Adds prefix to each line
func JoinNonEmpty(sep string, parts ...string) string  // Joins non-empty strings
func Repeat(s string, count int) string         // Repeats string
func FirstLine(s string) string                 // Returns first line only
func LineCount(s string) int                    // Counts lines
```

### Time Formatting (`time.go`)

```go
func FormatRelative(t time.Time) string      // "2 hours ago", "in 5 minutes"
func FormatDuration(d time.Duration) string  // "2m 30s", "1h 15m"
func FormatUptime(d time.Duration) string    // "01:15:42" (HH:MM:SS)
func FormatTimestamp(t time.Time, short bool) string  // Formatted timestamp
func FormatDate(t time.Time) string          // "2024-01-15"
func FormatDateTime(t time.Time) string      // "2024-01-15 10:30:00"
func ParseDurationOrDefault(s string, def time.Duration) time.Duration
```

### Styles (`styles.go`)

```go
// Colors
var (
    ColorPrimary   = lipgloss.Color("#7C3AED")  // Purple
    ColorSuccess   = lipgloss.Color("#10B981")  // Green
    ColorWarning   = lipgloss.Color("#F59E0B")  // Amber
    ColorError     = lipgloss.Color("#EF4444")  // Red
    ColorInfo      = lipgloss.Color("#87CEEB")  // Sky blue
    ColorMuted     = lipgloss.Color("#6B7280")  // Gray
    ColorDisabled  = lipgloss.Color("#4A4A4A")  // Dark gray
    ColorSelected  = lipgloss.Color("#FFD700")  // Gold
    ColorBorder    = lipgloss.Color("#3C3C3C")  // Border gray
    ColorAccent    = lipgloss.Color("#60A5FA")  // Light blue
    ColorBg        = lipgloss.Color("#1A1A1A")  // Background
    ColorBgAlt     = lipgloss.Color("#2A2A2A")  // Alt background
)

// Text styles
var (
    BoldStyle, MutedStyle, ErrorStyle, SuccessStyle, WarningStyle lipgloss.Style
)

// Component styles
var (
    HeaderStyle, PanelStyle, PanelActiveStyle lipgloss.Style
    ListItemStyle, ListItemSelectedStyle, ListItemDimStyle lipgloss.Style
    HelpKeyStyle, HelpDescStyle lipgloss.Style
    LabelStyle, ValueStyle, CountStyle lipgloss.Style
    BadgeStyle, BadgeMutedStyle lipgloss.Style
    StatusRunningStyle, StatusStoppedStyle, StatusErrorStyle lipgloss.Style
    EmptyStateStyle lipgloss.Style
)

// Border styles
var (
    BorderRounded, BorderDouble, BorderThick lipgloss.Border
)

// Helper function
func StatusIndicator(running bool) string  // Returns "●" or "○" with color
```

### Key Bindings (`keys.go`)

```go
type KeyMap struct {
    Up, Down, Left, Right key.Binding
    Enter, Escape, Tab    key.Binding
    Help, Quit           key.Binding
}

func DefaultKeyMap() KeyMap  // Returns standard key bindings

// Input helpers - use these in Update() to check key presses
func IsQuit(msg tea.KeyMsg) bool    // q or ctrl+c
func IsUp(msg tea.KeyMsg) bool      // up arrow or k
func IsDown(msg tea.KeyMsg) bool    // down arrow or j
func IsLeft(msg tea.KeyMsg) bool    // left arrow or h
func IsRight(msg tea.KeyMsg) bool   // right arrow or l
func IsEnter(msg tea.KeyMsg) bool   // enter
func IsEscape(msg tea.KeyMsg) bool  // esc
func IsTab(msg tea.KeyMsg) bool     // tab
func IsHelp(msg tea.KeyMsg) bool    // ?
```

### Layout Composition (`layout.go`)

```go
// Split configurations
type SplitConfig struct {
    Ratio     float64  // 0.0-1.0, portion for first section
    MinFirst  int      // Minimum width/height for first
    MinSecond int      // Minimum width/height for second
    Gap       int      // Gap between sections
}

func SplitHorizontal(width int, cfg SplitConfig) (first, second int)
func SplitVertical(height int, cfg SplitConfig) (first, second int)

// Composition
func Stack(spacing int, components ...string) string   // Vertical stack with newlines
func Row(spacing int, components ...string) string     // Horizontal row with spaces
func Columns(width, gap int, contents ...string) string  // Equal-width columns
func CenterInRect(content string, width, height int) string

// Alignment
func AlignLeft(s string, width int) string
func AlignRight(s string, width int) string
func AlignCenter(s string, width int) string

// Flex layout (like CSS flexbox)
func FlexRow(width int, left, center, right string) string

// Grid layout
type GridConfig struct {
    Columns int
    Gap     int
    Width   int
}
func Grid(items []string, cfg GridConfig) string

// Box (bordered container)
type BoxConfig struct {
    Width, Height int
    Title         string
    Border        lipgloss.Border
    Padding       int
}
func Box(content string, cfg BoxConfig) string

// Responsive layout - returns different content based on width
func ResponsiveLayout(width int, compact, normal, wide string) string
```

### Stateless Components (`components.go`)

```go
// Headers
type HeaderConfig struct {
    Title, Subtitle, Timestamp string
    Width                      int
    Style                      lipgloss.Style
}
func RenderHeader(cfg HeaderConfig) string

// Status indicators
type StatusConfig struct {
    Running bool
    Label   string  // Optional custom label
}
func RenderStatus(cfg StatusConfig) string  // "● Running" or "○ Stopped"

// Badges
func RenderBadge(text string, style lipgloss.Style) string
func RenderCountBadge(count int, label string) string  // "5 items"

// Progress
type ProgressConfig struct {
    Current, Total int
    Width          int
    ShowBar        bool  // true = progress bar, false = "3/10"
}
func RenderProgress(cfg ProgressConfig) string

// Dividers
func RenderDivider(width int, style lipgloss.Style) string
func RenderLabeledDivider(label string, width int) string  // "── Label ──"

// States
func RenderEmptyState(message string, width, height int) string
func RenderError(err error, width int) string

// Key-value display
func RenderLabelValue(label, value string) string
type KeyValuePair struct { Key, Value string }
func RenderKeyValueTable(pairs []KeyValuePair, width int) string

// Tables
type TableConfig struct {
    Headers []string
    Rows    [][]string
    Width   int
}
func RenderTable(cfg TableConfig) string

// Utilities
func RenderPercentage(value float64) string  // "75%" with color coding
func RenderBytes(bytes int64) string         // "1.5 GB"
func RenderTag(text string) string
func RenderTags(tags []string) string
```

### Spinner Component (`spinner.go`)

```go
type SpinnerType int
const (
    SpinnerDots SpinnerType = iota  // ⣾⣽⣻⢿⡿⣟⣯⣷
    SpinnerLine                      // -\|/
    SpinnerMiniDots                  // ⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏
    SpinnerJump                      // ⢄⢂⢁⡁⡈⡐⡠
    SpinnerPulse                     // █▓▒░
    SpinnerPoints                    // ∙∙∙●∙∙
    SpinnerGlobe                     // 🌍🌎🌏
    SpinnerMoon                      // 🌑🌒🌓🌔🌕🌖🌗🌘
    SpinnerMonkey                    // 🙈🙉🙊
)

type SpinnerModel struct { ... }

func NewSpinner(spinnerType SpinnerType, label string) SpinnerModel
func (m SpinnerModel) Init() tea.Cmd
func (m SpinnerModel) Update(msg tea.Msg) (SpinnerModel, tea.Cmd)
func (m SpinnerModel) View() string

// Fluent setters
func (m SpinnerModel) SetLabel(label string) SpinnerModel
func (m SpinnerModel) SetStyle(style lipgloss.Style) SpinnerModel
func (m SpinnerModel) SetSpinnerType(t SpinnerType) SpinnerModel

// Getters
func (m SpinnerModel) Label() string
func (m SpinnerModel) IsSpinning() bool
```

### Panel Component (`panel.go`)

```go
type PanelConfig struct {
    Title   string
    Width   int
    Height  int
    Focused bool
}

type PanelModel struct { ... }

func NewPanel(cfg PanelConfig) PanelModel
func (p PanelModel) View() string

// Fluent setters
func (p PanelModel) SetContent(content string) PanelModel
func (p PanelModel) SetTitle(title string) PanelModel
func (p PanelModel) SetFocused(focused bool) PanelModel
func (p PanelModel) SetWidth(width int) PanelModel
func (p PanelModel) SetHeight(height int) PanelModel

// Getters
func (p PanelModel) Content() string
func (p PanelModel) Title() string
func (p PanelModel) IsFocused() bool

// Convenience renders
func RenderInfoPanel(title, content string, width int) string
func RenderDetailPanel(title string, pairs []KeyValuePair, width int) string
func RenderScrollablePanel(title string, lines []string, offset, visible, width int) string

// Panel groups for multi-panel layouts
type PanelGroup struct { ... }
func NewPanelGroup(panels ...PanelModel) *PanelGroup
func (g *PanelGroup) FocusNext() *PanelGroup
func (g *PanelGroup) FocusPrev() *PanelGroup
func (g *PanelGroup) FocusedIndex() int
func (g *PanelGroup) Panels() []PanelModel
```

### List Component (`list.go`)

```go
// Interface for list items
type ListItem interface {
    Title() string
    Description() string
    FilterValue() string
}

// Simple implementation
type SimpleListItem struct {
    ItemTitle       string
    ItemDescription string
}

type ListConfig struct {
    Width            int
    Height           int
    ShowDescriptions bool
    Wrap             bool  // Wrap selection at ends
}

func DefaultListConfig() ListConfig

type ListModel struct { ... }

func NewList(cfg ListConfig) ListModel
func (m ListModel) Update(msg tea.Msg) (ListModel, tea.Cmd)
func (m ListModel) View() string

// Fluent setters
func (m ListModel) SetItems(items []ListItem) ListModel
func (m ListModel) SetWidth(width int) ListModel
func (m ListModel) SetHeight(height int) ListModel
func (m ListModel) SetShowDescriptions(show bool) ListModel
func (m ListModel) SetWrap(wrap bool) ListModel

// Navigation
func (m ListModel) SelectNext() ListModel
func (m ListModel) SelectPrev() ListModel
func (m ListModel) SelectFirst() ListModel
func (m ListModel) SelectLast() ListModel
func (m ListModel) Select(index int) ListModel
func (m ListModel) PageUp() ListModel
func (m ListModel) PageDown() ListModel

// Getters
func (m ListModel) SelectedItem() ListItem
func (m ListModel) SelectedIndex() int
func (m ListModel) Items() []ListItem
func (m ListModel) Len() int
func (m ListModel) IsEmpty() bool
```

### Status Bar (`statusbar.go`)

```go
type StatusBarModel struct { ... }

func NewStatusBar(width int) StatusBarModel
func (m StatusBarModel) View() string

// Fluent setters
func (m StatusBarModel) SetLeft(s string) StatusBarModel
func (m StatusBarModel) SetCenter(s string) StatusBarModel
func (m StatusBarModel) SetRight(s string) StatusBarModel
func (m StatusBarModel) SetWidth(width int) StatusBarModel
func (m StatusBarModel) SetStyle(style lipgloss.Style) StatusBarModel

// Getters
func (m StatusBarModel) Left() string
func (m StatusBarModel) Center() string
func (m StatusBarModel) Right() string
func (m StatusBarModel) Width() int

// Convenience function
func RenderStatusBar(left, center, right string, width int) string

// Section-based rendering
type StatusBarSection struct {
    Content string
    Style   lipgloss.Style
}
func RenderStatusBarWithSections(sections []StatusBarSection, width int) string

// Pre-built indicators
func ModeIndicator(mode string, active bool) string     // "INSERT" badge
func ConnectionIndicator(connected bool) string         // "● Connected"
func TimerIndicator(label, value string) string         // "Uptime: 01:23:45"
func CounterIndicator(label string, current, total int) string  // "Loop: 5/10"
```

### Help Bar (`help.go`)

```go
type HelpConfig struct {
    Width     int
    ShowAll   bool    // Show all bindings vs short help
    Separator string  // Default: " • "
}

func DefaultHelpConfig() HelpConfig

type HelpModel struct { ... }

func NewHelp(cfg HelpConfig) HelpModel
func (m HelpModel) View() string
func (m HelpModel) ShortHelp() string  // Fits in width
func (m HelpModel) FullHelp() string   // All bindings

// Fluent setters
func (m HelpModel) SetBindings(bindings []key.Binding) HelpModel
func (m HelpModel) SetWidth(width int) HelpModel
func (m HelpModel) SetShowAll(showAll bool) HelpModel
func (m HelpModel) SetSeparator(sep string) HelpModel

// Getters
func (m HelpModel) Bindings() []key.Binding

// Convenience functions
func RenderHelpBar(bindings []key.Binding, width int) string
func RenderHelpGrid(bindings []key.Binding, columns, width int) string

// Pre-built binding sets
func NavigationBindings() []key.Binding  // up, down, enter, esc
func QuitBindings() []key.Binding        // q
func AllBindings() []key.Binding         // All default bindings

// Quick helpers
func HelpBinding(keys, desc string) string      // "q quit"
func QuickHelp(pairs ...string) string          // QuickHelp("q", "quit", "?", "help")
```

## Usage Examples

### Basic TUI Layout

```go
func (m Model) View() string {
    // Header
    header := tui.RenderHeader(tui.HeaderConfig{
        Title:     "RALPH DASHBOARD",
        Subtitle:  m.project,
        Timestamp: tui.FormatTimestamp(time.Now(), true),
        Width:     m.width,
    })

    // Split layout
    leftW, rightW := tui.SplitHorizontal(m.width, tui.SplitConfig{
        Ratio: 0.4, MinLeft: 30, MinRight: 40, Gap: 1,
    })

    // Content
    agentList := m.renderAgentList(leftW)
    details := m.renderDetails(rightW)
    content := tui.Row(1, agentList, details)

    // Help bar
    footer := tui.RenderHelpBar(m.getBindings(), m.width)

    return tui.Stack(0, header, content, footer)
}
```

### Responsive Design

```go
func (m Model) View() string {
    mode := tui.GetLayoutMode(m.width)
    
    switch mode {
    case tui.LayoutCompact:
        return m.renderCompactView()
    case tui.LayoutNormal:
        return m.renderNormalView()
    case tui.LayoutWide:
        return m.renderWideView()
    }
    return ""
}

// Or use ResponsiveLayout helper
func (m Model) View() string {
    return tui.ResponsiveLayout(m.width,
        m.renderCompactView(),
        m.renderNormalView(),
        m.renderWideView(),
    )
}
```

### Interactive List with Selection

```go
type Model struct {
    list tui.ListModel
    // ...
}

func NewModel() Model {
    list := tui.NewList(tui.ListConfig{
        Width:            40,
        Height:           10,
        ShowDescriptions: true,
    })
    
    items := []tui.ListItem{
        tui.SimpleListItem{ItemTitle: "Agent 1", ItemDescription: "Running"},
        tui.SimpleListItem{ItemTitle: "Agent 2", ItemDescription: "Stopped"},
    }
    list = list.SetItems(items)
    
    return Model{list: list}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        if tui.IsEnter(msg) {
            selected := m.list.SelectedItem()
            // Handle selection
        }
    }
    
    var cmd tea.Cmd
    m.list, cmd = m.list.Update(msg)
    return m, cmd
}

func (m Model) View() string {
    return m.list.View()
}
```

### Panel Groups for Multi-Panel UI

```go
type Model struct {
    panelGroup *tui.PanelGroup
}

func NewModel() Model {
    leftPanel := tui.NewPanel(tui.PanelConfig{
        Title: "Agents", Width: 40, Height: 20,
    })
    rightPanel := tui.NewPanel(tui.PanelConfig{
        Title: "Details", Width: 40, Height: 20,
    })
    
    return Model{
        panelGroup: tui.NewPanelGroup(leftPanel, rightPanel),
    }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        if tui.IsTab(msg) {
            m.panelGroup = m.panelGroup.FocusNext()
        }
    }
    return m, nil
}
```

### Status Bar with Indicators

```go
func (m Model) renderStatusBar() string {
    left := tui.ModeIndicator("running", true) + " " + 
            tui.ConnectionIndicator(m.connected)
    
    center := tui.CounterIndicator("Loop", m.currentLoop, m.maxLoops)
    
    right := tui.TimerIndicator("Uptime", tui.FormatUptime(m.uptime))
    
    return tui.RenderStatusBar(left, center, right, m.width)
}
```

## Testing

All components have comprehensive tests in `*_test.go` files.

```bash
go test ./internal/tui/...  # Run all TUI tests
```

## Known Limitations

1. **Visual Width**: `CountVisibleWidth` counts runes, not true visual width. Double-width characters (CJK) are counted as 1, not 2. For most ASCII-based TUIs this is fine.

2. **ANSI Handling**: `StripANSI` removes escape codes but doesn't handle all edge cases. Complex nested ANSI sequences may not be fully stripped.

3. **Spinner**: Requires calling `Init()` and handling tick messages in your Update loop.

## Dependencies

```
github.com/charmbracelet/bubbletea v1.3.10
github.com/charmbracelet/lipgloss v1.1.0
github.com/charmbracelet/bubbles v0.21.0
```
