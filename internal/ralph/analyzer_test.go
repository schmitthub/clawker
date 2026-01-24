package ralph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *Status
	}{
		{
			name: "complete status block",
			input: `Some output before
---RALPH_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 3
FILES_MODIFIED: 5
TESTS_STATUS: PASSING
WORK_TYPE: IMPLEMENTATION
EXIT_SIGNAL: false
RECOMMENDATION: Continue with refactoring
---END_RALPH_STATUS---
Some output after`,
			expected: &Status{
				Status:         "IN_PROGRESS",
				TasksCompleted: 3,
				FilesModified:  5,
				TestsStatus:    "PASSING",
				WorkType:       "IMPLEMENTATION",
				ExitSignal:     false,
				Recommendation: "Continue with refactoring",
			},
		},
		{
			name: "exit signal true",
			input: `---RALPH_STATUS---
STATUS: COMPLETE
EXIT_SIGNAL: true
RECOMMENDATION: All tasks completed
---END_RALPH_STATUS---`,
			expected: &Status{
				Status:         "COMPLETE",
				ExitSignal:     true,
				Recommendation: "All tasks completed",
			},
		},
		{
			name: "blocked status",
			input: `---RALPH_STATUS---
STATUS: BLOCKED
RECOMMENDATION: Need clarification on requirements
---END_RALPH_STATUS---`,
			expected: &Status{
				Status:         "BLOCKED",
				Recommendation: "Need clarification on requirements",
			},
		},
		{
			name:     "no status block",
			input:    "Regular output without any status block",
			expected: nil,
		},
		{
			name:     "incomplete status block",
			input:    "---RALPH_STATUS---\nSTATUS: IN_PROGRESS\n",
			expected: nil,
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name: "invalid numeric values parsed as zero",
			input: `---RALPH_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: not_a_number
FILES_MODIFIED: xyz
---END_RALPH_STATUS---`,
			expected: &Status{
				Status:         "IN_PROGRESS",
				TasksCompleted: 0, // Invalid value parsed as 0
				FilesModified:  0, // Invalid value parsed as 0
			},
		},
		{
			name: "exit signal case variations - TRUE",
			input: `---RALPH_STATUS---
STATUS: COMPLETE
EXIT_SIGNAL: TRUE
---END_RALPH_STATUS---`,
			expected: &Status{
				Status:     "COMPLETE",
				ExitSignal: true,
			},
		},
		{
			name: "exit signal case variations - True",
			input: `---RALPH_STATUS---
STATUS: COMPLETE
EXIT_SIGNAL: True
---END_RALPH_STATUS---`,
			expected: &Status{
				Status:     "COMPLETE",
				ExitSignal: true,
			},
		},
		{
			name: "exit signal anything else is false",
			input: `---RALPH_STATUS---
STATUS: IN_PROGRESS
EXIT_SIGNAL: yes
---END_RALPH_STATUS---`,
			expected: &Status{
				Status:     "IN_PROGRESS",
				ExitSignal: false,
			},
		},
		{
			name: "multiple status blocks - first wins",
			input: `---RALPH_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 5
---END_RALPH_STATUS---
Some other output
---RALPH_STATUS---
STATUS: COMPLETE
TASKS_COMPLETED_THIS_LOOP: 10
---END_RALPH_STATUS---`,
			expected: &Status{
				Status:         "IN_PROGRESS",
				TasksCompleted: 5,
			},
		},
		{
			name: "whitespace handling in values",
			input: `---RALPH_STATUS---
STATUS:   BLOCKED
RECOMMENDATION:   Need more info
---END_RALPH_STATUS---`,
			expected: &Status{
				Status:         "BLOCKED",
				Recommendation: "Need more info",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseStatus(tt.input)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.expected.Status, result.Status)
				assert.Equal(t, tt.expected.TasksCompleted, result.TasksCompleted)
				assert.Equal(t, tt.expected.FilesModified, result.FilesModified)
				assert.Equal(t, tt.expected.TestsStatus, result.TestsStatus)
				assert.Equal(t, tt.expected.WorkType, result.WorkType)
				assert.Equal(t, tt.expected.ExitSignal, result.ExitSignal)
				assert.Equal(t, tt.expected.Recommendation, result.Recommendation)
			}
		})
	}
}

func TestStatus_IsComplete(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{
			name:     "complete status",
			status:   Status{Status: StatusComplete},
			expected: true,
		},
		{
			name:     "exit signal true",
			status:   Status{Status: StatusPending, ExitSignal: true},
			expected: true,
		},
		{
			name:     "in progress",
			status:   Status{Status: StatusPending},
			expected: false,
		},
		{
			name:     "blocked",
			status:   Status{Status: StatusBlocked},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.IsComplete())
		})
	}
}

func TestStatus_IsBlocked(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{
			name:     "blocked",
			status:   Status{Status: StatusBlocked},
			expected: true,
		},
		{
			name:     "not blocked",
			status:   Status{Status: StatusPending},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.IsBlocked())
		})
	}
}

func TestStatus_HasProgress(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{
			name:     "has tasks",
			status:   Status{TasksCompleted: 1},
			expected: true,
		},
		{
			name:     "has files",
			status:   Status{FilesModified: 1},
			expected: true,
		},
		{
			name:     "has both",
			status:   Status{TasksCompleted: 2, FilesModified: 3},
			expected: true,
		},
		{
			name:     "no progress",
			status:   Status{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.HasProgress())
		})
	}
}

func TestStatus_String(t *testing.T) {
	tests := []struct {
		name     string
		status   *Status
		expected string
	}{
		{
			name:     "nil status",
			status:   nil,
			expected: "no status",
		},
		{
			name:     "in progress only",
			status:   &Status{Status: "IN_PROGRESS"},
			expected: "IN_PROGRESS",
		},
		{
			name:     "with tasks and files",
			status:   &Status{Status: "IN_PROGRESS", TasksCompleted: 3, FilesModified: 5},
			expected: "IN_PROGRESS, 3 tasks, 5 files",
		},
		{
			name:     "with tests status",
			status:   &Status{Status: "COMPLETE", TestsStatus: "PASSING"},
			expected: "COMPLETE, tests: PASSING",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}
