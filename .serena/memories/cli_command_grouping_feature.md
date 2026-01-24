# CLI Command Grouping Feature

## Goal

Improve clawker CLI help output UX by organizing commands into logical groups using Cobra's native `AddGroup` feature.

## Status

**Phase: Planning Complete - Ready for Implementation**

## Background

The clawker CLI has ~45 commands that currently display in a flat alphabetical list in `--help` output. The user wants to group these into 3 categories for better UX.

## Proposed Groups

1. **Container Management** - All Docker-like commands (24 total)
   - 20 top-level aliases: `run`, `start`, `stop`, `build`, `ps`, `logs`, `exec`, `attach`, `kill`, `restart`, `pause`, `unpause`, `rm`, `rmi`, `cp`, `create`, `rename`, `wait`, `top`, `stats`
   - 4 management groups: `container`, `image`, `volume`, `network`

2. **Clawker Management** - Setup and configuration (5 total)
   - `init`, `project`, `config`, `monitor`, `generate`

3. **Vibe Commands** - Autonomous agent features (1 total)
   - `ralph`

## Implementation Plan

See detailed plan: `~/.claude/plans/snoopy-juggling-puddle.md`

### Files to Modify

1. `internal/cmd/root/root.go` - Add group definitions with `cmd.AddGroup()`, assign `GroupID` to subcommands
2. `internal/cmd/root/aliases.go` - Assign `GroupID = "container"` to all top-level aliases

### Key Code Pattern

```go
// Define groups (order determines display order)
cmd.AddGroup(&cobra.Group{ID: "container", Title: "Container Management:"})
cmd.AddGroup(&cobra.Group{ID: "clawker", Title: "Clawker Management:"})
cmd.AddGroup(&cobra.Group{ID: "vibe", Title: "Vibe Commands:"})

// Assign to commands
ralphCmd := ralph.NewCmdRalph(f)
ralphCmd.GroupID = "vibe"
cmd.AddCommand(ralphCmd)
```

## TODO Sequence

- [x] Explore current CLI command structure
- [x] Research Cobra command groups API (`AddGroup`, `GroupID`)
- [x] Draft proposed grouping (3 groups)
- [x] Create implementation plan
- [ ] Implement group definitions in `root.go`
- [ ] Assign GroupIDs to all commands in `root.go`
- [ ] Update `aliases.go` to assign GroupID to aliases
- [ ] Test with `clawker --help`
- [ ] Run `go test ./internal/cmd/...`
- [ ] Update documentation if needed

## Technical Notes

- Cobra v1.6.0+ required (already in use)
- Groups must be defined before commands with that GroupID are added
- Commands without GroupID appear in "Additional Commands"
- Display-only change - no command hierarchy restructuring

---

**IMPORTANT**: Always check with the user before proceeding with the next TODO item. When all work is complete, ask the user if they want to delete this memory.
