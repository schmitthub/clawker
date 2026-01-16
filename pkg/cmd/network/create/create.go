// Package create provides the network create command.
package create

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds options for the create command.
type Options struct {
	Driver     string
	DriverOpts []string
	Labels     []string
	Internal   bool
	IPv6       bool
	Attachable bool
}

// NewCmd creates the network create command.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "create [OPTIONS] NETWORK",
		Short: "Create a network",
		Long: `Creates a new clawker-managed network.

The network will be labeled as a clawker-managed resource.
By default, a bridge network driver is used.`,
		Example: `  # Create a network
  clawker network create mynetwork

  # Create an internal network (no external connectivity)
  clawker network create --internal mynetwork

  # Create a network with custom driver options
  clawker network create --driver bridge --opt com.docker.network.bridge.name=mybridge mynetwork

  # Create a network with labels
  clawker network create --label env=test --label project=myapp mynetwork`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(f, opts, args[0])
		},
	}

	cmd.Flags().StringVar(&opts.Driver, "driver", "bridge", "Driver to manage the network")
	cmd.Flags().StringArrayVarP(&opts.DriverOpts, "opt", "o", nil, "Set driver specific options")
	cmd.Flags().StringArrayVar(&opts.Labels, "label", nil, "Set metadata for a network")
	cmd.Flags().BoolVar(&opts.Internal, "internal", false, "Restrict external access to the network")
	cmd.Flags().BoolVar(&opts.IPv6, "ipv6", false, "Enable IPv6 networking")
	cmd.Flags().BoolVar(&opts.Attachable, "attachable", false, "Enable manual container attachment")

	return cmd
}

func run(_ *cmdutil.Factory, opts *Options, name string) error {
	ctx := context.Background()

	// Connect to Docker
	client, err := docker.NewClient(ctx)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}
	defer client.Close()

	// Build create options
	createOpts := network.CreateOptions{
		Driver:     opts.Driver,
		Options:    parseDriverOpts(opts.DriverOpts),
		Labels:     parseLabels(opts.Labels),
		Internal:   opts.Internal,
		EnableIPv6: &opts.IPv6,
		Attachable: opts.Attachable,
	}

	// Create the network
	resp, err := client.NetworkCreate(ctx, name, createOpts)
	if err != nil {
		cmdutil.HandleError(err)
		return err
	}

	// Print the network ID
	fmt.Println(resp.ID)
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
