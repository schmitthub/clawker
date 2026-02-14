package shared

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"

	"github.com/schmitthub/clawker/internal/logger"
)

// Status represents the parsed LOOP_STATUS block from Claude's output.
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

	// CompletionIndicators is the count of completion phrases found in output.
	CompletionIndicators int
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

// Work type constants.
const (
	WorkTypeImplementation = "IMPLEMENTATION"
	WorkTypeTesting        = "TESTING"
	WorkTypeDocumentation  = "DOCUMENTATION"
	WorkTypeRefactoring    = "REFACTORING"
)

var (
	// Match the LOOP_STATUS block boundaries
	statusBlockRe = regexp.MustCompile(`(?s)---LOOP_STATUS---(.+?)---END_LOOP_STATUS---`)

	// Match individual fields within the block
	fieldRe = regexp.MustCompile(`(?m)^([A-Z_]+):\s*(.*)$`)

	// Match Claude's rate limit error patterns
	rateLimitRe = regexp.MustCompile(`(?i)(rate.?limit|usage.?limit|5.?hour|too.?many.?requests|quota.?exceeded|api.?limit)`)

	// Completion indicator patterns
	completionPatterns = []string{
		"all tasks complete",
		"project ready",
		"work is done",
		"implementation complete",
		"no more work",
		"finished",
		"task complete",
		"all done",
		"nothing left to do",
		"completed successfully",
	}

	// Error patterns to extract for signature
	errorPatternRe = regexp.MustCompile(`(?i)(error|exception|failed|failure|cannot|unable|refused|denied|timeout|crash)[\s:]+([^\n]{0,100})`)

	// Normalization regexes for error message comparison
	lineNumRe   = regexp.MustCompile(`:\d+|line\s+\d+`)
	timestampRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}|\d{2}:\d{2}:\d{2}`)
	addrRe      = regexp.MustCompile(`0x[0-9a-fA-F]+`)
)

// ParseStatus extracts the LOOP_STATUS block from output and parses it.
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
			if val, err := strconv.Atoi(value); err != nil {
				logger.Debug().Str("value", value).Msg("failed to parse TASKS_COMPLETED_THIS_LOOP, defaulting to 0")
			} else {
				status.TasksCompleted = val
			}
		case "FILES_MODIFIED":
			if val, err := strconv.Atoi(value); err != nil {
				logger.Debug().Str("value", value).Msg("failed to parse FILES_MODIFIED, defaulting to 0")
			} else {
				status.FilesModified = val
			}
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

	// Count completion indicators in the full output
	status.CompletionIndicators = CountCompletionIndicators(output)

	return status
}

// IsComplete returns true if the status indicates completion.
// This is the basic check: STATUS: COMPLETE or EXIT_SIGNAL: true
func (s *Status) IsComplete() bool {
	return s.Status == StatusComplete || s.ExitSignal
}

// IsCompleteStrict returns true only if BOTH conditions are met:
// - EXIT_SIGNAL is true
// - CompletionIndicators >= threshold
// This prevents premature exit due to false positives.
func (s *Status) IsCompleteStrict(threshold int) bool {
	if threshold <= 0 {
		threshold = DefaultCompletionThreshold
	}
	return s.ExitSignal && s.CompletionIndicators >= threshold
}

// IsBlocked returns true if the status indicates the agent is blocked.
func (s *Status) IsBlocked() bool {
	return s.Status == StatusBlocked
}

// HasProgress returns true if the status indicates meaningful progress.
func (s *Status) HasProgress() bool {
	return s.TasksCompleted > 0 || s.FilesModified > 0
}

// IsTestOnly returns true if this loop was test-only work.
func (s *Status) IsTestOnly() bool {
	return s.WorkType == WorkTypeTesting
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

// CountCompletionIndicators counts how many completion phrases appear in the output.
func CountCompletionIndicators(output string) int {
	lower := strings.ToLower(output)
	count := 0
	for _, pattern := range completionPatterns {
		if strings.Contains(lower, pattern) {
			count++
		}
	}
	return count
}

// DetectRateLimitError checks if the output contains Claude's rate limit error.
// Returns true if a rate limit error is detected.
func DetectRateLimitError(output string) bool {
	return rateLimitRe.MatchString(output)
}

// ExtractErrorSignature extracts a normalized error signature from output.
// The signature can be used to detect repeated identical errors.
// Returns empty string if no error pattern is found.
func ExtractErrorSignature(output string) string {
	matches := errorPatternRe.FindAllStringSubmatch(output, 5) // Get up to 5 error patterns
	if len(matches) == 0 {
		return ""
	}

	// Combine all error patterns into a signature
	var parts []string
	for _, match := range matches {
		if len(match) >= 3 {
			// Normalize: lowercase, trim spaces
			errorType := strings.ToLower(strings.TrimSpace(match[1]))
			errorMsg := strings.ToLower(strings.TrimSpace(match[2]))
			// Remove variable parts like line numbers, timestamps
			errorMsg = normalizeErrorMessage(errorMsg)
			parts = append(parts, errorType+":"+errorMsg)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	// Hash the combined parts for a compact signature
	combined := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(hash[:8]) // 16 char hex string
}

// normalizeErrorMessage removes variable parts from error messages
// to allow comparison of "same" errors.
func normalizeErrorMessage(msg string) string {
	msg = lineNumRe.ReplaceAllString(msg, "")
	msg = timestampRe.ReplaceAllString(msg, "")
	msg = addrRe.ReplaceAllString(msg, "")
	msg = strings.Join(strings.Fields(msg), " ")
	return msg
}

// AnalysisResult contains the full analysis of a loop's output.
type AnalysisResult struct {
	Status          *Status
	RateLimitHit    bool
	ErrorSignature  string
	OutputSize      int
	CompletionCount int

	// Stream metadata (populated by AnalyzeStreamResult, zero when using AnalyzeOutput).
	NumTurns     int
	TotalCostUSD float64
	DurationMS   int
}

// AnalyzeOutput performs full analysis of a loop's output.
func AnalyzeOutput(output string) *AnalysisResult {
	return &AnalysisResult{
		Status:          ParseStatus(output),
		RateLimitHit:    DetectRateLimitError(output),
		ErrorSignature:  ExtractErrorSignature(output),
		OutputSize:      len(output),
		CompletionCount: CountCompletionIndicators(output),
	}
}

// AnalyzeStreamResult produces an AnalysisResult by combining text analysis
// (from TextAccumulator output) with stream ResultEvent metadata.
// Use this instead of AnalyzeOutput when processing --output-format stream-json output.
//
// The text parameter should contain the concatenated assistant text from
// TextAccumulator.Text(). The result parameter is the terminal ResultEvent
// from ParseStream (may be nil if the stream ended prematurely).
func AnalyzeStreamResult(text string, result *ResultEvent) *AnalysisResult {
	analysis := &AnalysisResult{
		Status:          ParseStatus(text),
		CompletionCount: CountCompletionIndicators(text),
		ErrorSignature:  ExtractErrorSignature(text),
		OutputSize:      len(text),
		RateLimitHit:    DetectRateLimitError(text),
	}

	if result != nil {
		// Budget exhaustion is a form of rate limiting
		if !analysis.RateLimitHit && result.Subtype == ResultSubtypeErrorMaxBudget {
			analysis.RateLimitHit = true
		}

		// Capture stream metadata for monitoring and diagnostics
		analysis.NumTurns = result.NumTurns
		analysis.TotalCostUSD = result.TotalCostUSD
		analysis.DurationMS = result.DurationMS
	}

	return analysis
}
