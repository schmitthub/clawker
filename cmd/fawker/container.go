package main

import (
	"context"
	"fmt"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/cmd/container"
	"github.com/schmitthub/clawker/internal/cmd/container/run"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/cobra"
)

// newFawkerContainerCmd builds the container command tree with a fawker-specific
// run function that shows initialization progress as a tree display instead of
// exercising the real PTY/attach path. Other subcommands use their real implementations.
func newFawkerContainerCmd(f *cmdutil.Factory) *cobra.Command {
	containerCmd := container.NewCmdContainer(f)

	// Find and remove the default "run" subcommand (uses real attachThenStart).
	for _, sub := range containerCmd.Commands() {
		if sub.Name() == "run" {
			containerCmd.RemoveCommand(sub)
			break
		}
	}

	// Add fawker-specific run subcommand with initialization progress display.
	containerCmd.AddCommand(run.NewCmdRun(f, fawkerRunF))

	return containerCmd
}

// fawkerRunF is the fawker-specific run function that replaces the real runRun.
// Instead of workspace setup, host proxy, credentials, and PTY attachment, it:
//  1. Shows initialization progress as a tree display via TUI.RunProgress
//  2. Exercises key Docker client calls (resolve, create, start) through the fakes
//  3. Simulates a brief container session for interactive mode
func fawkerRunF(ctx context.Context, opts *run.RunOptions) error {
	ios := opts.IOStreams
	cs := ios.ColorScheme()
	containerOpts := opts.ContainerOptions
	cfgGateway := opts.Config()
	cfg := cfgGateway.Project

	// --- Phase 1: Initialization progress tree ---

	ch := make(chan tui.ProgressStep, 32)
	var initErr error

	go func() {
		defer close(ch)

		// Step 1: Connect to Docker
		sendStep(ch, "docker", "Connect to Docker", tui.StepRunning)
		client, err := opts.Client(ctx)
		if err != nil {
			sendStep(ch, "docker", "Connect to Docker", tui.StepError)
			initErr = fmt.Errorf("connecting to Docker: %w", err)
			return
		}
		sendStep(ch, "docker", "Connect to Docker", tui.StepComplete)
		sleep(80)

		// Step 2: Resolve image
		sendStep(ch, "image", "Resolve image", tui.StepRunning)
		if containerOpts.Image == "@" {
			resolvedImage, err := client.ResolveImageWithSource(ctx)
			if err != nil {
				sendStep(ch, "image", "Resolve image", tui.StepError)
				initErr = err
				return
			}
			if resolvedImage == nil {
				sendStep(ch, "image", "Resolve image", tui.StepError)
				initErr = fmt.Errorf("no image found — run 'clawker build' or set default_image")
				return
			}
			containerOpts.Image = resolvedImage.Reference
		}
		sendStep(ch, "image", fmt.Sprintf("Resolve image (%s)", containerOpts.Image), tui.StepComplete)
		sleep(60)

		// Step 3: Check image exists
		sendStep(ch, "check", "Verify image available", tui.StepRunning)
		exists, err := client.ImageExists(ctx, containerOpts.Image)
		if err != nil || !exists {
			sendStep(ch, "check", "Verify image available", tui.StepError)
			if err != nil {
				initErr = fmt.Errorf("checking image: %w", err)
			} else {
				initErr = fmt.Errorf("image %q not found — run 'clawker build'", containerOpts.Image)
			}
			return
		}
		sendStep(ch, "check", "Verify image available", tui.StepComplete)
		sleep(50)

		// Step 4: Resolve agent name
		agentName := containerOpts.GetAgentName()
		if agentName == "" {
			agentName = docker.GenerateRandomName()
		}
		opts.AgentName = agentName
		containerName := docker.ContainerName(cfg.Project, agentName)

		// Step 5: Prepare workspace
		sendStep(ch, "workspace", "Prepare workspace (bind mode)", tui.StepRunning)
		sleep(120)
		sendStep(ch, "workspace", "Prepare workspace (bind mode)", tui.StepComplete)
		sleep(40)

		// Step 6: Config volume
		sendStep(ch, "config", "Initialize config volume", tui.StepRunning)
		sleep(100)
		sendStep(ch, "config", "Initialize config volume", tui.StepComplete)
		sleep(40)

		// Step 7: Create container
		sendStep(ch, "create", fmt.Sprintf("Create container (%s)", containerName), tui.StepRunning)
		resp, err := client.ContainerCreate(ctx, docker.ContainerCreateOptions{
			Config: &mobycontainer.Config{
				Image: containerOpts.Image,
				Tty:   containerOpts.TTY,
			},
			Name: containerName,
			ExtraLabels: docker.Labels{map[string]string{
				docker.LabelProject: cfg.Project,
				docker.LabelAgent:   agentName,
			}},
			EnsureNetwork: &docker.EnsureNetworkOptions{
				Name: docker.NetworkName,
			},
		})
		if err != nil {
			sendStep(ch, "create", fmt.Sprintf("Create container (%s)", containerName), tui.StepError)
			initErr = fmt.Errorf("creating container: %w", err)
			return
		}
		sendStep(ch, "create", fmt.Sprintf("Create container (%s)", containerName), tui.StepComplete)
		sleep(60)

		// Step 8: Start container
		containerID := resp.ID
		sendStep(ch, "start", "Start container", tui.StepRunning)
		if _, err := client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerID}); err != nil {
			sendStep(ch, "start", "Start container", tui.StepError)
			initErr = fmt.Errorf("starting container: %w", err)
			return
		}
		sendStep(ch, "start", "Start container", tui.StepComplete)

		if !opts.Detach {
			sleep(40)
			sendStep(ch, "attach", "Attach terminal", tui.StepRunning)
			sleep(80)
			sendStep(ch, "attach", "Attach terminal", tui.StepComplete)
		}
	}()

	result := opts.TUI.RunProgress("auto", tui.ProgressDisplayConfig{
		Title:          "Initializing",
		Subtitle:       cfg.Project,
		CompletionVerb: "Ready",
	}, ch)

	if result.Err != nil {
		return result.Err
	}
	if initErr != nil {
		return initErr
	}

	// --- Phase 2: Post-init output ---

	if opts.Detach {
		fmt.Fprintln(ios.Out, "sha256:fakec")
		return nil
	}

	// Simulated interactive container session for demo.
	fmt.Fprintf(ios.ErrOut, "\n%s Container session active\n\n",
		cs.SuccessIcon())
	fmt.Fprintf(ios.Out, "  %s\n", cs.Muted("$ whoami"))
	sleep(200)
	fmt.Fprintf(ios.Out, "  user\n")
	sleep(200)
	fmt.Fprintf(ios.Out, "  %s\n", cs.Muted("$ echo $CLAWKER_PROJECT"))
	sleep(200)
	fmt.Fprintf(ios.Out, "  %s\n", cfg.Project)
	sleep(300)
	fmt.Fprintf(ios.Out, "  %s\n", cs.Muted("$ exit"))
	fmt.Fprintf(ios.ErrOut, "\n%s Container exited with code %s\n",
		cs.SuccessIcon(), cs.Success("0"))

	return nil
}

// sendStep sends a progress step event to the channel.
func sendStep(ch chan<- tui.ProgressStep, id, name string, status tui.ProgressStepStatus) {
	ch <- tui.ProgressStep{
		ID:     id,
		Name:   name,
		Status: status,
	}
}

// sleep pauses for the given milliseconds. Used to create visual progression
// in fawker demo output. This is intentional for UAT — not production code.
func sleep(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}
