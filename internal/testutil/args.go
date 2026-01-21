package testutil

// SplitArgs splits a command string into arguments, handling quoted strings.
// Similar to shell word splitting, but simplified for test scenarios.
// This is useful for parsing CLI arguments in tests.
//
// Examples:
//
//	SplitArgs("foo bar")          → ["foo", "bar"]
//	SplitArgs("foo 'bar baz'")    → ["foo", "bar baz"]
//	SplitArgs(`foo "bar baz"`)    → ["foo", "bar baz"]
//	SplitArgs("")                 → nil
//	SplitArgs("  ")               → nil
//
// Note: Unlike real shell parsing, this does not handle escape characters,
// command substitution, or other advanced shell features. Tabs and newlines
// within unquoted strings are preserved as-is (not treated as word separators).
func SplitArgs(input string) []string {
	var args []string
	var current string
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(input); i++ {
		c := input[i]
		if c == '"' || c == '\'' {
			if inQuote && c == quoteChar {
				inQuote = false
			} else if !inQuote {
				inQuote = true
				quoteChar = c
			} else {
				current += string(c)
			}
		} else if c == ' ' && !inQuote {
			if current != "" {
				args = append(args, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}
