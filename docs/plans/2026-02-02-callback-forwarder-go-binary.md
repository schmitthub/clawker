# Replace callback-forwarder.sh with Go Binary — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the `callback-forwarder.sh` shell script with a compiled Go binary, eliminating `curl`/`jq` runtime dependencies and mirroring the existing `ssh-agent-proxy.go` pattern.

**Architecture:** The Go source lives as a single file at `internal/build/templates/callback-forwarder.go` (alongside `ssh-agent-proxy.go`). It's embedded via `//go:embed`, distributed to Docker build contexts, compiled in a multi-stage Dockerfile builder stage, and COPY'd into the final image. The existing `internal/build/callback-forwarder/` package is deleted.

**Tech Stack:** Go stdlib (`flag`, `net/http`, `encoding/json`), Docker multi-stage builds, `//go:embed`

---

### Task 1: Create the fixed Go source file

**Files:**
- Create: `internal/build/templates/callback-forwarder.go`
- Reference: `internal/build/callback-forwarder/main.go` (existing, to be deleted later)

**Step 1: Write the Go source**

Create `internal/build/templates/callback-forwarder.go` based on the existing `internal/build/callback-forwarder/main.go` with these behavioral fixes:

```go
// callback-forwarder polls the host proxy for captured OAuth callback data and
// forwards it to the local HTTP server (Claude Code's callback listener).
//
// Usage:
//
//	callback-forwarder -session SESSION_ID -port PORT [-proxy URL] [-timeout SECONDS] [-poll SECONDS]
//
// Environment variables:
//
//	CLAWKER_HOST_PROXY: Host proxy URL (default: http://host.docker.internal:18374)
//	CALLBACK_SESSION: Session ID to poll for
//	CALLBACK_PORT: Local port to forward callback to
//	CB_FORWARDER_TIMEOUT: Timeout in seconds (default: 300)
//	CB_FORWARDER_POLL_INTERVAL: Poll interval in seconds (default: 2)
//	CB_FORWARDER_CLEANUP: Delete session after forwarding (default: true)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// CallbackData matches the CallbackData struct from the host proxy.
type CallbackData struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	ReceivedAt string            `json:"received_at"`
}

// CallbackDataResponse matches the response from GET /callback/{session}/data.
type CallbackDataResponse struct {
	Received bool          `json:"received"`
	Callback *CallbackData `json:"callback,omitempty"`
	Error    string        `json:"error,omitempty"`
}

func main() {
	// Parse flags
	sessionID := flag.String("session", os.Getenv("CALLBACK_SESSION"), "Callback session ID")
	port := flag.Int("port", 0, "Local port to forward callback to")
	proxyURL := flag.String("proxy", os.Getenv("CLAWKER_HOST_PROXY"), "Host proxy URL")
	timeout := flag.Int("timeout", 300, "Timeout in seconds (default: 300)")
	pollInterval := flag.Int("poll", 2, "Poll interval in seconds (default: 2)")
	cleanup := flag.Bool("cleanup", true, "Delete session after forwarding (default: true)")
	verbose := flag.Bool("v", false, "Verbose output")
	flag.Parse()

	// Environment variable fallbacks for flags (CB_FORWARDER_ prefix to avoid collisions)
	if !flagWasSet("timeout") {
		if v := os.Getenv("CB_FORWARDER_TIMEOUT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*timeout = n
			}
		}
	}
	if !flagWasSet("poll") {
		if v := os.Getenv("CB_FORWARDER_POLL_INTERVAL"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*pollInterval = n
			}
		}
	}
	if !flagWasSet("cleanup") {
		if v := os.Getenv("CB_FORWARDER_CLEANUP"); v != "" {
			*cleanup = v == "true" || v == "1" || v == "yes"
		}
	}

	// Handle port from environment if not set via flag
	if *port == 0 {
		portEnv := os.Getenv("CALLBACK_PORT")
		if portEnv != "" {
			if _, err := fmt.Sscanf(portEnv, "%d", port); err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid CALLBACK_PORT value '%s': %v\n", portEnv, err)
				os.Exit(1)
			}
		}
	}

	// Validate required parameters
	if *sessionID == "" {
		fmt.Fprintln(os.Stderr, "Error: session ID required (-session or CALLBACK_SESSION)")
		os.Exit(1)
	}
	if *port == 0 {
		fmt.Fprintln(os.Stderr, "Error: port required (-port or CALLBACK_PORT)")
		os.Exit(1)
	}
	if *proxyURL == "" {
		// Default to host.docker.internal for Docker containers
		*proxyURL = "http://host.docker.internal:18374"
	}

	// Ensure proxyURL doesn't have trailing slash
	*proxyURL = strings.TrimSuffix(*proxyURL, "/")

	if *verbose {
		fmt.Fprintf(os.Stderr, "Waiting for OAuth callback...\n")
		fmt.Fprintf(os.Stderr, "  Session: %s\n", *sessionID)
		fmt.Fprintf(os.Stderr, "  Port: %d\n", *port)
		fmt.Fprintf(os.Stderr, "  Proxy: %s\n", *proxyURL)
		fmt.Fprintf(os.Stderr, "  Timeout: %ds\n", *timeout)
	}

	// Create HTTP client with reasonable timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	dataURL := fmt.Sprintf("%s/callback/%s/data", *proxyURL, *sessionID)
	deleteURL := fmt.Sprintf("%s/callback/%s", *proxyURL, *sessionID)
	deadline := time.Now().Add(time.Duration(*timeout) * time.Second)

	// Track consecutive errors for user feedback
	consecutiveErrors := 0
	const maxSilentErrors = 3

	// Poll for callback data
	for time.Now().Before(deadline) {
		resp, err := client.Get(dataURL)
		if err != nil {
			consecutiveErrors++
			if *verbose {
				fmt.Fprintf(os.Stderr, "Poll error: %v\n", err)
			} else if consecutiveErrors == maxSilentErrors {
				fmt.Fprintln(os.Stderr, "Warning: multiple poll errors, retrying...")
			}
			time.Sleep(time.Duration(*pollInterval) * time.Second)
			continue
		}
		consecutiveErrors = 0

		// Check status code first before decoding
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			fmt.Fprintln(os.Stderr, "Error: session not found or expired")
			os.Exit(1)
		}

		var dataResp CallbackDataResponse
		if err := json.NewDecoder(resp.Body).Decode(&dataResp); err != nil {
			resp.Body.Close()
			if *verbose {
				fmt.Fprintf(os.Stderr, "Decode error: %v\n", err)
			}
			time.Sleep(time.Duration(*pollInterval) * time.Second)
			continue
		}
		resp.Body.Close()

		if !dataResp.Received {
			// No callback yet, keep polling
			time.Sleep(time.Duration(*pollInterval) * time.Second)
			continue
		}

		// Callback received! Forward it
		if *verbose {
			fmt.Fprintf(os.Stderr, "Callback received, forwarding to localhost:%d\n", *port)
		}

		err = forwardCallback(client, *port, dataResp.Callback)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error forwarding callback: %v\n", err)
		} else if *verbose {
			fmt.Fprintf(os.Stderr, "Callback forwarded successfully\n")
		}

		// Cleanup session
		if *cleanup {
			req, err := http.NewRequest(http.MethodDelete, deleteURL, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create cleanup request: %v\n", err)
			} else {
				resp, err := client.Do(req)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to cleanup session: %v\n", err)
				} else {
					resp.Body.Close()
				}
			}
		}

		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	fmt.Fprintln(os.Stderr, "Timeout waiting for OAuth callback")
	os.Exit(1)
}

// flagWasSet returns true if the named flag was explicitly passed on the command line.
func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// forwardCallback makes an HTTP request to the local port with the captured callback data.
func forwardCallback(client *http.Client, port int, data *CallbackData) error {
	if data == nil {
		return fmt.Errorf("no callback data")
	}

	// Build the local URL
	localURL := fmt.Sprintf("http://localhost:%d%s", port, data.Path)
	if data.Query != "" {
		localURL += "?" + data.Query
	}

	var body io.Reader
	if data.Body != "" {
		body = strings.NewReader(data.Body)
	}

	req, err := http.NewRequest(data.Method, localURL, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set captured headers
	for k, v := range data.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to forward request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("local server returned status %d", resp.StatusCode)
	}

	return nil
}
```

