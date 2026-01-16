// Package testutil provides shared test utilities for CLI command tests.
package testutil

// SplitArgs splits a command string into arguments, handling quoted strings.
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
