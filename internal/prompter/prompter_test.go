package prompter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
)

func TestPromptForConfirmation(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"lowercase y", "y\n", true},
		{"uppercase Y", "Y\n", true},
		{"lowercase n", "n\n", false},
		{"uppercase N", "N\n", false},
		{"yes word", "yes\n", false}, // Only y/Y accepted
		{"no word", "no\n", false},
		{"empty input", "\n", false},
		{"whitespace y", "  y  \n", true},
		{"random text", "maybe\n", false},
		{"EOF (empty reader)", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.input)
			got := PromptForConfirmation(reader, "Continue?")
			if got != tt.want {
				t.Errorf("PromptForConfirmation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewPrompter(t *testing.T) {
	ios := iostreamstest.New()
	p := NewPrompter(ios.IOStreams)
	if p == nil {
		t.Fatal("NewPrompter() returned nil")
	}
	if p.ios != ios.IOStreams {
		t.Error("NewPrompter().ios is not set correctly")
	}
}

func TestPrompter_String(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		cfg         PromptConfig
		interactive bool
		want        string
		wantErr     bool
	}{
		{
			name:        "returns user input",
			input:       "user value\n",
			cfg:         PromptConfig{Message: "Enter value"},
			interactive: true,
			want:        "user value",
			wantErr:     false,
		},
		{
			name:        "returns default on empty input",
			input:       "\n",
			cfg:         PromptConfig{Message: "Enter value", Default: "default"},
			interactive: true,
			want:        "default",
			wantErr:     false,
		},
		{
			name:        "returns default on EOF",
			input:       "",
			cfg:         PromptConfig{Message: "Enter value", Default: "default"},
			interactive: true,
			want:        "default",
			wantErr:     false,
		},
		{
			name:        "non-interactive returns default",
			input:       "",
			cfg:         PromptConfig{Message: "Enter value", Default: "default"},
			interactive: false,
			want:        "default",
			wantErr:     false,
		},
		{
			name:        "non-interactive with required and empty default errors",
			input:       "",
			cfg:         PromptConfig{Message: "Enter value", Required: true, Default: ""},
			interactive: false,
			want:        "",
			wantErr:     true,
		},
		{
			name:        "interactive required with empty input errors",
			input:       "\n",
			cfg:         PromptConfig{Message: "Enter value", Required: true},
			interactive: true,
			want:        "",
			wantErr:     true,
		},
		{
			name:  "validator called on input",
			input: "invalid\n",
			cfg: PromptConfig{
				Message: "Enter value",
				Validator: func(s string) error {
					if s == "invalid" {
						return fmt.Errorf("invalid value")
					}
					return nil
				},
			},
			interactive: true,
			want:        "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := iostreamstest.New()
			ios.InBuf.SetInput(tt.input)
			ios.SetInteractive(tt.interactive)

			p := NewPrompter(ios.IOStreams)
			got, err := p.String(tt.cfg)

			if tt.wantErr {
				if err == nil {
					t.Error("String() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("String() unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("String() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestPrompter_Confirm(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		defaultYes  bool
		interactive bool
		want        bool
		wantErr     bool
	}{
		{
			name:        "y confirms",
			input:       "y\n",
			defaultYes:  false,
			interactive: true,
			want:        true,
			wantErr:     false,
		},
		{
			name:        "yes confirms",
			input:       "yes\n",
			defaultYes:  false,
			interactive: true,
			want:        true,
			wantErr:     false,
		},
		{
			name:        "Y confirms",
			input:       "Y\n",
			defaultYes:  false,
			interactive: true,
			want:        true,
			wantErr:     false,
		},
		{
			name:        "n denies",
			input:       "n\n",
			defaultYes:  true,
			interactive: true,
			want:        false,
			wantErr:     false,
		},
		{
			name:        "empty uses default yes",
			input:       "\n",
			defaultYes:  true,
			interactive: true,
			want:        true,
			wantErr:     false,
		},
		{
			name:        "empty uses default no",
			input:       "\n",
			defaultYes:  false,
			interactive: true,
			want:        false,
			wantErr:     false,
		},
		{
			name:        "EOF uses default",
			input:       "",
			defaultYes:  true,
			interactive: true,
			want:        true,
			wantErr:     false,
		},
		{
			name:        "non-interactive returns default yes",
			input:       "",
			defaultYes:  true,
			interactive: false,
			want:        true,
			wantErr:     false,
		},
		{
			name:        "non-interactive returns default no",
			input:       "",
			defaultYes:  false,
			interactive: false,
			want:        false,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := iostreamstest.New()
			ios.InBuf.SetInput(tt.input)
			ios.SetInteractive(tt.interactive)

			p := NewPrompter(ios.IOStreams)
			got, err := p.Confirm("Continue?", tt.defaultYes)

			if tt.wantErr {
				if err == nil {
					t.Error("Confirm() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Confirm() unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("Confirm() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestPrompter_Select(t *testing.T) {
	options := []SelectOption{
		{Label: "Option A", Description: "First option"},
		{Label: "Option B", Description: "Second option"},
		{Label: "Option C", Description: "Third option"},
	}

	tests := []struct {
		name        string
		input       string
		options     []SelectOption
		defaultIdx  int
		interactive bool
		want        int
		wantErr     bool
	}{
		{
			name:        "select first option",
			input:       "1\n",
			options:     options,
			defaultIdx:  0,
			interactive: true,
			want:        0,
			wantErr:     false,
		},
		{
			name:        "select second option",
			input:       "2\n",
			options:     options,
			defaultIdx:  0,
			interactive: true,
			want:        1,
			wantErr:     false,
		},
		{
			name:        "select third option",
			input:       "3\n",
			options:     options,
			defaultIdx:  0,
			interactive: true,
			want:        2,
			wantErr:     false,
		},
		{
			name:        "empty uses default",
			input:       "\n",
			options:     options,
			defaultIdx:  1,
			interactive: true,
			want:        1,
			wantErr:     false,
		},
		{
			name:        "EOF uses default",
			input:       "",
			options:     options,
			defaultIdx:  2,
			interactive: true,
			want:        2,
			wantErr:     false,
		},
		{
			name:        "non-interactive returns default",
			input:       "",
			options:     options,
			defaultIdx:  1,
			interactive: false,
			want:        1,
			wantErr:     false,
		},
		{
			name:        "invalid selection errors",
			input:       "4\n",
			options:     options,
			defaultIdx:  0,
			interactive: true,
			want:        -1,
			wantErr:     true,
		},
		{
			name:        "zero selection errors",
			input:       "0\n",
			options:     options,
			defaultIdx:  0,
			interactive: true,
			want:        -1,
			wantErr:     true,
		},
		{
			name:        "non-numeric selection errors",
			input:       "abc\n",
			options:     options,
			defaultIdx:  0,
			interactive: true,
			want:        -1,
			wantErr:     true,
		},
		{
			name:        "no options errors",
			input:       "",
			options:     []SelectOption{},
			defaultIdx:  0,
			interactive: true,
			want:        -1,
			wantErr:     true,
		},
		{
			name:        "invalid default index gets clamped",
			input:       "\n",
			options:     options,
			defaultIdx:  10,
			interactive: true,
			want:        0, // Clamped to 0
			wantErr:     false,
		},
		{
			name:        "negative default index gets clamped",
			input:       "\n",
			options:     options,
			defaultIdx:  -5,
			interactive: true,
			want:        0, // Clamped to 0
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := iostreamstest.New()
			ios.InBuf.SetInput(tt.input)
			ios.SetInteractive(tt.interactive)

			p := NewPrompter(ios.IOStreams)
			got, err := p.Select("Choose option", tt.options, tt.defaultIdx)

			if tt.wantErr {
				if err == nil {
					t.Error("Select() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Select() unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("Select() = %d, want %d", got, tt.want)
				}
			}
		})
	}
}