Key changes from original `callback-forwarder/main.go`:
- Removed `// Package main` doc comment (not a package, just a standalone file)
- Added `strconv` import
- Added `flagWasSet()` helper
- Added `CB_FORWARDER_TIMEOUT`, `CB_FORWARDER_POLL_INTERVAL`, `CB_FORWARDER_CLEANUP` env var fallbacks after `flag.Parse()`
- Added consecutive error tracking (`consecutiveErrors` counter, warns after 3)
- Fixed exit code: `os.Exit(1)` on forward failure instead of always `os.Exit(0)`

**Step 2: Verify it compiles standalone**

Run: `cd /tmp && cp /Users/andrew/Code/clawker/internal/build/templates/callback-forwarder.go . && go build -o /dev/null callback-forwarder.go && echo "OK"`
Expected: `OK`

**Step 3: Commit**

```bash
git add internal/build/templates/callback-forwarder.go
git commit -m "feat: add callback-forwarder Go source to templates

Standalone Go source for callback-forwarder binary, mirroring
ssh-agent-proxy.go pattern. Fixes behavioral gaps vs shell script:
env var fallbacks, exit code on forward failure, error tracking."
```

---

### Task 2: Update dockerfile.go embed + build context

**Files:**
- Modify: `internal/build/dockerfile.go:41-42` (embed declaration)
- Modify: `internal/build/dockerfile.go:168` (GenerateDockerfiles script list)
- Modify: `internal/build/dockerfile.go:335-338` (GenerateBuildContextFromDockerfile tar)
- Modify: `internal/build/dockerfile.go:394` (WriteBuildContextToDir script list)

