package build

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

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
		{"deprecated dockerfile flag", "dockerfile", "", ""},
	}

	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

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

func TestCmd_DeprecatedDockerfileFlag(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Dockerfile flag should be hidden (deprecated)
	flag := cmd.Flags().Lookup("dockerfile")
	require.NotNil(t, flag)
	require.True(t, flag.Hidden, "deprecated --dockerfile flag should be hidden")
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
		name   string
		input  []string
		expect map[string]string
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
			name:   "key without value is ignored",
			input:  []string{"key"},
			expect: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseKeyValuePairs(tt.input)

			if tt.expect == nil {
				require.Nil(t, result)
				return
			}

			require.Equal(t, tt.expect, result)
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
			name: "deprecated dockerfile flag",
			args: []string{"--dockerfile", "Dockerfile.old"},
		},
		{
			name: "combined flags",
			args: []string{"-f", "Dockerfile", "-t", "myapp:latest", "--no-cache", "--pull", "-q"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmd(f)

			// Override RunE to prevent actual execution
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				return nil
			}

			// Cobra hack-around for help flag
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
			}
		})
	}
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string {
	return &s
}
