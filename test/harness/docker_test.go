package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddTestLabels(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]string
		verify func(t *testing.T, result map[string]string)
	}{
		{
			name:  "nil input",
			input: nil,
			verify: func(t *testing.T, result map[string]string) {
				assert.NotNil(t, result)
				assert.Equal(t, TestLabelValue, result[TestLabel])
			},
		},
		{
			name:  "empty input",
			input: map[string]string{},
			verify: func(t *testing.T, result map[string]string) {
				assert.Len(t, result, 1)
				assert.Equal(t, TestLabelValue, result[TestLabel])
			},
		},
		{
			name:  "preserves existing labels",
			input: map[string]string{"existing": "value"},
			verify: func(t *testing.T, result map[string]string) {
				assert.Len(t, result, 2)
				assert.Equal(t, "value", result["existing"])
				assert.Equal(t, TestLabelValue, result[TestLabel])
			},
		},
		{
			name:  "does not mutate input",
			input: map[string]string{"key": "value"},
			verify: func(t *testing.T, result map[string]string) {
				// Original should not have test label
				// (We verify this outside the verify func)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalLen := 0
			if tt.input != nil {
				originalLen = len(tt.input)
			}

			result := AddTestLabels(tt.input)
			tt.verify(t, result)

			// Verify no mutation of original
			if tt.input != nil {
				assert.Len(t, tt.input, originalLen, "original map should not be modified")
			}
		})
	}
}

func TestAddClawkerLabels(t *testing.T) {
	result := AddClawkerLabels(nil, "myproject", "myagent", "TestAddClawkerLabels")

	assert.Equal(t, TestLabelValue, result[TestLabel])
	assert.Equal(t, "true", result[ClawkerManagedLabel])
	assert.Equal(t, "myproject", result[_blankCfg.LabelProject()])
	assert.Equal(t, "myagent", result[_blankCfg.LabelAgent()])
	assert.Equal(t, "TestAddClawkerLabels", result[LabelTestName])
}

func TestAddClawkerLabels_PreservesExisting(t *testing.T) {
	input := map[string]string{"custom": "label"}
	result := AddClawkerLabels(input, "proj", "agent", "TestAddClawkerLabels_PreservesExisting")

	assert.Equal(t, "label", result["custom"])
	assert.Equal(t, "true", result[ClawkerManagedLabel])
	assert.Equal(t, "TestAddClawkerLabels_PreservesExisting", result[LabelTestName])
}