**Step 1: Update embed declaration**

Change lines 41-42 from:
```go
//go:embed templates/callback-forwarder.sh
var CallbackForwarderScript string
```
To:
```go
//go:embed templates/callback-forwarder.go
var CallbackForwarderSource string
```

**Step 2: Update GenerateDockerfiles script list**

Change line 168 from:
```go
{"callback-forwarder.sh", CallbackForwarderScript, 0755},
```
To:
```go
{"callback-forwarder.go", CallbackForwarderSource, 0644},
```

**Step 3: Update GenerateBuildContextFromDockerfile tar addition**

Change lines 335-338 from:
```go
// Add callback-forwarder script for OAuth callback proxying
if err := addFileToTar(tw, "callback-forwarder.sh", []byte(CallbackForwarderScript)); err != nil {
    return nil, err
}
```
To:
```go
// Add callback-forwarder Go source for compilation in multi-stage build
if err := addFileToTar(tw, "callback-forwarder.go", []byte(CallbackForwarderSource)); err != nil {
    return nil, err
}
```

**Step 4: Update WriteBuildContextToDir script list**

Change line 394 from:
```go
{"callback-forwarder.sh", CallbackForwarderScript, 0755},
```
To:
```go
{"callback-forwarder.go", CallbackForwarderSource, 0644},
```

**Step 5: Verify it compiles**

Run: `go build ./internal/build/...`
Expected: Success (no errors)

**Step 6: Commit**

```bash
git add internal/build/dockerfile.go
git commit -m "refactor: embed callback-forwarder.go source instead of .sh script

Update go:embed and all three build context generation paths
(GenerateDockerfiles, GenerateBuildContextFromDockerfile,
WriteBuildContextToDir) to use Go source with 0644 permissions."
```

---

### Task 3: Update Dockerfile template

**Files:**
- Modify: `internal/build/templates/Dockerfile.tmpl:4-13` (add builder stage)
- Modify: `internal/build/templates/Dockerfile.tmpl:231` (change COPY)

**Step 1: Add callback-forwarder builder stage**

After the existing ssh-proxy-builder block (after line 13, before `FROM {{.BaseImage}}`), insert:

```dockerfile

# Builder stage for callback-forwarder
FROM golang:1.23-alpine AS callback-forwarder-builder
WORKDIR /build
COPY callback-forwarder.go .
{{- if .BuildKitEnabled}}
RUN --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o callback-forwarder callback-forwarder.go
{{- else}}
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o callback-forwarder callback-forwarder.go
{{- end}}
```

**Step 2: Change COPY line for callback-forwarder**

Change line 231 from:
```dockerfile
COPY callback-forwarder.sh /usr/local/bin/callback-forwarder
```
To:
```dockerfile
COPY --from=callback-forwarder-builder /build/callback-forwarder /usr/local/bin/callback-forwarder
```

The existing `chmod +x` on line 233 stays (harmless on binary, keeps it grouped with host-open and git-credential).

**Step 3: Verify template renders**

Run: `go build ./internal/build/...`
Expected: Success

**Step 4: Commit**

```bash
git add internal/build/templates/Dockerfile.tmpl
git commit -m "feat: compile callback-forwarder Go binary in Dockerfile

Add multi-stage builder (mirrors ssh-agent-proxy pattern).
COPY compiled binary from builder stage instead of shell script."
```

