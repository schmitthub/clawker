package whailtest

import (
	"fmt"

	"github.com/schmitthub/clawker/pkg/whail"
)

// StepDigest returns a deterministic sha256 digest string for step n.
// Used by build scenarios to generate realistic step IDs.
func StepDigest(n int) string {
	return fmt.Sprintf("sha256:%064d", n)
}

// SimpleBuildEvents returns a basic 3-visible-step build sequence with 2
// internal (hidden) steps. Mirrors a minimal Dockerfile: FROM + RUN + COPY.
func SimpleBuildEvents() []whail.BuildProgressEvent {
	return []whail.BuildProgressEvent{
		// Internal steps (filtered by IsInternalStep)
		{StepID: StepDigest(1), StepName: "[internal] load build definition from Dockerfile", Status: whail.BuildStepComplete},
		{StepID: StepDigest(2), StepName: "[internal] load .dockerignore", Status: whail.BuildStepComplete},
		// Visible steps
		{StepID: StepDigest(3), StepName: "[stage-0 1/3] FROM node:20-slim", Status: whail.BuildStepRunning},
		{StepID: StepDigest(3), StepName: "[stage-0 1/3] FROM node:20-slim", Status: whail.BuildStepComplete},
		{StepID: StepDigest(4), StepName: "[stage-0 2/3] RUN apt-get update && apt-get install -y git", Status: whail.BuildStepRunning},
		{StepID: StepDigest(4), StepName: "[stage-0 2/3] RUN apt-get update && apt-get install -y git", LogLine: "Reading package lists..."},
		{StepID: StepDigest(4), StepName: "[stage-0 2/3] RUN apt-get update && apt-get install -y git", Status: whail.BuildStepComplete},
		{StepID: StepDigest(5), StepName: "[stage-0 3/3] COPY . /app", Status: whail.BuildStepRunning},
		{StepID: StepDigest(5), StepName: "[stage-0 3/3] COPY . /app", Status: whail.BuildStepComplete},
	}
}

// CachedBuildEvents returns a 5-visible-step build where 4 steps are cached.
// Only the final COPY is re-executed (typical incremental rebuild).
func CachedBuildEvents() []whail.BuildProgressEvent {
	return []whail.BuildProgressEvent{
		{StepID: StepDigest(1), StepName: "[internal] load build definition from Dockerfile", Status: whail.BuildStepComplete},
		{StepID: StepDigest(2), StepName: "[internal] load .dockerignore", Status: whail.BuildStepComplete},
		{StepID: StepDigest(3), StepName: "[stage-0 1/5] FROM node:20-slim", Status: whail.BuildStepCached, Cached: true},
		{StepID: StepDigest(4), StepName: "[stage-0 2/5] RUN apt-get update", Status: whail.BuildStepCached, Cached: true},
		{StepID: StepDigest(5), StepName: "[stage-0 3/5] RUN npm install -g pnpm", Status: whail.BuildStepCached, Cached: true},
		{StepID: StepDigest(6), StepName: "[stage-0 4/5] COPY package.json /app/", Status: whail.BuildStepCached, Cached: true},
		{StepID: StepDigest(7), StepName: "[stage-0 5/5] COPY . /app", Status: whail.BuildStepRunning},
		{StepID: StepDigest(7), StepName: "[stage-0 5/5] COPY . /app", Status: whail.BuildStepComplete},
	}
}

// MultiStageBuildEvents returns an 8-visible-step build across 3 named stages.
// Exercises stage grouping in the progress display.
func MultiStageBuildEvents() []whail.BuildProgressEvent {
	return []whail.BuildProgressEvent{
		{StepID: StepDigest(1), StepName: "[internal] load build definition from Dockerfile", Status: whail.BuildStepComplete},
		{StepID: StepDigest(2), StepName: "[internal] load metadata for docker.io/library/golang:1.22", Status: whail.BuildStepComplete},
		// Stage: builder
		{StepID: StepDigest(3), StepName: "[builder 1/4] FROM golang:1.22 AS builder", Status: whail.BuildStepRunning},
		{StepID: StepDigest(3), StepName: "[builder 1/4] FROM golang:1.22 AS builder", Status: whail.BuildStepComplete},
		{StepID: StepDigest(4), StepName: "[builder 2/4] COPY go.mod go.sum /src/", Status: whail.BuildStepRunning},
		{StepID: StepDigest(4), StepName: "[builder 2/4] COPY go.mod go.sum /src/", Status: whail.BuildStepComplete},
		{StepID: StepDigest(5), StepName: "[builder 3/4] RUN go mod download", Status: whail.BuildStepRunning},
		{StepID: StepDigest(5), StepName: "[builder 3/4] RUN go mod download", LogLine: "go: downloading github.com/example/lib v1.0.0"},
		{StepID: StepDigest(5), StepName: "[builder 3/4] RUN go mod download", Status: whail.BuildStepComplete},
		{StepID: StepDigest(6), StepName: "[builder 4/4] RUN go build -o /bin/app", Status: whail.BuildStepRunning},
		{StepID: StepDigest(6), StepName: "[builder 4/4] RUN go build -o /bin/app", Status: whail.BuildStepComplete},
		// Stage: assets
		{StepID: StepDigest(7), StepName: "[assets 1/2] FROM node:20-slim AS assets", Status: whail.BuildStepRunning},
		{StepID: StepDigest(7), StepName: "[assets 1/2] FROM node:20-slim AS assets", Status: whail.BuildStepComplete},
		{StepID: StepDigest(8), StepName: "[assets 2/2] RUN npm run build", Status: whail.BuildStepRunning},
		{StepID: StepDigest(8), StepName: "[assets 2/2] RUN npm run build", Status: whail.BuildStepComplete},
		// Stage: runtime
		{StepID: StepDigest(9), StepName: "[runtime 1/2] FROM alpine:3.19 AS runtime", Status: whail.BuildStepRunning},
		{StepID: StepDigest(9), StepName: "[runtime 1/2] FROM alpine:3.19 AS runtime", Status: whail.BuildStepComplete},
		{StepID: StepDigest(10), StepName: "[runtime 2/2] COPY --from=builder /bin/app /usr/local/bin/", Status: whail.BuildStepRunning},
		{StepID: StepDigest(10), StepName: "[runtime 2/2] COPY --from=builder /bin/app /usr/local/bin/", Status: whail.BuildStepComplete},
	}
}

