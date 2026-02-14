package shared

import (
	"fmt"
	"io"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

// ResultOutput is the structured output for loop results.
type ResultOutput struct {
	LoopsCompleted      int    `json:"loops_completed"`
	ExitReason          string `json:"exit_reason"`
	Success             bool   `json:"success"`
	Error               string `json:"error,omitempty"`
	TotalTasksCompleted int    `json:"total_tasks_completed,omitempty"`
	TotalFilesModified  int    `json:"total_files_modified,omitempty"`
	FinalStatus         string `json:"final_status,omitempty"`
	RateLimitHit        bool   `json:"rate_limit_hit,omitempty"`
}

// NewResultOutput maps a Result into a ResultOutput for serialization.
func NewResultOutput(result *Result) *ResultOutput {
	out := &ResultOutput{
		LoopsCompleted: result.LoopsCompleted,
		ExitReason:     result.ExitReason,
		Success:        result.Error == nil,
		RateLimitHit:   result.RateLimitHit,
	}

	if result.Error != nil {
		out.Error = result.Error.Error()
	}

	if result.Session != nil {
		out.TotalTasksCompleted = result.Session.TotalTasksCompleted
		out.TotalFilesModified = result.Session.TotalFilesModified
	}

	if result.FinalStatus != nil {
		out.FinalStatus = result.FinalStatus.Status
	}

	return out
}

// WriteResult writes the loop result in the appropriate format.
func WriteResult(out, errOut io.Writer, result *Result, format *cmdutil.FormatFlags) error {
	output := NewResultOutput(result)

	if format.IsJSON() {
		return cmdutil.WriteJSON(out, output)
	}

	if format.Quiet {
		fmt.Fprintln(out, output.ExitReason)
		return nil
	}

	// Default: human-readable summary to stderr (data-free)
	// The Monitor already prints a detailed summary, so we only add
	// minimal context here for non-monitor scenarios.
	return nil
}