---

### Task 4: Update test harness for Go source compilation

**Files:**
- Modify: `test/harness/client.go:279` (BuildLightImage file filter)
- Modify: `test/harness/client.go:353` (generateLightDockerfile signature + builder stages)
- Modify: `test/harness/client.go:374` (createLightBuildContext signature + Go sources)

**Step 1: Update BuildLightImage to collect .go files**

In `BuildLightImage`, change the file-reading loop (~line 276-289) from only collecting `.sh` files to also collecting `.go` files. Track them separately:

```go
var allScripts []string  // .sh files
var goSources []string   // .go files
scriptContents := make(map[string][]byte)
for _, entry := range entries {
    if entry.IsDir() {
        continue
    }
    name := entry.Name()
    ext := filepath.Ext(name)
    if ext != ".sh" && ext != ".go" {
        continue
    }
    content, err := os.ReadFile(filepath.Join(scriptsDir, name))
    if err != nil {
        t.Fatalf("BuildLightImage: failed to read %s: %v", name, err)
    }
    scriptContents[name] = content
    if ext == ".sh" {
        allScripts = append(allScripts, name)
    } else {
        goSources = append(goSources, name)
    }
}
```

Update the hash computation to include Go sources:
```go
for _, name := range goSources {
    hasher.Write([]byte(name))
    hasher.Write(scriptContents[name])
}
```

Update the calls to `generateLightDockerfile` and `createLightBuildContext` to pass `goSources`.

**Step 2: Update generateLightDockerfile for multi-stage**

Change signature to accept Go sources:
```go
func generateLightDockerfile(scripts []string, goSources []string) string
```

Add Go builder stages before `FROM alpine:3.21`:
```go
// Add Go builder stages for each .go source
for _, goFile := range goSources {
    binaryName := strings.TrimSuffix(goFile, ".go")
    stageName := binaryName + "-builder"
    fmt.Fprintf(&sb, "FROM golang:1.23-alpine AS %s\n", stageName)
    sb.WriteString("WORKDIR /build\n")
    fmt.Fprintf(&sb, "COPY %s .\n", goFile)
    fmt.Fprintf(&sb, "RUN CGO_ENABLED=0 go build -ldflags=\"-s -w\" -o %s %s\n\n", binaryName, goFile)
}
```

After the scripts COPY block, add binary COPY:
```go
for _, goFile := range goSources {
    binaryName := strings.TrimSuffix(goFile, ".go")
    stageName := binaryName + "-builder"
    fmt.Fprintf(&sb, "COPY --from=%s /build/%s /usr/local/bin/%s\n", stageName, binaryName, binaryName)
}
if len(goSources) > 0 {
    sb.WriteString("RUN chmod +x")
    for _, goFile := range goSources {
        fmt.Fprintf(&sb, " /usr/local/bin/%s", strings.TrimSuffix(goFile, ".go"))
    }
    sb.WriteString("\n")
}
```

**Step 3: Update createLightBuildContext**

Change signature:
```go
func createLightBuildContext(dockerfile string, scripts []string, goSources []string, contents map[string][]byte) (io.Reader, error)
```

Add Go sources at tar root (not under `scripts/`):
```go
// Add Go sources at root level (for builder stage COPY)
for _, name := range goSources {
    if err := addTarFile(tw, name, contents[name]); err != nil {
        return nil, err
    }
}
```

**Step 4: Verify it compiles**

Run: `go build ./test/harness/...`
Expected: Success

**Step 5: Commit**

```bash
git add test/harness/client.go
git commit -m "feat: BuildLightImage supports Go source multi-stage builds

Collect .go files from templates/, add golang builder stages to
light Dockerfile, copy compiled binaries into final image.
Mirrors production Dockerfile.tmpl pattern."
```

---

### Task 5: Update integration tests

**Files:**
- Modify: `test/internals/scripts_test.go:336,369,402,417` (invocation paths + env vars)
- Modify: `test/internals/scripts_test.go:367-368,415-416` (env var names)

**Step 1: Update TestCallbackForwarder_PollsProxy**

Line 369 — change invocation from `.sh` to binary and update env vars:
```go
forwarderScript := `
    CLAWKER_HOST_PROXY="` + proxyURL + `" \
    CALLBACK_SESSION="` + sessionID + `" \
    CALLBACK_PORT=8080 \
    CB_FORWARDER_TIMEOUT=10 \
    CB_FORWARDER_POLL_INTERVAL=1 \
    /usr/local/bin/callback-forwarder -v 2>&1 || echo "forwarder exit code: $?"