// ErrorBuildEvents returns a 3-visible-step build where the last step fails.
// The error step includes a log line with the failure output.
func ErrorBuildEvents() []whail.BuildProgressEvent {
	return []whail.BuildProgressEvent{
		{StepID: StepDigest(1), StepName: "[internal] load build definition from Dockerfile", Status: whail.BuildStepComplete},
		{StepID: StepDigest(2), StepName: "[stage-0 1/3] FROM node:20-slim", Status: whail.BuildStepCached, Cached: true},
		{StepID: StepDigest(3), StepName: "[stage-0 2/3] COPY package.json /app/", Status: whail.BuildStepRunning},
		{StepID: StepDigest(3), StepName: "[stage-0 2/3] COPY package.json /app/", Status: whail.BuildStepComplete},
		{StepID: StepDigest(4), StepName: "[stage-0 3/3] RUN npm install", Status: whail.BuildStepRunning},
		{StepID: StepDigest(4), StepName: "[stage-0 3/3] RUN npm install", LogLine: "npm ERR! code ERESOLVE"},
		{StepID: StepDigest(4), StepName: "[stage-0 3/3] RUN npm install", LogLine: "npm ERR! ERESOLVE unable to resolve dependency tree"},
		{StepID: StepDigest(4), StepName: "[stage-0 3/3] RUN npm install", Status: whail.BuildStepError, Error: "process \"npm install\" did not complete successfully: exit code: 1"},
	}
}

// LargeLogOutputEvents returns a single-step build that emits 50 log lines.
// Exercises viewport overflow and log scrolling in the progress display.
func LargeLogOutputEvents() []whail.BuildProgressEvent {
	events := []whail.BuildProgressEvent{
		{StepID: StepDigest(1), StepName: "[internal] load build definition from Dockerfile", Status: whail.BuildStepComplete},
		{StepID: StepDigest(2), StepName: "[stage-0 1/1] RUN make build", Status: whail.BuildStepRunning},
	}
	for i := 1; i <= 50; i++ {
		events = append(events, whail.BuildProgressEvent{
			StepID:   StepDigest(2),
			StepName: "[stage-0 1/1] RUN make build",
			LogLine:  fmt.Sprintf("[%d/50] Compiling source file_%d.go", i, i),
		})
	}
	events = append(events, whail.BuildProgressEvent{
		StepID:   StepDigest(2),
		StepName: "[stage-0 1/1] RUN make build",
		Status:   whail.BuildStepComplete,
	})
	return events
}

// ManyStepsBuildEvents returns a 10-visible-step build that exercises
// the per-stage child window (MaxVisible is typically 5).
func ManyStepsBuildEvents() []whail.BuildProgressEvent {
	events := []whail.BuildProgressEvent{
		{StepID: StepDigest(1), StepName: "[internal] load build definition from Dockerfile", Status: whail.BuildStepComplete},
		{StepID: StepDigest(2), StepName: "[internal] load .dockerignore", Status: whail.BuildStepComplete},
	}
	for i := 1; i <= 10; i++ {
		id := StepDigest(i + 2)
		name := fmt.Sprintf("[stage-0 %d/10] RUN step_%d", i, i)
		events = append(events,
			whail.BuildProgressEvent{StepID: id, StepName: name, Status: whail.BuildStepRunning},
			whail.BuildProgressEvent{StepID: id, StepName: name, Status: whail.BuildStepComplete},
		)
	}
	return events
}

// InternalOnlyEvents returns a build with 3 internal steps and zero visible steps.
// Edge case: the progress display should handle this gracefully.
func InternalOnlyEvents() []whail.BuildProgressEvent {
	return []whail.BuildProgressEvent{
		{StepID: StepDigest(1), StepName: "[internal] load build definition from Dockerfile", Status: whail.BuildStepComplete},
		{StepID: StepDigest(2), StepName: "[internal] load .dockerignore", Status: whail.BuildStepComplete},
		{StepID: StepDigest(3), StepName: "[internal] load metadata for docker.io/library/node:20-slim", Status: whail.BuildStepComplete},
	}
}

// BuildScenario pairs a name with a pre-built event sequence.
type BuildScenario struct {
	Name   string
	Events []whail.BuildProgressEvent
}

// AllBuildScenarios returns all pre-built build scenarios.
func AllBuildScenarios() []BuildScenario {
	return []BuildScenario{
		{Name: "simple", Events: SimpleBuildEvents()},
		{Name: "cached", Events: CachedBuildEvents()},
		{Name: "multi-stage", Events: MultiStageBuildEvents()},
		{Name: "error", Events: ErrorBuildEvents()},
		{Name: "large-log", Events: LargeLogOutputEvents()},
		{Name: "many-steps", Events: ManyStepsBuildEvents()},
		{Name: "internal-only", Events: InternalOnlyEvents()},
	}
}
