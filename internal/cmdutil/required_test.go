package cmdutil

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestAgentArgsValidator(t *testing.T) {
	tests := []struct {
		name       string
		minArgs    int
		agentFlag  string
		args       []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:      "valid with positional arg",
			minArgs:   1,
			agentFlag: "",
			args:      []string{"container1"},
			wantErr:   false,
		},
		{
			name:      "valid with multiple positional args",
			minArgs:   1,
			agentFlag: "",
			args:      []string{"container1", "container2"},
			wantErr:   false,
		},
		{
			name:      "valid with agent flag",
			minArgs:   1,
			agentFlag: "dev",
			args:      []string{},
			wantErr:   false,
		},
		{
			name:       "error: agent and positional args",
			minArgs:    1,
			agentFlag:  "dev",
			args:       []string{"container1"},
			wantErr:    true,
			wantErrMsg: "--agent and positional container arguments are mutually exclusive",
		},
		{
			name:       "error: no args and no agent",
			minArgs:    1,
			agentFlag:  "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
		{
			name:       "error: not enough args (minArgs=2)",
			minArgs:    2,
			agentFlag:  "",
			args:       []string{"container1"},
			wantErr:    true,
			wantErrMsg: "requires at least 2 container arguments or --agent flag",
		},
		{
			name:      "valid: minArgs=2 with enough args",
			minArgs:   2,
			agentFlag: "",
			args:      []string{"container1", "container2"},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().String("agent", tt.agentFlag, "")
			if tt.agentFlag != "" {
				cmd.Flags().Set("agent", tt.agentFlag)
			}

			validator := AgentArgsValidator(tt.minArgs)
			err := validator(cmd, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("AgentArgsValidator() expected error, got nil")
					return
				}
				if err.Error() != tt.wantErrMsg {
					t.Errorf("AgentArgsValidator() error = %q, want %q", err.Error(), tt.wantErrMsg)
				}
			} else {
				if err != nil {
					t.Errorf("AgentArgsValidator() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestAgentArgsValidatorExact(t *testing.T) {
	tests := []struct {
		name       string
		n          int
		agentFlag  string
		args       []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:      "valid with exact args",
			n:         2,
			agentFlag: "",
			args:      []string{"container1", "newname"},
			wantErr:   false,
		},
		{
			name:      "valid with agent flag",
			n:         2,
			agentFlag: "dev",
			args:      []string{},
			wantErr:   false,
		},
		{
			name:       "error: agent and positional args",
			n:          2,
			agentFlag:  "dev",
			args:       []string{"container1"},
			wantErr:    true,
			wantErrMsg: "--agent and positional container arguments are mutually exclusive",
		},
		{
			name:       "error: too few args (n=2)",
			n:          2,
			agentFlag:  "",
			args:       []string{"container1"},
			wantErr:    true,
			wantErrMsg: "requires exactly 2 container arguments or --agent flag",
		},
		{
			name:       "error: too many args (n=2)",
			n:          2,
			agentFlag:  "",
			args:       []string{"a", "b", "c"},
			wantErr:    true,
			wantErrMsg: "requires exactly 2 container arguments or --agent flag",
		},
		{
			name:       "error: no args (n=1)",
			n:          1,
			agentFlag:  "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires exactly 1 container argument or --agent flag",
		},
		{
			name:      "valid: n=1 with single arg",
			n:         1,
			agentFlag: "",
			args:      []string{"container1"},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().String("agent", tt.agentFlag, "")
			if tt.agentFlag != "" {
				cmd.Flags().Set("agent", tt.agentFlag)
			}

			validator := AgentArgsValidatorExact(tt.n)
			err := validator(cmd, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("AgentArgsValidatorExact() expected error, got nil")
					return
				}
				if err.Error() != tt.wantErrMsg {
					t.Errorf("AgentArgsValidatorExact() error = %q, want %q", err.Error(), tt.wantErrMsg)
				}
			} else {
				if err != nil {
					t.Errorf("AgentArgsValidatorExact() unexpected error: %v", err)
				}
			}
		})
	}
}