`
```

**Step 2: Update TestCallbackForwarder_Timeout**

Lines 411-418 — change invocation and env vars:
```go
forwarderScript := `
    CLAWKER_HOST_PROXY="` + proxyURL + `" \
    CALLBACK_SESSION="` + sessionID + `" \
    CALLBACK_PORT=8080 \
    CB_FORWARDER_TIMEOUT=3 \
    CB_FORWARDER_POLL_INTERVAL=1 \
    /usr/local/bin/callback-forwarder 2>&1
    echo "exit_code=$?"
`
```

**Step 3: Commit**

```bash
git add test/internals/scripts_test.go
git commit -m "test: update callback-forwarder tests for Go binary

Use /usr/local/bin/callback-forwarder (no .sh extension).
Use CB_FORWARDER_TIMEOUT and CB_FORWARDER_POLL_INTERVAL env vars."
```

---

### Task 6: Update build unit test

**Files:**
- Modify: `internal/build/build_test.go:46`

**Step 1: Update expected file name**

Change line 46 from:
```go
"callback-forwarder.sh",
```
To:
```go
"callback-forwarder.go",
```

**Step 2: Run unit tests**

Run: `go test ./internal/build/... -v -run TestWriteBuildContext`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/build/build_test.go
git commit -m "test: expect callback-forwarder.go in build context"
```

---

### Task 7: Delete old files

**Files:**
- Delete: `internal/build/templates/callback-forwarder.sh`
- Delete: `internal/build/callback-forwarder/main.go` (and directory)

**Step 1: Delete the shell script**

```bash
rm internal/build/templates/callback-forwarder.sh
```

**Step 2: Delete the old package directory**

```bash
rm -rf internal/build/callback-forwarder/
```

**Step 3: Verify everything compiles**

Run: `go build ./...`
Expected: Success (nothing references deleted files)

**Step 4: Run all unit tests**

Run: `make test`
Expected: All pass

**Step 5: Commit**

```bash
git add -A internal/build/templates/callback-forwarder.sh internal/build/callback-forwarder/
git commit -m "chore: remove callback-forwarder shell script and old package

Replaced by templates/callback-forwarder.go compiled in Dockerfile."
```

---

### Task 8: Update documentation

**Files:**
- Modify: `internal/build/CLAUDE.md`

**Step 1: Update CLAUDE.md**

- Remove `callback-forwarder/` from the Subpackages table
- In the embedded assets list, change `CallbackForwarderScript` to `CallbackForwarderSource`
- Update any reference to `callback-forwarder.sh` to describe it as a compiled Go binary

**Step 2: Commit**

```bash
git add internal/build/CLAUDE.md
git commit -m "docs: update CLAUDE.md for callback-forwarder Go binary"
```

---

### Task 9: Run integration tests

**Step 1: Run callback-forwarder integration tests**

Run: `go test ./test/internals/... -run TestCallbackForwarder -v -timeout 5m`
Expected: Both `TestCallbackForwarder_PollsProxy` and `TestCallbackForwarder_Timeout` PASS

**Step 2: Run full unit test suite**

Run: `make test`
Expected: All pass

**Step 3: Run whail integration tests (if Docker + BuildKit available)**

Run: `go test ./test/whail/... -v -timeout 5m`
Expected: All pass (verifies Dockerfile template renders correctly with new builder stage)

---

## Files Summary

| File | Action |
|------|--------|
| `internal/build/templates/callback-forwarder.go` | **Create** — fixed Go source |
| `internal/build/dockerfile.go` | **Modify** — embed + 3 build context points |
| `internal/build/templates/Dockerfile.tmpl` | **Modify** — add builder stage, change COPY |
| `test/harness/client.go` | **Modify** — multi-stage support in BuildLightImage |
| `test/internals/scripts_test.go` | **Modify** — binary paths + env var names |
| `internal/build/build_test.go` | **Modify** — expected filename |
| `internal/build/templates/callback-forwarder.sh` | **Delete** |
| `internal/build/callback-forwarder/` | **Delete** directory |
| `internal/build/CLAUDE.md` | **Modify** — docs |

## No Changes Needed

- `host-open.sh` — already calls `callback-forwarder` by name (no `.sh` extension)
- `internal/build/hash.go` — content hash changes automatically (correct behavior)
- `Makefile` — no build targets needed (Go source embedded via `//go:embed`)
