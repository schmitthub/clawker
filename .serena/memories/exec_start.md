# Exec and Start Command Agent Flag Implementation

## Overview
Add `--agent` flag to `clawker start` and `clawker exec` commands to allow specifying an agent name that gets resolved to the full container name using project context.

## Research Findings

### Current State
- `start` command: `pkg/cmd/container/start/start.go`
  - Takes positional CONTAINER args (full names like `clawker.myproject.myagent`)
  - Has `--attach` and `--interactive` flags
  - `StartOptions` struct with `Attach` and `Interactive` fields

- `exec` command: `pkg/cmd/container/exec/exec.go`
  - Takes positional CONTAINER and COMMAND args
  - Has flags: `-i`, `-t`, `--detach`, `-e`, `-w`, `-u`, `--privileged`
  - `Options` struct with these fields

### Reference Pattern (from run/create commands)
The `run` and `create` commands already implement `--agent` flag:
```go
// Options struct has:
Agent string // Agent name for clawker naming
Name  string // Full container name (overrides agent)

// Flag registration:
cmd.Flags().StringVar(&opts.Agent, "agent", "", "Agent name (constructs container name as clawker.<project>.<agent>)")
cmd.Flags().StringVar(&opts.Name, "name", "", "Full container name (overrides --agent)")

// Name resolution in run function:
cfg, err := f.Config() // Load config for project name
agent := opts.Agent
containerName := opts.Name
if containerName == "" {
    containerName = docker.ContainerName(cfg.Project, agent)
}
```

### Key Dependencies
- `internal/docker/names.go`: `ContainerName(project, agent string)` - formats as `clawker.<project>.<agent>`
- `internal/docker/labels.go`: `LabelAgent = LabelPrefix + "agent"` - label key for agent name
- `pkg/cmdutil/factory.go`: `Factory.Config()` - loads project config to get project name

## Implementation Complete

The `--agent` flag has been successfully added to both `clawker start` and `clawker exec` commands.

### Changes Made

1. **pkg/cmd/container/start/start.go**
   - Added `Agent` field to `StartOptions` struct
   - Added `--agent` flag registration
   - Updated command usage to make CONTAINER optional when --agent provided
   - Added custom Args validator for mutual exclusivity
   - Modified `runStart` to resolve container name from agent + project config

2. **pkg/cmd/container/exec/exec.go**
   - Added `Agent` field to `Options` struct  
   - Added `--agent` flag registration
   - Updated command usage to make CONTAINER optional when --agent provided
   - Added custom Args validator (1 arg with --agent, 2 args without)
   - Modified `run` to resolve container name from agent + project config

3. **Test files updated**
   - Added test cases for --agent flag parsing
   - Added test case for mutual exclusivity error
   - Added test case for missing command with --agent (exec only)
   - Updated Properties tests to verify --agent flag exists

4. **pkg/cmd/start/start.go** (top-level alias)
   - Updated Use string to reflect optional CONTAINER

### Usage

```bash
# Start using agent name
clawker container start --agent ralph
clawker start --agent ralph

# Exec using agent name  
clawker container exec --agent ralph ls -la
clawker container exec -it --agent ralph /bin/bash
```

The agent name is resolved using the project from clawker.yaml to construct the full container name as `clawker.<project>.<agent>`.
