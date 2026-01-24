package prompter

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// PromptForConfirmation prompts the user for y/N confirmation.
// Uses the provided reader for input (pass cmd.InOrStdin() for testability).
// Returns true only for "y" or "Y", false for anything else including errors.
// Writes prompt to stderr (not stdout) to preserve stdout for data output.
//
// Deprecated: Use Prompter.Confirm() instead for better testability.
func PromptForConfirmation(in io.Reader, message string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", message)
	reader := bufio.NewReader(in)
	response, err := reader.ReadString('\n')
	if err != nil {
		// Treat read errors (EOF, etc.) as "no"
		fmt.Fprintln(os.Stderr) // Newline for cleaner output
		return false
	}
	response = strings.TrimSpace(response)
	return response == "y" || response == "Y"
}

// Prompter provides interactive prompting functionality.
// It uses IOStreams for testable I/O.
type Prompter struct {
	ios *iostreams.IOStreams
}

// NewPrompter creates a new Prompter with the given IOStreams.
func NewPrompter(ios *iostreams.IOStreams) *Prompter {
	return &Prompter{ios: ios}
}

// PromptConfig configures a string prompt.
type PromptConfig struct {
	Message   string
	Default   string
	Required  bool
	Validator func(string) error
}

// String prompts the user for a string value.
// Returns the default if the user enters nothing.
// In non-interactive mode, returns the default without prompting.
func (p *Prompter) String(cfg PromptConfig) (string, error) {
	if !p.ios.IsInteractive() {
		if cfg.Required && cfg.Default == "" {
			return "", fmt.Errorf("required input missing in non-interactive mode")
		}
		return cfg.Default, nil
	}

	prompt := cfg.Message
	if cfg.Default != "" {
		prompt = fmt.Sprintf("%s [%s]", cfg.Message, cfg.Default)
	}

	fmt.Fprintf(p.ios.ErrOut, "%s: ", prompt)

	reader := bufio.NewReader(p.ios.In)
	response, err := reader.ReadString('\n')
	if err != nil {
		if err == io.EOF && cfg.Default != "" {
			fmt.Fprintln(p.ios.ErrOut) // Newline for cleaner output
			return cfg.Default, nil
		}
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	response = strings.TrimSpace(response)
	if response == "" {
		response = cfg.Default
	}

	if cfg.Required && response == "" {
		return "", fmt.Errorf("required input missing")
	}

	if cfg.Validator != nil {
		if err := cfg.Validator(response); err != nil {
			return "", err
		}
	}

	return response, nil
}

// Confirm prompts the user for a yes/no confirmation.
// In non-interactive mode, returns the default without prompting.
func (p *Prompter) Confirm(message string, defaultYes bool) (bool, error) {
	if !p.ios.IsInteractive() {
		return defaultYes, nil
	}

	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}

	fmt.Fprintf(p.ios.ErrOut, "%s %s ", message, hint)

	reader := bufio.NewReader(p.ios.In)
	response, err := reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			fmt.Fprintln(p.ios.ErrOut) // Newline for cleaner output
			return defaultYes, nil
		}
		return false, fmt.Errorf("failed to read input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))
	if response == "" {
		return defaultYes, nil
	}

	return response == "y" || response == "yes", nil
}

// SelectOption represents an option in a selection prompt.
type SelectOption struct {
	Label       string
	Description string
}

// Select prompts the user to select from a list of options.
// Returns the index of the selected option.
// In non-interactive mode, returns the defaultIdx without prompting.
func (p *Prompter) Select(message string, options []SelectOption, defaultIdx int) (int, error) {
	if len(options) == 0 {
		return -1, fmt.Errorf("no options provided")
	}

	if defaultIdx < 0 || defaultIdx >= len(options) {
		defaultIdx = 0
	}

	if !p.ios.IsInteractive() {
		return defaultIdx, nil
	}

	fmt.Fprintf(p.ios.ErrOut, "%s:\n", message)
	for i, opt := range options {
		marker := "  "
		if i == defaultIdx {
			marker = "> "
		}
		if opt.Description != "" {
			fmt.Fprintf(p.ios.ErrOut, "%s%d. %s (%s)\n", marker, i+1, opt.Label, opt.Description)
		} else {
			fmt.Fprintf(p.ios.ErrOut, "%s%d. %s\n", marker, i+1, opt.Label)
		}
	}

	fmt.Fprintf(p.ios.ErrOut, "Enter selection [%d]: ", defaultIdx+1)

	reader := bufio.NewReader(p.ios.In)
	response, err := reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			fmt.Fprintln(p.ios.ErrOut) // Newline for cleaner output
			return defaultIdx, nil
		}
		return -1, fmt.Errorf("failed to read input: %w", err)
	}

	response = strings.TrimSpace(response)
	if response == "" {
		return defaultIdx, nil
	}

	idx, err := strconv.Atoi(response)
	if err != nil || idx < 1 || idx > len(options) {
		return -1, fmt.Errorf("invalid selection: %s", response)
	}

	return idx - 1, nil
}
