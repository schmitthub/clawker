package cmdutil

import (
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/spf13/cobra"
)

// Unit tests for resolve.go functions that don't require Docker

func TestResolveDefaultImage(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		settings *config.Settings
		want     string
	}{
		{
			name:     "both nil returns empty",
			cfg:      nil,
			settings: nil,
			want:     "",
		},
		{
			name: "config takes precedence over settings",
			cfg: &config.Config{
				DefaultImage: "config-image:latest",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-image:latest",
				},
			},
			want: "config-image:latest",
		},
		{
			name: "settings fallback when config empty",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-image:latest",
				},
			},
			want: "settings-image:latest",
		},
		{
			name: "settings only (nil config)",
			cfg:  nil,
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-only:latest",
				},
			},
			want: "settings-only:latest",
		},
		{
			name: "config only (nil settings)",
			cfg: &config.Config{
				DefaultImage: "config-only:latest",
			},
			settings: nil,
			want:     "config-only:latest",
		},
		{
			name: "both empty returns empty",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "",
				},
			},
			want: "",
		},
		{
			name: "empty config with nil settings",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: nil,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveDefaultImage(tt.cfg, tt.settings)
			if got != tt.want {
				t.Errorf("ResolveDefaultImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImage_FallbackToDefault(t *testing.T) {
	// When no docker client is provided, should fall back to default from config/settings
	// (project image lookup requires docker client)
	tests := []struct {
		name     string
		cfg      *config.Config
		settings *config.Settings
		want     string
	}{
		{
			name: "falls back to config default",
			cfg: &config.Config{
				DefaultImage: "config-default:latest",
			},
			settings: nil,
			want:     "config-default:latest",
		},
		{
			name: "falls back to settings default",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-default:latest",
				},
			},
			want: "settings-default:latest",
		},
		{
			name: "config default takes precedence over settings",
			cfg: &config.Config{
				DefaultImage: "config-default:latest",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-default:latest",
				},
			},
			want: "config-default:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass nil for dockerClient - project image lookup will be skipped
			got, err := ResolveImage(context.TODO(), nil, tt.cfg, tt.settings)
			if err != nil {
				t.Fatalf("ResolveImage() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImage_NilParameters(t *testing.T) {
	// Test with all nil parameters - should return empty string, no error
	got, err := ResolveImage(context.TODO(), nil, nil, nil)
	if err != nil {
		t.Fatalf("ResolveImage() returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("ResolveImage() with all nil = %q, want empty string", got)
	}
}

func TestFindProjectImage_NilClient(t *testing.T) {
	ctx := context.Background()

	result, err := FindProjectImage(ctx, nil, "myproject")
	if err != nil {
		t.Errorf("FindProjectImage() unexpected error = %v", err)
	}
	if result != "" {
		t.Errorf("FindProjectImage() = %q, want empty string", result)
	}
}

func TestFindProjectImage_EmptyProject(t *testing.T) {
	ctx := context.Background()

	// Even with a valid client, empty project should return empty
	// Note: We pass nil client since empty project should short-circuit before using it
	result, err := FindProjectImage(ctx, nil, "")
	if err != nil {
		t.Errorf("FindProjectImage() unexpected error = %v", err)
	}
	if result != "" {
		t.Errorf("FindProjectImage() = %q, want empty string", result)
	}
}

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
			agentFlag: "ralph",
			args:      []string{},
			wantErr:   false,
		},
		{
			name:       "error: agent and positional args",
			minArgs:    1,
			agentFlag:  "ralph",
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
			// Create a minimal cobra command with the agent flag
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
			agentFlag: "ralph",
			args:      []string{},
			wantErr:   false,
		},
		{
			name:       "error: agent and positional args",
			n:          2,
			agentFlag:  "ralph",
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
			// Create a minimal cobra command with the agent flag
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

func TestResolveContainerName(t *testing.T) {
	tests := []struct {
		name       string
		project    string
		agentName  string
		wantResult string
		wantErr    bool
	}{
		{
			name:       "valid resolution",
			project:    "myapp",
			agentName:  "ralph",
			wantResult: "clawker.myapp.ralph",
			wantErr:    false,
		},
		{
			name:       "empty project returns error",
			project:    "",
			agentName:  "ralph",
			wantResult: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Factory{}
			f.configData = &config.Config{Project: tt.project}

			result, err := ResolveContainerName(f, tt.agentName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveContainerName() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("ResolveContainerName() unexpected error: %v", err)
					return
				}
				if result != tt.wantResult {
					t.Errorf("ResolveContainerName() = %q, want %q", result, tt.wantResult)
				}
			}
		})
	}
}

func TestResolveContainerName_ConfigError(t *testing.T) {
	f := &Factory{}
	f.configErr = fmt.Errorf("config load error")

	_, err := ResolveContainerName(f, "ralph")
	if err == nil {
		t.Errorf("ResolveContainerName() expected error when config fails, got nil")
	}
}

func TestResolveContainerNames(t *testing.T) {
	tests := []struct {
		name          string
		project       string
		agentName     string
		containerArgs []string
		wantResult    []string
		wantErr       bool
	}{
		{
			name:          "with agent name",
			project:       "myapp",
			agentName:     "ralph",
			containerArgs: nil,
			wantResult:    []string{"clawker.myapp.ralph"},
			wantErr:       false,
		},
		{
			name:          "with container args",
			project:       "myapp",
			agentName:     "",
			containerArgs: []string{"container1", "container2"},
			wantResult:    []string{"container1", "container2"},
			wantErr:       false,
		},
		{
			name:          "empty agent and empty args",
			project:       "myapp",
			agentName:     "",
			containerArgs: []string{},
			wantResult:    []string{},
			wantErr:       false,
		},
		{
			name:          "agent with empty project returns error",
			project:       "",
			agentName:     "ralph",
			containerArgs: nil,
			wantResult:    nil,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := NewTestIOStreams()
			f := &Factory{IOStreams: ios.IOStreams}
			f.configData = &config.Config{Project: tt.project}

			result, err := ResolveContainerNames(f, tt.agentName, tt.containerArgs)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveContainerNames() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("ResolveContainerNames() unexpected error: %v", err)
					return
				}
				if len(result) != len(tt.wantResult) {
					t.Errorf("ResolveContainerNames() len = %d, want %d", len(result), len(tt.wantResult))
					return
				}
				for i, r := range result {
					if r != tt.wantResult[i] {
						t.Errorf("ResolveContainerNames()[%d] = %q, want %q", i, r, tt.wantResult[i])
					}
				}
			}
		})
	}
}

