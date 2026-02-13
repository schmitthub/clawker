package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	containershared "github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/spf13/pflag"
)

// containerHomeDir is the home directory for the claude user inside containers.
const containerHomeDir = "/home/claude"

// LoopContainerConfig holds all inputs needed to set up a container for loop execution.
type LoopContainerConfig struct {
	// Client is the Docker client.
	Client *docker.Client

	// Config is the gateway config providing Project(), Settings(), etc.
	Config *config.Config

	// LoopOpts holds the shared loop flags (agent, image, worktree, etc.).
	LoopOpts *LoopOptions

	// Flags is the command's pflag.FlagSet for Changed() detection.
	Flags *pflag.FlagSet

	// Version is the clawker build version.
	Version string

	// GitManager returns the git manager.
	GitManager func() (*git.GitManager, error)

	// HostProxy returns the host proxy service.
	HostProxy func() hostproxy.HostProxyService

	// SocketBridge returns the socket bridge manager.
	SocketBridge func() socketbridge.SocketBridgeManager

	// IOStreams is the I/O streams for spinner output during creation.
	IOStreams *iostreams.IOStreams
}

// LoopContainerResult holds outputs from container setup.
type LoopContainerResult struct {
	// ContainerID is the Docker container ID.
	ContainerID string

	// ContainerName is the full container name (clawker.project.agent).
	ContainerName string

	// AgentName is the resolved agent name.
	AgentName string

	// Project is the project name.
	Project string
}

// SetupLoopContainer creates, configures, and starts a container for loop execution.
// It handles: image resolution, container creation, hook injection, container start,
// and socket bridge setup. Returns a result and a cleanup function that stops and
// removes the container.
//
// The cleanup function uses context.Background() so it runs even after cancellation.
func SetupLoopContainer(ctx context.Context, cfg *LoopContainerConfig) (*LoopContainerResult, func(), error) {
	ios := cfg.IOStreams
	projectCfg := cfg.Config.Project

	// --- Phase A: Image resolution ---
	image := cfg.LoopOpts.Image
	if image == "" || image == "@" {
		resolvedImage, err := cfg.Client.ResolveImageWithSource(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving image: %w", err)
		}
		if resolvedImage == nil {
			cs := ios.ColorScheme()
			fmt.Fprintf(ios.ErrOut, "%s No image specified and no default image configured\n", cs.FailureIcon())
			fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
			fmt.Fprintln(ios.ErrOut, "  1. Specify an image: clawker loop iterate --image IMAGE ...")
			fmt.Fprintln(ios.ErrOut, "  2. Set default_image in clawker.yaml")
			fmt.Fprintln(ios.ErrOut, "  3. Set default_image in ~/.local/clawker/settings.yaml")
			fmt.Fprintln(ios.ErrOut, "  4. Build a project image: clawker build")
			return nil, nil, fmt.Errorf("no image available")
		}

		if resolvedImage.Source == docker.ImageSourceDefault {
			exists, err := cfg.Client.ImageExists(ctx, resolvedImage.Reference)
			if err != nil {
				return nil, nil, fmt.Errorf("checking if image exists: %w", err)
			}
			if !exists {
				return nil, nil, fmt.Errorf("default image %q not found — build it first with: clawker build", resolvedImage.Reference)
			}
		}

		image = resolvedImage.Reference
	}

	// --- Phase B: Create container with spinner ---
	containerOpts := containershared.NewContainerOptions()
	containerOpts.Agent = cfg.LoopOpts.Agent
	containerOpts.Image = image
	containerOpts.Worktree = cfg.LoopOpts.Worktree
	// Loop containers don't need stdin/TTY — they use docker exec for claude -p
	containerOpts.Stdin = false
	containerOpts.TTY = false

	events := make(chan containershared.CreateContainerEvent, 16)
	type outcome struct {
		result *containershared.CreateContainerResult
		err    error
	}
	done := make(chan outcome, 1)

	go func() {
		defer close(events)
		r, err := containershared.CreateContainer(ctx, &containershared.CreateContainerConfig{
			Client:      cfg.Client,
			Config:      projectCfg,
			Options:     containerOpts,
			Flags:       cfg.Flags,
			Version:     cfg.Version,
			GitManager:  cfg.GitManager,
			HostProxy:   cfg.HostProxy,
			Is256Color:  ios.Is256ColorSupported(),
			IsTrueColor: ios.IsTrueColorSupported(),
		}, events)
		done <- outcome{r, err}
	}()

	var warnings []string
	for ev := range events {
		switch {
		case ev.Type == containershared.MessageWarning:
			warnings = append(warnings, ev.Message)
		case ev.Status == containershared.StepRunning:
			ios.StartSpinner(ev.Message)
		}
	}
	ios.StopSpinner()

	o := <-done
	if o.err != nil {
		return nil, nil, fmt.Errorf("creating container: %w", o.err)
	}

	cs := ios.ColorScheme()
	for _, w := range warnings {
		fmt.Fprintf(ios.ErrOut, "%s %s\n", cs.WarningIcon(), w)
	}

	containerID := o.result.ContainerID
	containerName := o.result.ContainerName
	agentName := o.result.AgentName

	// Build cleanup function (stop + remove container and associated volumes).
	// Uses context.Background() because cleanup must run even after ctx cancellation.
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := cfg.Client.RemoveContainerWithVolumes(cleanupCtx, containerID, true); err != nil {
			logger.Warn().Err(err).Str("container", containerName).Msg("failed to clean up loop container")
		} else {
			logger.Debug().Str("container", containerName).Msg("cleaned up loop container")
		}
	}

	// --- Phase C: Inject hooks ---
	ios.StartSpinner("Injecting loop hooks")
	if err := InjectLoopHooks(ctx, containerID, cfg.LoopOpts.HooksFile, containershared.NewCopyToContainerFn(cfg.Client)); err != nil {
		ios.StopSpinner()
		cleanup()
		return nil, nil, fmt.Errorf("injecting hooks: %w", err)
	}
	ios.StopSpinner()

	// --- Phase D: Start container ---
	ios.StartSpinner("Starting container")
	if _, err := cfg.Client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerID}); err != nil {
		ios.StopSpinner()
		cleanup()
		return nil, nil, fmt.Errorf("starting container: %w", err)
	}
	ios.StopSpinner()

	// Start socket bridge for GPG/SSH forwarding if needed.
	if containershared.NeedsSocketBridge(projectCfg) && cfg.SocketBridge != nil {
		gpgEnabled := projectCfg.Security.GitCredentials != nil && projectCfg.Security.GitCredentials.GPGEnabled()
		if err := cfg.SocketBridge().EnsureBridge(containerID, gpgEnabled); err != nil {
			logger.Warn().Err(err).Msg("failed to start socket bridge for loop container")
		}
	}

	return &LoopContainerResult{
		ContainerID:   containerID,
		ContainerName: containerName,
		AgentName:     agentName,
		Project:       projectCfg.Project,
	}, cleanup, nil
}

