package buildkit

import (
	"regexp"
	"strings"

	bkclient "github.com/moby/buildkit/client"
	digest "github.com/opencontainers/go-digest"
	"github.com/rs/zerolog/log"

	"github.com/schmitthub/clawker/pkg/whail"
)

// ansiPattern matches ANSI escape sequences for stripping from log output.
// Build tools may inject escape sequences to colorize or control cursor
// positioning — forwarding them raw would allow escape injection.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// drainProgress reads from the BuildKit status channel until it is closed.
// When suppress is true, only error-state vertexes are logged. Otherwise,
// vertex names and log lines are forwarded to zerolog at debug level.
func drainProgress(ch chan *bkclient.SolveStatus, suppress bool, onProgress whail.BuildProgressFunc) {
	logged := make(map[digest.Digest]bool)
	// Track state transitions for progress callbacks — BuildKit sends full-state
	// snapshots so we deduplicate status changes per vertex.
	lastStatus := make(map[digest.Digest]whail.BuildStepStatus)
	stepIndex := make(map[digest.Digest]int)
	nextIndex := 0

	for status := range ch {
		for _, v := range status.Vertexes {
			if v.Error != "" {
				name := v.Name
				if name == "" {
					name = v.Digest.String()
				}
				log.Warn().Str("vertex", name).Str("error", v.Error).Msg("buildkit vertex error")
				if onProgress != nil {
					idx, ok := stepIndex[v.Digest]
					if !ok {
						idx = nextIndex
						stepIndex[v.Digest] = idx
						nextIndex++
					}
					onProgress(whail.BuildProgressEvent{
						StepID:     v.Digest.String(),
						StepName:   name,
						StepIndex:  idx,
						TotalSteps: -1,
						Status:     whail.BuildStepError,
						Error:      v.Error,
					})
					lastStatus[v.Digest] = whail.BuildStepError
				}
				continue
			}
			if suppress && onProgress == nil {
				continue
			}

			if v.Name == "" {
				continue
			}

			// Assign stable index on first encounter.
			idx, seen := stepIndex[v.Digest]
			if !seen {
				idx = nextIndex
				stepIndex[v.Digest] = idx
				nextIndex++
			}

			// Determine new status from vertex state.
			var newStatus whail.BuildStepStatus
			switch {
			case v.Completed != nil && v.Cached:
				newStatus = whail.BuildStepCached
			case v.Completed != nil:
				newStatus = whail.BuildStepComplete
			case v.Started != nil:
				newStatus = whail.BuildStepRunning
			default:
				newStatus = whail.BuildStepPending
			}

			// Only emit on state transition.
			if prev, ok := lastStatus[v.Digest]; ok && prev == newStatus {
				continue
			}
			lastStatus[v.Digest] = newStatus

			// Zerolog fallback when no progress callback.
			if !suppress && onProgress == nil && !logged[v.Digest] && (v.Started != nil || v.Completed != nil) {
				logged[v.Digest] = true
				log.Debug().Str("vertex", v.Name).Msg("buildkit")
			}

			if onProgress != nil {
				onProgress(whail.BuildProgressEvent{
					StepID:     v.Digest.String(),
					StepName:   v.Name,
					StepIndex:  idx,
					TotalSteps: -1,
					Status:     newStatus,
					Cached:     v.Cached,
				})
			}
		}
		if suppress && onProgress == nil {
			continue
		}
		for _, l := range status.Logs {
			if !suppress && onProgress == nil {
				log.Debug().Str("vertex", l.Vertex.String()).Bytes("data", l.Data).Msg("buildkit log")
			}
			if onProgress != nil {
				// Split log data into lines for the callback.
				// Strip \r from lines: build tools (apt-get, pip, npm) use \r to
				// overwrite progress bars in-place. Keep only content after the last
				// \r, mimicking terminal rendering. Also handles CRLF line endings.
				lines := strings.Split(strings.TrimRight(string(l.Data), "\n\r"), "\n")
				for _, line := range lines {
					if line == "" {
						continue
					}
					if idx := strings.LastIndex(line, "\r"); idx >= 0 {
						line = line[idx+1:]
						if line == "" {
							continue
						}
					}
					// Strip ANSI escape sequences to prevent escape injection
					// from build tool output.
					line = ansiPattern.ReplaceAllString(line, "")
					if line == "" {
						continue
					}
					onProgress(whail.BuildProgressEvent{
						StepID:     l.Vertex.String(),
						StepName:   "",
						StepIndex:  -1,
						TotalSteps: -1,
						Status:     whail.BuildStepRunning,
						LogLine:    line,
					})
				}
			}
		}
	}
}
