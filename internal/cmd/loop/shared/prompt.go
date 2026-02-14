package shared

import "strings"

// LoopStatusInstructions is the default system prompt that instructs the agent
// to output a structured LOOP_STATUS block at the end of each response.
// This block is parsed by the loop engine for circuit breaker and progress tracking.
//
// The example block within this prompt is intentionally valid — it is used in
// tests to verify that the prompt stays in sync with ParseStatus.
const LoopStatusInstructions = `At the end of your response, you MUST output a structured status block in the following exact format:

---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 0
FILES_MODIFIED: 0
TESTS_STATUS: NOT_RUN
WORK_TYPE: IMPLEMENTATION
EXIT_SIGNAL: false
RECOMMENDATION: Describe your next step here
---END_LOOP_STATUS---

Field definitions:
- STATUS: One of IN_PROGRESS, COMPLETE, or BLOCKED
- TASKS_COMPLETED_THIS_LOOP: Number of tasks completed in THIS iteration only (integer)
- FILES_MODIFIED: Number of unique files you created or modified (integer)
- TESTS_STATUS: One of PASSING, FAILING, or NOT_RUN
- WORK_TYPE: One of IMPLEMENTATION, TESTING, DOCUMENTATION, or REFACTORING
- EXIT_SIGNAL: Set to true ONLY when ALL work is genuinely done; false otherwise
- RECOMMENDATION: A brief, actionable one-line recommendation for the next step

Rules:
- Always output this block at the very end of your response, even if you made no progress
- Set STATUS to COMPLETE and EXIT_SIGNAL to true only when all assigned work is finished
- Set STATUS to BLOCKED if you cannot proceed without human intervention
- Do not fabricate progress — report actual counts accurately`

// BuildSystemPrompt combines the default LOOP_STATUS instructions with optional
// additional instructions provided by the user via --append-system-prompt.
// If additional is empty, returns only the default instructions.
func BuildSystemPrompt(additional string) string {
	if additional == "" {
		return LoopStatusInstructions
	}
	return LoopStatusInstructions + "\n\n" + strings.TrimSpace(additional)
}
