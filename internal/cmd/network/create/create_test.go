package create

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdCreate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantOpts CreateOptions
		wantErr  bool
	}{
		{
			name:     "basic network",
			input:    "mynetwork",
			wantOpts: CreateOptions{Name: "mynetwork", Driver: "bridge"},
		},
		{
			name:     "custom driver",
			input:    "--driver overlay mynetwork",
			wantOpts: CreateOptions{Name: "mynetwork", Driver: "overlay"},
		},
		{
			name:     "internal network",
			input:    "--internal mynetwork",
			wantOpts: CreateOptions{Name: "mynetwork", Driver: "bridge", Internal: true},
		},
		{
			name:     "ipv6 enabled",
			input:    "--ipv6 mynetwork",
			wantOpts: CreateOptions{Name: "mynetwork", Driver: "bridge", IPv6: true},
		},
		{
			name:     "attachable network",
			input:    "--attachable mynetwork",
			wantOpts: CreateOptions{Name: "mynetwork", Driver: "bridge", Attachable: true},
		},
		{
			name:     "driver opts",
			input:    "-o key1=value1 -o key2=value2 mynetwork",
			wantOpts: CreateOptions{Name: "mynetwork", Driver: "bridge", DriverOpts: []string{"key1=value1", "key2=value2"}},
		},
		{
			name:     "labels",
			input:    "--label env=test --label project=myapp mynetwork",
			wantOpts: CreateOptions{Name: "mynetwork", Driver: "bridge", Labels: []string{"env=test", "project=myapp"}},
		},
		{
			name:    "no arguments",
			input:   "",
			wantErr: true,
		},
		{
			name:    "too many arguments",
			input:   "net1 net2",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *CreateOptions
			cmd := NewCmdCreate(f, func(_ context.Context, opts *CreateOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantOpts.Name, gotOpts.Name)
			require.Equal(t, tt.wantOpts.Driver, gotOpts.Driver)
			require.Equal(t, tt.wantOpts.Internal, gotOpts.Internal)
			require.Equal(t, tt.wantOpts.IPv6, gotOpts.IPv6)
			require.Equal(t, tt.wantOpts.Attachable, gotOpts.Attachable)
			require.Equal(t, tt.wantOpts.DriverOpts, gotOpts.DriverOpts)
			require.Equal(t, tt.wantOpts.Labels, gotOpts.Labels)
		})
	}
}

func TestCmdCreate_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdCreate(f, nil)

	require.Equal(t, "create [OPTIONS] NETWORK", cmd.Use)
	require.Empty(t, cmd.Aliases)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("driver"))
	require.NotNil(t, cmd.Flags().Lookup("opt"))
	require.NotNil(t, cmd.Flags().Lookup("label"))
	require.NotNil(t, cmd.Flags().Lookup("internal"))
	require.NotNil(t, cmd.Flags().Lookup("ipv6"))
	require.NotNil(t, cmd.Flags().Lookup("attachable"))

	require.NotNil(t, cmd.Flags().ShorthandLookup("o"))
	require.NotNil(t, cmd.Args)
}

func TestParseKeyValue(t *testing.T) {
	tests := []struct {
		input string
		key   string
		value string
	}{
		{"key=value", "key", "value"},
		{"key=", "key", ""},
		{"key", "key", ""},
		{"key=value=with=equals", "key", "value=with=equals"},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			k, v := parseKeyValue(tt.input)
			require.Equal(t, tt.key, k)
			require.Equal(t, tt.value, v)
		})
	}
}
