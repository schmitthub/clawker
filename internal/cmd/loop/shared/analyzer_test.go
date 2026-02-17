package shared

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
---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 3
FILES_MODIFIED: 5
TESTS_STATUS: PASSING
WORK_TYPE: IMPLEMENTATION
EXIT_SIGNAL: false
RECOMMENDATION: Continue with refactoring
---END_LOOP_STATUS---
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
			input: `---LOOP_STATUS---
STATUS: COMPLETE
EXIT_SIGNAL: true
RECOMMENDATION: All tasks completed
---END_LOOP_STATUS---`,
			expected: &Status{
				Status:         "COMPLETE",
				ExitSignal:     true,
				Recommendation: "All tasks completed",
			},
		},
		{
			name: "blocked status",
			input: `---LOOP_STATUS---
STATUS: BLOCKED
RECOMMENDATION: Need clarification on requirements
---END_LOOP_STATUS---`,
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
			input:    "---LOOP_STATUS---\nSTATUS: IN_PROGRESS\n",
			expected: nil,
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name: "invalid numeric values parsed as zero",
			input: `---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: not_a_number
FILES_MODIFIED: xyz
---END_LOOP_STATUS---`,
			expected: &Status{
				Status:         "IN_PROGRESS",
				TasksCompleted: 0, // Invalid value parsed as 0
				FilesModified:  0, // Invalid value parsed as 0
			},
		},
		{
			name: "exit signal case variations - TRUE",
			input: `---LOOP_STATUS---
STATUS: COMPLETE
EXIT_SIGNAL: TRUE
---END_LOOP_STATUS---`,
			expected: &Status{
				Status:     "COMPLETE",
				ExitSignal: true,
			},
		},
		{
			name: "exit signal case variations - True",
			input: `---LOOP_STATUS---
STATUS: COMPLETE
EXIT_SIGNAL: True
---END_LOOP_STATUS---`,
			expected: &Status{
				Status:     "COMPLETE",
				ExitSignal: true,
			},
		},
		{
			name: "exit signal anything else is false",
			input: `---LOOP_STATUS---
STATUS: IN_PROGRESS
EXIT_SIGNAL: yes
---END_LOOP_STATUS---`,
			expected: &Status{
				Status:     "IN_PROGRESS",
				ExitSignal: false,
			},
		},
		{
			name: "multiple status blocks - first wins",
			input: `---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 5
---END_LOOP_STATUS---
Some other output
---LOOP_STATUS---
STATUS: COMPLETE
TASKS_COMPLETED_THIS_LOOP: 10
---END_LOOP_STATUS---`,
			expected: &Status{
				Status:         "IN_PROGRESS",
				TasksCompleted: 5,
			},
		},
		{
			name: "whitespace handling in values",
			input: `---LOOP_STATUS---
STATUS:   BLOCKED
RECOMMENDATION:   Need more info
---END_LOOP_STATUS---`,
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
		name         string
		output       string
		expectEmpty  bool
		expectSameAs string // If non-empty, compare signature with this output
		expectDiffAs string // If non-empty, signature should differ from this output
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
			name:         "same error different line numbers",
			output:       "Error: file not found at line 123",
			expectSameAs: "Error: file not found at line 456",
		},
		{
			name:         "different errors",
			output:       "Error: file not found",
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
---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 2
FILES_MODIFIED: 3
WORK_TYPE: TESTING
EXIT_SIGNAL: false
RECOMMENDATION: Continue testing
---END_LOOP_STATUS---
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

// --- AnalyzeStreamResult tests ---

func TestAnalyzeStreamResult_BasicParsing(t *testing.T) {
	text := `I've fixed three tests and modified 2 files.

---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 3
FILES_MODIFIED: 2
TESTS_STATUS: PASSING
WORK_TYPE: IMPLEMENTATION
EXIT_SIGNAL: false
RECOMMENDATION: Continue with remaining tests
---END_LOOP_STATUS---`

	result := AnalyzeStreamResult(text, nil)

	require.NotNil(t, result)
	require.NotNil(t, result.Status)
	assert.Equal(t, "IN_PROGRESS", result.Status.Status)
	assert.Equal(t, 3, result.Status.TasksCompleted)
	assert.Equal(t, 2, result.Status.FilesModified)
	assert.Equal(t, "PASSING", result.Status.TestsStatus)
	assert.False(t, result.RateLimitHit)
	assert.Greater(t, result.OutputSize, 0)
}

func TestAnalyzeStreamResult_WithSuccessResult(t *testing.T) {
	text := `All tasks complete, project ready.

---LOOP_STATUS---
STATUS: COMPLETE
EXIT_SIGNAL: true
RECOMMENDATION: All done
---END_LOOP_STATUS---`

	resultEvent := &ResultEvent{
		Subtype:  ResultSubtypeSuccess,
		NumTurns: 5,
		IsError:  false,
		Result:   "Done",
	}

	analysis := AnalyzeStreamResult(text, resultEvent)

	require.NotNil(t, analysis)
	require.NotNil(t, analysis.Status)
	assert.True(t, analysis.Status.IsComplete())
	assert.False(t, analysis.RateLimitHit)
	assert.Equal(t, 3, analysis.CompletionCount) // "All tasks complete" + "project ready" + "all done" (from RECOMMENDATION)
}

func TestAnalyzeStreamResult_NilResultEvent(t *testing.T) {
	text := `---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 1
---END_LOOP_STATUS---`

	analysis := AnalyzeStreamResult(text, nil)

	require.NotNil(t, analysis)
	require.NotNil(t, analysis.Status)
	assert.Equal(t, "IN_PROGRESS", analysis.Status.Status)
	assert.False(t, analysis.RateLimitHit)
}

func TestAnalyzeStreamResult_RateLimitFromText(t *testing.T) {
	text := `Error: You have exceeded your rate limit. Please try again later.`

	analysis := AnalyzeStreamResult(text, nil)

	assert.True(t, analysis.RateLimitHit)
}

func TestAnalyzeStreamResult_RateLimitFromResultEvent(t *testing.T) {
	text := `Working on the task...

---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 0
---END_LOOP_STATUS---`

	resultEvent := &ResultEvent{
		Subtype: ResultSubtypeErrorMaxBudget,
		IsError: true,
	}

	analysis := AnalyzeStreamResult(text, resultEvent)

	assert.True(t, analysis.RateLimitHit)
}

func TestAnalyzeStreamResult_NoRateLimitForOtherErrors(t *testing.T) {
	text := `Working on the task...`

	resultEvent := &ResultEvent{
		Subtype: ResultSubtypeErrorDuringExecution,
		IsError: true,
	}

	analysis := AnalyzeStreamResult(text, resultEvent)

	assert.False(t, analysis.RateLimitHit)
}

func TestAnalyzeStreamResult_NoStatusBlock(t *testing.T) {
	text := `I made some changes but forgot to output the status block.`

	analysis := AnalyzeStreamResult(text, nil)

	require.NotNil(t, analysis)
	assert.Nil(t, analysis.Status)
	assert.Equal(t, len(text), analysis.OutputSize)
}

func TestAnalyzeStreamResult_ErrorSignatureExtracted(t *testing.T) {
	text := `Error: file not found
---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 0
EXIT_SIGNAL: false
---END_LOOP_STATUS---`

	analysis := AnalyzeStreamResult(text, nil)

	assert.NotEmpty(t, analysis.ErrorSignature)
}

func TestAnalyzeStreamResult_CompletionIndicators(t *testing.T) {
	text := `All tasks complete. The implementation is complete and the project is finished.

---LOOP_STATUS---
STATUS: COMPLETE
EXIT_SIGNAL: true
RECOMMENDATION: All done
---END_LOOP_STATUS---`

	analysis := AnalyzeStreamResult(text, nil)

	assert.Greater(t, analysis.CompletionCount, 0)
}

func TestAnalyzeStreamResult_CompatibleWithCircuitBreaker(t *testing.T) {
	// Verify that AnalyzeStreamResult produces AnalysisResult compatible with
	// the circuit breaker's UpdateWithAnalysis method
	text := `---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 1
FILES_MODIFIED: 2
WORK_TYPE: IMPLEMENTATION
EXIT_SIGNAL: false
---END_LOOP_STATUS---`

	analysis := AnalyzeStreamResult(text, &ResultEvent{
		Subtype: ResultSubtypeSuccess,
	})

	cb := NewCircuitBreaker(3)
	result := cb.UpdateWithAnalysis(analysis.Status, analysis)

	assert.False(t, result.Tripped)
	assert.False(t, result.IsComplete)
	assert.Equal(t, 0, cb.NoProgressCount()) // Had progress
}

func TestAnalyzeStreamResult_ResultEventTurnsAndCost(t *testing.T) {
	text := `---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 0
---END_LOOP_STATUS---`

	resultEvent := &ResultEvent{
		Subtype:      ResultSubtypeSuccess,
		NumTurns:     12,
		TotalCostUSD: 0.15,
		DurationMS:   30000,
	}

	analysis := AnalyzeStreamResult(text, resultEvent)

	// ResultEvent metadata is captured in the analysis
	assert.Equal(t, 12, analysis.NumTurns)
	assert.InDelta(t, 0.15, analysis.TotalCostUSD, 0.001)
	assert.Equal(t, 30000, analysis.DurationMS)
}
