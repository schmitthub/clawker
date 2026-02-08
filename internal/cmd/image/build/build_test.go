package build

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdBuild(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdBuild(f, nil)

	// Test command basics
	require.Equal(t, "build", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

func TestCmd_Flags(t *testing.T) {
	tests := []struct {
		name      string
		flag      string
		shorthand string
		defValue  string
	}{
		{"file flag", "file", "f", ""},
		{"tag flag", "tag", "t", "[]"},
		{"no-cache flag", "no-cache", "", "false"},
		{"pull flag", "pull", "", "false"},
		{"build-arg flag", "build-arg", "", "[]"},
		{"label flag", "label", "", "[]"},
		{"target flag", "target", "", ""},
		{"quiet flag", "quiet", "q", "false"},
		{"progress flag", "progress", "", "auto"},
		{"network flag", "network", "", ""},
	}

	f := &cmdutil.Factory{}
	cmd := NewCmdBuild(f, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := cmd.Flags().Lookup(tt.flag)
			require.NotNil(t, flag, "flag --%s should exist", tt.flag)

			if tt.shorthand != "" {
				require.Equal(t, tt.shorthand, flag.Shorthand,
					"flag --%s should have shorthand -%s", tt.flag, tt.shorthand)
			}

			require.Equal(t, tt.defValue, flag.DefValue,
				"flag --%s should have default value %q", tt.flag, tt.defValue)
		})
	}
}

func TestParseBuildArgs(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		expect map[string]*string
	}{
		{
			name:   "empty args",
			input:  nil,
			expect: nil,
		},
		{
			name:  "single key-value",
			input: []string{"KEY=value"},
			expect: map[string]*string{
				"KEY": strPtr("value"),
			},
		},
		{
			name:  "multiple key-values",
			input: []string{"KEY1=value1", "KEY2=value2"},
			expect: map[string]*string{
				"KEY1": strPtr("value1"),
				"KEY2": strPtr("value2"),
			},
		},
		{
			name:  "value with equals sign",
			input: []string{"KEY=val=ue"},
			expect: map[string]*string{
				"KEY": strPtr("val=ue"),
			},
		},
		{
			name:  "key without value uses nil (env passthrough)",
			input: []string{"KEY"},
			expect: map[string]*string{
				"KEY": nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseBuildArgs(tt.input)

			if tt.expect == nil {
				require.Nil(t, result)
				return
			}

			require.Equal(t, len(tt.expect), len(result))
			for k, v := range tt.expect {
				resultVal, ok := result[k]
				require.True(t, ok, "key %q should exist", k)
				if v == nil {
					require.Nil(t, resultVal)
				} else {
					require.Equal(t, *v, *resultVal)
				}
			}
		})
	}
}

func TestParseKeyValuePairs(t *testing.T) {
	tests := []struct {
		name          string
		input         []string
		expect        map[string]string
		expectInvalid []string
	}{
		{
			name:   "empty pairs",
			input:  nil,
			expect: nil,
		},
		{
			name:   "single pair",
			input:  []string{"key=value"},
			expect: map[string]string{"key": "value"},
		},
		{
			name:   "multiple pairs",
			input:  []string{"key1=value1", "key2=value2"},
			expect: map[string]string{"key1": "value1", "key2": "value2"},
		},
		{
			name:   "value with equals",
			input:  []string{"key=val=ue"},
			expect: map[string]string{"key": "val=ue"},
		},
		{
			name:          "key without value is invalid",
			input:         []string{"key"},
			expect:        map[string]string{},
			expectInvalid: []string{"key"},
		},
		{
			name:          "mixed valid and invalid",
			input:         []string{"a=b", "bad", "c=d"},
			expect:        map[string]string{"a": "b", "c": "d"},
			expectInvalid: []string{"bad"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, invalid := parseKeyValuePairs(tt.input)

			if tt.expect == nil {
				require.Nil(t, result)
				require.Nil(t, invalid)
				return
			}

			require.Equal(t, tt.expect, result)
			require.Equal(t, tt.expectInvalid, invalid)
		})
	}
}

func TestMergeLabels(t *testing.T) {
	tests := []struct {
		name          string
		userLabels    map[string]string
		clawkerLabels map[string]string
		expect        map[string]string
	}{
		{
			name:          "empty inputs",
			userLabels:    nil,
			clawkerLabels: nil,
			expect:        map[string]string{},
		},
		{
			name:          "only user labels",
			userLabels:    map[string]string{"user": "value"},
			clawkerLabels: nil,
			expect:        map[string]string{"user": "value"},
		},
		{
			name:          "only clawker labels",
			userLabels:    nil,
			clawkerLabels: map[string]string{"com.clawker.managed": "true"},
			expect:        map[string]string{"com.clawker.managed": "true"},
		},
		{
			name:          "clawker labels override user labels",
			userLabels:    map[string]string{"com.clawker.managed": "false", "user": "value"},
			clawkerLabels: map[string]string{"com.clawker.managed": "true"},
			expect:        map[string]string{"com.clawker.managed": "true", "user": "value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeLabels(tt.userLabels, tt.clawkerLabels)
			require.Equal(t, tt.expect, result)
		})
	}
}

