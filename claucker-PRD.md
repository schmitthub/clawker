
# PRD: Claucker (Claude Container Orchestration)

**Document Status:** `APPROVED`

**Version:** 1.0.0

**Architect:** Distinguished Engineer (AI Partner)

**Date:** January 8, 2026

---

## 1. Executive Summary

**Claucker** is a specialized container orchestration runtime designed to wrap the **Claude Code** agent in a secure, reproducible, and ephemeral sandbox.

Unlike general-purpose tools like Docker Compose, Claucker is opinionated. It treats the AI agent as an untrusted but highly capable operator. It abstracts the complexity of volume management, credential injection, and TTY (Tele-typewriter) pass-through, ensuring that Claude can operate autonomously on a codebase without risking host system integrity.

**Core Philosophy:** "Safe Autonomy." The host system is read-only by default; write permissions are granted explicitly via volume strategies.

---

## 2. Architecture & Design Principles

### 2.1 Domain Model

The system is built around three core domain objects:

1. **The Blueprint (`claucker.yaml`)**: The immutable definition of the environment (OS, tools, allowed paths).
2. **The Workspace**: The filesystem state. This abstracts the difference between a `bind` mount (live host editing) and a `volume` clone (sandboxed experimentation).
3. **The Session**: The runtime execution of the container, managing the lifecycle of the PTY (Pseudo-Terminal) connection between the user's terminal and the Claude process.

### 2.2 Security Architecture

* **Credential Projection**: Anthropic API keys and OAuth tokens must be injected into the container's memory (environment variables) at runtime, never written to disk or baked into images.
* **PID 1 Management**: The container entrypoint must handle signal propagation (`SIGINT`, `SIGTERM`) correctly to ensure the Claude process cleans up resources gracefully when the user exits.

---

## 3. Functional Requirements

### 3.1 Workspace Strategies

Claucker must support two distinct modes of operation, configurable via flags or config:

| Strategy | Flag | Behavior | Use Case |
| --- | --- | --- | --- |
| **Live Link** | `--mode=bind` | Mounts host source directory directly to `/workspace`. Changes are immediate on host. | Pair programming, low-risk refactors. |
| **Sandbox** | `--mode=snapshot` | Copies host source to an ephemeral Docker Volume. Host files are untouched. | "YOLO" mode, massive refactors, risky migrations. |

### 3.2 CLI Commands (Cobra)

| Command | Sub-command | Description |
| --- | --- | --- |
| `claucker` | `init` | Scaffolds `claucker.yaml` and `.clauckerignore`. |
|  | `up` | Idempotent command: Build image  Create Volume  Run Container  Attach TTY. |
|  | `down` | Stops containers. With `--clean`, destroys associated volumes. |
|  | `sh` | escape hatch: Opens a raw bash shell in the running container. |
|  | `logs` | Streams container logs (distinct from the TTY output). |

### 3.3 Configuration Schema (Viper)

The `claucker.yaml` is the source of truth.

```yaml
version: "1"
project: "my-app"

build:
  # Base image containing Claude Code prerequisites
  image: "node:20-slim" 
  # Optional: Dockerfile path if custom build is needed
  dockerfile: "./.devcontainer/Dockerfile"
  # System packages to install (e.g., git, curl, ripgrep)
  packages: ["git", "ripgrep", "make"]

agent:
  # Files specifically needed by Claude (prompts, docs) to be injected
  includes:
    - "./docs/architecture.md"
    - "./.claude/memory.md"
  # Environment variables for the agent
  env:
    NODE_ENV: "development"

workspace:
  remote_path: "/workspace"
  # Default mode if not specified by flag
  default_mode: "snapshot" 

```

---

## 4. Technical Specifications

### 4.1 Technology Stack

* **Language**: Go 1.25+ (Strict typing, generics).
* **CLI Framework**: `github.com/spf13/cobra` (Command structure).
* **Config**: `github.com/spf13/viper` (Loading, unmarshalling, env overrides).
* **Container Runtime**: `github.com/docker/docker/client` (Direct SDK, no shell calls to `docker` binary).
* **Terminal/PTY**: `golang.org/x/term` (Raw terminal mode handling).
* **Logging**: `github.com/rs/zerolog` (Structured JSON logging for debugging).

### 4.2 Error Handling Strategy

* **User-Facing**: Human-readable errors with "Next Steps" (e.g., "Docker daemon not running. Please start Docker Desktop.").
* **Internal**: Stack traces only visible with `--debug` flag.

---

## 5. Development Phases & Milestones

### Phase 1: The Core & Configuration (Days 1-2)

**Goal:** A binary that parses config and establishes the Docker handshake.

* **Task 1.1:** Setup project structure (`cmd/`, `internal/config`, `internal/docker`).
* **Task 1.2:** Implement `claucker.yaml` struct definitions with strictly typed validation.
* **Task 1.3:** Initialize Docker Client and implement a `HealthCheck` function to verify daemon connectivity.
* **Acceptance:** `claucker config check` parses a file and validates rules (e.g., image cannot be empty).

### Phase 2: The Builder Engine (Days 3-4)

**Goal:** Dynamic construction of the execution environment.

* **Task 2.1:** Implement "On-the-fly" Dockerfile generation if one isn't provided (injecting `packages` from config).
* **Task 2.2:** Implement `tar` archiving logic to stream the build context to the Docker daemon.
* **Acceptance:** `claucker build` successfully creates a tagged Docker image.

### Phase 3: The Runtime & Orchestration (Days 5-7)

**Goal:** Lifecycle management and TTY attachment.

* **Task 3.1:** Implement `VolumeStrategy` interface.
* *Bind implementation:* Configures `HostConfig.Binds`.
* *Snapshot implementation:* Creates volume, runs a helper container to `tar` copy source code into volume.


* **Task 3.2:** Implement `ContainerAttach`. This is critical. It must put the user's local terminal into "Raw" mode and pipe `stdin/stdout/stderr` seamlessly to the container to support interactive Claude prompts.
* **Acceptance:** `claucker up` launches Claude, and the user can interactively select options in the Claude UI.

### Phase 4: Polish & Security (Days 8-10)

**Goal:** Credential passing and cleanup.

* **Task 4.1:** Implement automatic `.env` file reading and injection (filtering for keys like `ANTHROPIC_API_KEY`).
* **Task 4.2:** Implement `SignalCatcher` to ensure `Ctrl+C` kills the container, not just the CLI.
* **Acceptance:** The system is robust; network disconnects or forced quits clean up the containers.

---

## 6. Project Structure (Standard Go Layout)

```text
claucker/
├── cmd/
│   ├── root.go              # CLI Entrypoint, Global Flags
│   ├── init.go              # Scaffolding Logic
│   └── up.go                # Orchestration Entrypoint
├── internal/
│   ├── config/              # Viper definition & Validation logic
│   ├── engine/              # Docker SDK Abstractions
│   │   ├── client.go        # Client initialization
│   │   ├── image.go         # Build logic
│   │   └── container.go     # Run logic
│   ├── workspace/           # Strategy Pattern for Bind vs Volume
│   └── term/                # PTY/Raw Mode wrappers
├── pkg/
│   └── logger/              # Zerolog setup
├── go.mod
└── main.go

```

---

## 7. Next Actions

The architectural blueprint is approved. The immediate next step is to initialize the repository and implement the **Configuration Schema**.

**Would you like me to proceed with generating the `internal/config/schema.go` file to lock in the type definitions for the YAML structure?**