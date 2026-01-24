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

func TestStatus_IsTestOnly(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{
			name:     "testing work type",
			status:   Status{WorkType: WorkTypeTesting},
			expected: true,
		},
		{
			name:     "implementation work type",
			status:   Status{WorkType: WorkTypeImplementation},
			expected: false,
		},
		{
			name:     "empty work type",
			status:   Status{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.IsTestOnly())
		})
	}
}

func TestStatus_IsCompleteStrict(t *testing.T) {
	tests := []struct {
		name      string
		status    Status
		threshold int
		expected  bool
	}{
		{
			name:      "exit signal with enough indicators",
			status:    Status{ExitSignal: true, CompletionIndicators: 3},
			threshold: 2,
			expected:  true,
		},
		{
			name:      "exit signal with exact threshold",
			status:    Status{ExitSignal: true, CompletionIndicators: 2},
			threshold: 2,
			expected:  true,
		},
		{
			name:      "exit signal but not enough indicators",
			status:    Status{ExitSignal: true, CompletionIndicators: 1},
			threshold: 2,
			expected:  false,
		},
		{
			name:      "enough indicators but no exit signal",
			status:    Status{ExitSignal: false, CompletionIndicators: 5},
			threshold: 2,
			expected:  false,
		},
		{
			name:      "neither condition met",
			status:    Status{ExitSignal: false, CompletionIndicators: 0},
			threshold: 2,
			expected:  false,
		},
		{
			name:      "zero threshold uses default",
			status:    Status{ExitSignal: true, CompletionIndicators: 2},
			threshold: 0,
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.IsCompleteStrict(tt.threshold))
		})
	}
}

func TestCountCompletionIndicators(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected int
	}{
		{
			name:     "no indicators",
			output:   "Just some regular output",
			expected: 0,
		},
		{
			name:     "one indicator",
			output:   "All tasks complete and ready to go",
			expected: 1,
		},
		{
			name:     "multiple indicators",
			output:   "All tasks complete, project ready, work is done!",
			expected: 3,
		},
		{
			name:     "case insensitive",
			output:   "ALL TASKS COMPLETE and PROJECT READY",
			expected: 2,
		},
		{
			name:     "partial match doesn't count",
			output:   "Some tasks are still incomplete",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, CountCompletionIndicators(tt.output))
		})
	}
}

func TestDetectRateLimitError(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected bool
	}{
		{
			name:     "no rate limit",
			output:   "Regular output without any errors",
			expected: false,
		},
		{
			name:     "rate limit detected",
			output:   "Error: You have exceeded your rate limit",
			expected: true,
		},
		{
			name:     "usage limit detected",
			output:   "Usage limit exceeded, please wait",
			expected: true,
		},
		{
			name:     "5-hour limit",
			output:   "You've hit the 5-hour usage limit",
			expected: true,
		},
		{
			name:     "too many requests",
			output:   "Error: Too many requests, slow down",
			expected: true,
		},
		{
			name:     "quota exceeded",
			output:   "API quota exceeded for the current period",
			expected: true,
		},
		{
			name:     "case insensitive",
			output:   "RATE LIMIT ERROR",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, DetectRateLimitError(tt.output))
		})
	}
}

func TestExtractErrorSignature(t *testing.T) {
	tests := []struct {
		name          string
		output        string
		expectEmpty   bool
		expectSameAs  string // If non-empty, compare signature with this output
		expectDiffAs  string // If non-empty, signature should differ from this output
	}{
		{
			name:        "no error",
			output:      "Everything is fine",
			expectEmpty: true,
		},
		{
			name:        "simple error",
			output:      "Error: file not found",
			expectEmpty: false,
		},
		{
			name:        "exception pattern",
			output:      "Exception: null pointer",
			expectEmpty: false,
		},
		{
			name:        "failed pattern",
			output:      "Test failed: assertion error",
			expectEmpty: false,
		},
		{
			name:       "same error different line numbers",
			output:     "Error: file not found at line 123",
			expectSameAs: "Error: file not found at line 456",
		},
		{
			name:       "different errors",
			output:     "Error: file not found",
			expectDiffAs: "Error: permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig := ExtractErrorSignature(tt.output)
			if tt.expectEmpty {
				assert.Empty(t, sig)
			} else {
				assert.NotEmpty(t, sig)
			}

			if tt.expectSameAs != "" {
				otherSig := ExtractErrorSignature(tt.expectSameAs)
				assert.Equal(t, sig, otherSig, "signatures should match")
			}

			if tt.expectDiffAs != "" {
				otherSig := ExtractErrorSignature(tt.expectDiffAs)
				assert.NotEqual(t, sig, otherSig, "signatures should differ")
			}
		})
	}
}

func TestAnalyzeOutput(t *testing.T) {
	output := `Some output here
---RALPH_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 2
FILES_MODIFIED: 3
WORK_TYPE: TESTING
EXIT_SIGNAL: false
RECOMMENDATION: Continue testing
---END_RALPH_STATUS---
Error: test failed assertion
All tasks complete.`

	result := AnalyzeOutput(output)

	require.NotNil(t, result)
	require.NotNil(t, result.Status)
	assert.Equal(t, "IN_PROGRESS", result.Status.Status)
	assert.Equal(t, 2, result.Status.TasksCompleted)
	assert.False(t, result.RateLimitHit)
	assert.NotEmpty(t, result.ErrorSignature)
	assert.Greater(t, result.OutputSize, 0)
	assert.Equal(t, 1, result.CompletionCount) // "All tasks complete" matches
}