func TestCmd_FlagParsing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name: "no flags",
			args: []string{},
		},
		{
			name: "file short flag",
			args: []string{"-f", "Dockerfile.dev"},
		},
		{
			name: "file long flag",
			args: []string{"--file", "Dockerfile.dev"},
		},
		{
			name: "tag short flag",
			args: []string{"-t", "myimage:latest"},
		},
		{
			name: "multiple tags",
			args: []string{"-t", "myimage:latest", "-t", "myimage:v1"},
		},
		{
			name: "no-cache flag",
			args: []string{"--no-cache"},
		},
		{
			name: "pull flag",
			args: []string{"--pull"},
		},
		{
			name: "build-arg",
			args: []string{"--build-arg", "KEY=VALUE"},
		},
		{
			name: "label",
			args: []string{"--label", "version=1.0"},
		},
		{
			name: "target",
			args: []string{"--target", "builder"},
		},
		{
			name: "quiet short flag",
			args: []string{"-q"},
		},
		{
			name: "quiet long flag",
			args: []string{"--quiet"},
		},
		{
			name: "progress flag",
			args: []string{"--progress", "plain"},
		},
		{
			name: "network flag",
			args: []string{"--network", "host"},
		},
		{
			name: "combined flags",
			args: []string{"-f", "Dockerfile", "-t", "myapp:latest", "--no-cache", "--pull", "-q"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			var gotOpts *BuildOptions
			cmd := NewCmdBuild(f, func(_ context.Context, opts *BuildOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")
			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, gotOpts)
			}
		})
	}
}

// TestCmd_FlagValuePropagation verifies that flag values are correctly captured
// in BuildOptions. This catches bugs where flag bindings are accidentally changed.
func TestCmd_FlagValuePropagation(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		verify func(t *testing.T, opts *BuildOptions)
	}{
		{
			name: "file flag value",
			args: []string{"-f", "Dockerfile.dev"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, "Dockerfile.dev", opts.File)
			},
		},
		{
			name: "single tag",
			args: []string{"-t", "myimage:v1"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, []string{"myimage:v1"}, opts.Tags)
			},
		},
		{
			name: "multiple tags",
			args: []string{"-t", "myimage:v1", "-t", "myimage:latest"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, []string{"myimage:v1", "myimage:latest"}, opts.Tags)
			},
		},
		{
			name: "no-cache true",
			args: []string{"--no-cache"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.True(t, opts.NoCache)
			},
		},
		{
			name: "pull true",
			args: []string{"--pull"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.True(t, opts.Pull)
			},
		},
		{
			name: "build-arg values",
			args: []string{"--build-arg", "KEY1=value1", "--build-arg", "KEY2=value2"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, []string{"KEY1=value1", "KEY2=value2"}, opts.BuildArgs)
			},
		},
		{
			name: "label values",
			args: []string{"--label", "version=1.0", "--label", "team=backend"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, []string{"version=1.0", "team=backend"}, opts.Labels)
			},
		},
		{
			name: "target value",
			args: []string{"--target", "builder"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, "builder", opts.Target)
			},
		},
		{
			name: "quiet short flag",
			args: []string{"-q"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.True(t, opts.Quiet)
			},
		},
		{
			name: "progress value",
			args: []string{"--progress", "plain"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, "plain", opts.Progress)
			},
		},
		{
			name: "progress none",
			args: []string{"--progress", "none"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, "none", opts.Progress)
			},
		},
		{
			name: "network value",
			args: []string{"--network", "host"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, "host", opts.Network)
			},
		},
		{
			name: "combined flags preserve all values",
			args: []string{"-f", "Custom.dockerfile", "-t", "app:v1", "-t", "app:latest", "--no-cache", "--pull", "-q", "--target", "prod"},
			verify: func(t *testing.T, opts *BuildOptions) {
				require.Equal(t, "Custom.dockerfile", opts.File)
				require.Equal(t, []string{"app:v1", "app:latest"}, opts.Tags)
				require.True(t, opts.NoCache)
				require.True(t, opts.Pull)
				require.True(t, opts.Quiet)
				require.Equal(t, "prod", opts.Target)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *BuildOptions
			cmd := NewCmdBuild(f, func(_ context.Context, opts *BuildOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")
			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.NotNil(t, gotOpts)

			tt.verify(t, gotOpts)
		})
	}
}

// BuildKitEnabled is now resolved directly in buildRun via docker.BuildKitEnabled,
// no longer wired through Factory. The test for factory wiring is removed.

// strPtr returns a pointer to the given string.
func strPtr(s string) *string {
	return &s
}