// InjectLoopHooks injects hook configuration and scripts into a created (not started) container.
// If hooksFile is empty, default hooks are used. If provided, the file is read as a
// complete replacement. Hook scripts referenced by default hooks are also injected.
func InjectLoopHooks(ctx context.Context, containerID string, hooksFile string, copyFn containershared.CopyToContainerFn) error {
	hooks, hookFiles, err := loop.ResolveHooks(hooksFile)
	if err != nil {
		return err
	}

	// Write settings.json with hooks config to the container's .claude/ directory.
	settingsJSON, err := hooks.MarshalSettingsJSON()
	if err != nil {
		return fmt.Errorf("marshaling hook settings: %w", err)
	}

	settingsTar, err := buildSettingsTar(settingsJSON)
	if err != nil {
		return fmt.Errorf("building settings tar: %w", err)
	}

	if err := copyFn(ctx, containerID, containerHomeDir+"/.claude", settingsTar); err != nil {
		return fmt.Errorf("injecting settings.json: %w", err)
	}

	// Write hook script files to their absolute paths in the container.
	if len(hookFiles) > 0 {
		scriptsTar, err := buildHookFilesTar(hookFiles)
		if err != nil {
			return fmt.Errorf("building hook scripts tar: %w", err)
		}

		if err := copyFn(ctx, containerID, "/", scriptsTar); err != nil {
			return fmt.Errorf("injecting hook scripts: %w", err)
		}
	}

	shortID := containerID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	logger.Debug().Str("containerID", shortID).Msg("injected loop hooks into container")
	return nil
}

// buildSettingsTar creates a tar archive containing settings.json with the given content.
// The file is owned by the container user (UID/GID 1001).
func buildSettingsTar(content []byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	hdr := &tar.Header{
		Name:    "settings.json",
		Mode:    0o644,
		Size:    int64(len(content)),
		Uid:     config.ContainerUID,
		Gid:     config.ContainerGID,
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, fmt.Errorf("write tar header: %w", err)
	}
	if _, err := tw.Write(content); err != nil {
		return nil, fmt.Errorf("write tar content: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}

	return &buf, nil
}

// buildHookFilesTar creates a tar archive containing hook script files.
// Keys are absolute paths inside the container; values are file contents.
// Directories are created as needed. Files are owned by root (scripts in /tmp).
func buildHookFilesTar(files map[string][]byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()

	// Track directories we've created
	dirs := make(map[string]bool)

	for path, content := range files {
		// Create parent directories
		dir := filepath.Dir(path)
		if dir != "/" && dir != "." && !dirs[dir] {
			dirHdr := &tar.Header{
				Typeflag: tar.TypeDir,
				// Trim leading "/" — tar paths inside a "/" dest are relative
				Name:    dir[1:] + "/",
				Mode:    0o755,
				ModTime: now,
			}
			if err := tw.WriteHeader(dirHdr); err != nil {
				return nil, fmt.Errorf("write dir header for %s: %w", dir, err)
			}
			dirs[dir] = true
		}

		hdr := &tar.Header{
			// Trim leading "/" — tar paths inside a "/" dest are relative
			Name:    path[1:],
			Mode:    0o755,
			Size:    int64(len(content)),
			ModTime: now,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", path, err)
		}
		if _, err := tw.Write(content); err != nil {
			return nil, fmt.Errorf("write tar content for %s: %w", path, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}

	return &buf, nil
}
