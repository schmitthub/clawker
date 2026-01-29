// Package create provides the volume create command.
package create

import (
	"context"
	"fmt"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/cobra"
)

// Options holds options for the create command.
type Options struct {
	Driver     string
	DriverOpts []string
	Labels     []string
}

// NewCmd creates the volume create command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "create [OPTIONS] [VOLUME]",
		Short: "Create a volume",
		Long: `Creates a new clawker-managed volume.

If no name is specified, Docker will generate a random name.
The volume will be labeled as a clawker-managed resource.`,
		Example: `  # Create a volume with a name
  clawker volume create myvolume

  # Create a volume with specific driver
  clawker volume create --driver local myvolume

  # Create a volume with driver options
  clawker volume create --driver local --opt type=tmpfs --opt device=tmpfs myvolume

  # Create a volume with labels
  clawker volume create --label env=test --label project=myapp myvolume`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 0 {
				name = args[0]
			}
			return run(f, opts, name)
		},
	}

	cmd.Flags().StringVarP(&opts.Driver, "driver", "d", "local", "Specify volume driver name")
	cmd.Flags().StringArrayVarP(&opts.DriverOpts, "opt", "o", nil, "Set driver specific options")
	cmd.Flags().StringArrayVar(&opts.Labels, "label", nil, "Set metadata for a volume")

	return cmd
}

func run(f *cmdutil.Factory, opts *Options, name string) error {
	ctx := context.Background()
	ios := f.IOStreams

	// Connect to Docker
	client, err := f.Client(ctx)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Build create options
	createOpts := docker.VolumeCreateOptions{
		Name:       name,
		Driver:     opts.Driver,
		DriverOpts: parseDriverOpts(opts.DriverOpts),
		Labels:     parseLabels(opts.Labels),
	}

	// Create the volume
	vol, err := client.VolumeCreate(ctx, createOpts)
	if err != nil {
		cmdutil.HandleError(ios, err)
		return err
	}

	// Print the volume name
	fmt.Fprintln(ios.Out, vol.Volume.Name)
	return nil
}

// parseDriverOpts converts --opt key=value flags to a map.
func parseDriverOpts(opts []string) map[string]string {
	if len(opts) == 0 {
		return nil
	}

	result := make(map[string]string)
	for _, opt := range opts {
		k, v := parseKeyValue(opt)
		if k != "" {
			result[k] = v
		}
	}
	return result
}

// parseLabels converts --label key=value flags to a map.
func parseLabels(labels []string) map[string]string {
	if len(labels) == 0 {
		return nil
	}

	result := make(map[string]string)
	for _, label := range labels {
		k, v := parseKeyValue(label)
		if k != "" {
			result[k] = v
		}
	}
	return result
}

// parseKeyValue splits a key=value string.
func parseKeyValue(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
