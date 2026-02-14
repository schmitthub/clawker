package shared

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoopStatusInstructions_ContainsBlockMarkers(t *testing.T) {
	// The system prompt must contain the exact markers that ParseStatus expects
	assert.Contains(t, LoopStatusInstructions, "---LOOP_STATUS---")
	assert.Contains(t, LoopStatusInstructions, "---END_LOOP_STATUS---")
}

func TestLoopStatusInstructions_ContainsAllFields(t *testing.T) {
	// Every field that ParseStatus recognizes must be documented in the prompt
	fields := []string{
		"STATUS:",
		"TASKS_COMPLETED_THIS_LOOP:",
		"FILES_MODIFIED:",
		"TESTS_STATUS:",
		"WORK_TYPE:",
		"EXIT_SIGNAL:",
		"RECOMMENDATION:",
	}
	for _, field := range fields {
		assert.Contains(t, LoopStatusInstructions, field, "system prompt must mention field %q", field)
	}
}

func TestLoopStatusInstructions_ContainsStatusValues(t *testing.T) {
	// The prompt must list the valid status values
	assert.Contains(t, LoopStatusInstructions, StatusPending)
	assert.Contains(t, LoopStatusInstructions, StatusComplete)
	assert.Contains(t, LoopStatusInstructions, StatusBlocked)
}

func TestLoopStatusInstructions_ContainsWorkTypes(t *testing.T) {
	assert.Contains(t, LoopStatusInstructions, WorkTypeImplementation)
	assert.Contains(t, LoopStatusInstructions, WorkTypeTesting)
	assert.Contains(t, LoopStatusInstructions, WorkTypeDocumentation)
	assert.Contains(t, LoopStatusInstructions, WorkTypeRefactoring)
}

func TestLoopStatusInstructions_ExampleIsParseable(t *testing.T) {
	// Extract the example block from the system prompt and verify ParseStatus can parse it
	// The prompt contains a LOOP_STATUS example — it must be parseable
	status := ParseStatus(LoopStatusInstructions)
	require.NotNil(t, status, "the example LOOP_STATUS block in the system prompt must be parseable by ParseStatus")
}

func TestBuildSystemPrompt_DefaultOnly(t *testing.T) {
	got := BuildSystemPrompt("")
	assert.Equal(t, LoopStatusInstructions, got)
}

func TestBuildSystemPrompt_WithAdditional(t *testing.T) {
	got := BuildSystemPrompt("Always run tests before marking complete.")
	assert.True(t, strings.HasPrefix(got, LoopStatusInstructions))
	assert.Contains(t, got, "Always run tests before marking complete.")
}

func TestBuildSystemPrompt_TrimsWhitespace(t *testing.T) {
	got := BuildSystemPrompt("  Extra instructions  \n\n")
	assert.Contains(t, got, "Extra instructions")
	// Should not have trailing whitespace after the additional instructions
	assert.False(t, strings.HasSuffix(got, "\n\n"))
}

func TestBuildSystemPrompt_SeparatesWithNewlines(t *testing.T) {
	got := BuildSystemPrompt("Additional context.")
	// The default and additional should be separated by a blank line
	assert.Contains(t, got, LoopStatusInstructions+"\n\n"+"Additional context.")
}