func TestResolveImageWithSource_NoDocker(t *testing.T) {
	// Test ResolveImageWithSource without Docker client (project image lookup skipped)
	ctx := context.Background()

	tests := []struct {
		name       string
		cfg        *config.Config
		settings   *config.Settings
		wantRef    string
		wantSource ImageSource
		wantNil    bool
	}{
		{
			name:       "falls back to default from config",
			cfg:        &config.Config{DefaultImage: "config-default:latest"},
			settings:   nil,
			wantRef:    "config-default:latest",
			wantSource: ImageSourceDefault,
			wantNil:    false,
		},
		{
			name:       "falls back to default from settings",
			cfg:        &config.Config{DefaultImage: ""},
			settings:   &config.Settings{Project: config.ProjectDefaults{DefaultImage: "settings-default:latest"}},
			wantRef:    "settings-default:latest",
			wantSource: ImageSourceDefault,
			wantNil:    false,
		},
		{
			name:       "config default takes precedence over settings",
			cfg:        &config.Config{DefaultImage: "config-default:latest"},
			settings:   &config.Settings{Project: config.ProjectDefaults{DefaultImage: "settings-default:latest"}},
			wantRef:    "config-default:latest",
			wantSource: ImageSourceDefault,
			wantNil:    false,
		},
		{
			name:       "returns nil when no image found",
			cfg:        nil,
			settings:   nil,
			wantRef:    "",
			wantSource: "",
			wantNil:    true,
		},
		{
			name: "returns nil when all sources empty",
			cfg: &config.Config{
				DefaultImage: "",
				Project:      "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "",
				},
			},
			wantRef:    "",
			wantSource: "",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass nil for dockerClient - project image lookup will be skipped
			result, err := ResolveImageWithSource(ctx, nil, tt.cfg, tt.settings)

			if err != nil {
				t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
			}

			if tt.wantNil {
				if result != nil {
					t.Errorf("ResolveImageWithSource() = %+v, want nil", result)
				}
				return
			}

			if result == nil {
				t.Fatal("ResolveImageWithSource() returned nil, want non-nil")
			}

			if result.Reference != tt.wantRef {
				t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, tt.wantRef)
			}
			if result.Source != tt.wantSource {
				t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, tt.wantSource)
			}
		})
	}
}
