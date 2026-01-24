package ralph

import (
	"regexp"
	"strconv"
	"strings"
)

// Status represents the parsed RALPH_STATUS block from Claude's output.
type Status struct {
	// Status is one of: IN_PROGRESS, COMPLETE, BLOCKED
	Status string

	// TasksCompleted is the number of tasks completed in this loop.
	TasksCompleted int

	// FilesModified is the number of files modified in this loop.
	FilesModified int

	// TestsStatus is one of: PASSING, FAILING, NOT_RUN
	TestsStatus string

	// WorkType describes the type of work done (IMPLEMENTATION, TESTING, etc.)
	WorkType string

	// ExitSignal indicates whether Claude requested to exit the loop.
	ExitSignal bool

	// Recommendation is a one-line recommendation for next steps.
	Recommendation string
}

// StatusPending indicates work is still in progress.
const StatusPending = "IN_PROGRESS"

// StatusComplete indicates the task is complete.
const StatusComplete = "COMPLETE"

// StatusBlocked indicates the agent is blocked.
const StatusBlocked = "BLOCKED"

// TestsPassing indicates tests are passing.
const TestsPassing = "PASSING"

// TestsFailing indicates tests are failing.
const TestsFailing = "FAILING"

// TestsNotRun indicates tests were not run.
const TestsNotRun = "NOT_RUN"

var (
	// Match the RALPH_STATUS block boundaries
	statusBlockRe = regexp.MustCompile(`(?s)---RALPH_STATUS---(.+?)---END_RALPH_STATUS---`)

	// Match individual fields within the block
	fieldRe = regexp.MustCompile(`(?m)^([A-Z_]+):\s*(.*)$`)
)

// ParseStatus extracts the RALPH_STATUS block from output and parses it.
// Returns nil if no valid status block is found.
func ParseStatus(output string) *Status {
	matches := statusBlockRe.FindStringSubmatch(output)
	if len(matches) < 2 {
		return nil
	}

	blockContent := matches[1]
	status := &Status{}

	fieldMatches := fieldRe.FindAllStringSubmatch(blockContent, -1)
	for _, match := range fieldMatches {
		if len(match) < 3 {
			continue
		}
		key := strings.TrimSpace(match[1])
		value := strings.TrimSpace(match[2])

		switch key {
		case "STATUS":
			status.Status = value
		case "TASKS_COMPLETED_THIS_LOOP":
			status.TasksCompleted, _ = strconv.Atoi(value)
		case "FILES_MODIFIED":
			status.FilesModified, _ = strconv.Atoi(value)
		case "TESTS_STATUS":
			status.TestsStatus = value
		case "WORK_TYPE":
			status.WorkType = value
		case "EXIT_SIGNAL":
			status.ExitSignal = strings.ToLower(value) == "true"
		case "RECOMMENDATION":
			status.Recommendation = value
		}
	}

	return status
}

// IsComplete returns true if the status indicates completion.
func (s *Status) IsComplete() bool {
	return s.Status == StatusComplete || s.ExitSignal
}

// IsBlocked returns true if the status indicates the agent is blocked.
func (s *Status) IsBlocked() bool {
	return s.Status == StatusBlocked
}

// HasProgress returns true if the status indicates meaningful progress.
func (s *Status) HasProgress() bool {
	return s.TasksCompleted > 0 || s.FilesModified > 0
}

// String returns a human-readable summary of the status.
func (s *Status) String() string {
	if s == nil {
		return "no status"
	}
	parts := []string{s.Status}
	if s.TasksCompleted > 0 {
		parts = append(parts, strconv.Itoa(s.TasksCompleted)+" tasks")
	}
	if s.FilesModified > 0 {
		parts = append(parts, strconv.Itoa(s.FilesModified)+" files")
	}
	if s.TestsStatus != "" {
		parts = append(parts, "tests: "+s.TestsStatus)
	}
	return strings.Join(parts, ", ")
}
