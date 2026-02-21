package shared

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/spf13/pflag"
)

// containerHomeDir is the home directory for the claude user inside containers.
const containerHomeDir = "/home/claude"

const (
	// ReadyLogPrefix is the prefix for the ready signal log line.
	ReadyLogPrefix = "[clawker] ready"

	// ErrorLogPrefix is the prefix for error signal log lines.
	ErrorLogPrefix = "[clawker] error"
)

// LoopContainerConfig holds all inputs needed to set up a container for loop execution.
type LoopContainerConfig struct {
	// Client is the Docker client.
	Client *docker.Client

	Command []string

	// Config is the config.Config interface providing Project(), Settings(), etc.
	Config config.Config

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

	// ProjectCfg is the project name.
	ProjectCfg *config.Project

	// WorkDir is the host working directory for this session.
	WorkDir string
}

// ResolveLoopImage resolves the container image for loop execution.
// If an image is explicitly set on loopOpts, it's returned as-is.
// Otherwise, the Docker client's image resolution chain is used.
func ResolveLoopImage(ctx context.Context, client *docker.Client, ios *iostreams.IOStreams, loopOpts *LoopOptions) (string, error) {
	image := loopOpts.Image
	if image != "" && image != "@" {
		return image, nil
	}

	resolvedImage, err := client.ResolveImageWithSource(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving image: %w", err)
	}
	if resolvedImage == nil {
		cs := ios.ColorScheme()
		fmt.Fprintf(ios.ErrOut, "%s No image specified and no default image configured\n", cs.FailureIcon())
		fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
		fmt.Fprintln(ios.ErrOut, "  1. Specify an image: clawker loop iterate --image IMAGE ...")
		fmt.Fprintln(ios.ErrOut, "  2. Set default_image in clawker.yaml")
		fmt.Fprintln(ios.ErrOut, "  3. Set default_image in ~/.local/clawker/settings.yaml")
		fmt.Fprintln(ios.ErrOut, "  4. Build a project image: clawker build")
		return "", fmt.Errorf("no image available")
	}

	if resolvedImage.Source == docker.ImageSourceDefault {
		exists, err := client.ImageExists(ctx, resolvedImage.Reference)
		if err != nil {
			return "", fmt.Errorf("checking if image exists: %w", err)
		}
		if !exists {
			return "", fmt.Errorf("default image %q not found — build it first with: clawker build", resolvedImage.Reference)
		}
	}

	return resolvedImage.Reference, nil
}

// MakeCreateContainerFunc creates a factory closure that creates a new container
// for each loop iteration. The returned containers are created with hooks injected
// but NOT started — the Runner's StartContainer handles attachment and start.
func MakeCreateContainerFunc(cfg *LoopContainerConfig) func(context.Context) (*ContainerStartConfig, error) {
	return func(ctx context.Context) (*ContainerStartConfig, error) {
		containerOpts := containershared.NewContainerOptions()
		containerOpts.Agent = cfg.LoopOpts.Agent
		containerOpts.Image = cfg.LoopOpts.Image
		containerOpts.Worktree = cfg.LoopOpts.Worktree
		containerOpts.Command = cfg.Command
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
				Cfg:         cfg.Config,
				Config:      cfg.Config.Project(),
				Options:     containerOpts,
				Flags:       cfg.Flags,
				Version:     cfg.Version,
				GitManager:  cfg.GitManager,
				HostProxy:   cfg.HostProxy,
				Logger:      cfg.IOStreams.Logger,
				Is256Color:  cfg.IOStreams.Is256ColorSupported(),
				IsTrueColor: cfg.IOStreams.IsTrueColorSupported(),
			}, events)
			done <- outcome{r, err}
		}()

		// Drain events (runner is in a goroutine — no spinner available)
		for range events {
		}

		o := <-done
		if o.err != nil {
			return nil, fmt.Errorf("creating iteration container: %w", o.err)
		}

		containerID := o.result.ContainerID

		// Inject hooks into the container
		if err := InjectLoopHooks(ctx, cfg.Config, containerID, cfg.LoopOpts.HooksFile, containershared.NewCopyToContainerFn(cfg.Client), containershared.NewCopyFromContainerFn(cfg.Client), cfg.IOStreams.Logger); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = cfg.Client.RemoveContainerWithVolumes(cleanupCtx, containerID, true)
			return nil, fmt.Errorf("injecting hooks: %w", err)
		}

		cleanup := func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := cfg.Client.RemoveContainerWithVolumes(cleanupCtx, containerID, true); err != nil {
				shortID := containerID
				if len(shortID) > 12 {
					shortID = shortID[:12]
				}
				cfg.IOStreams.Logger.Warn().Err(err).Str("container", shortID).Msg("failed to clean up iteration container")
			}
		}

		return &ContainerStartConfig{
			ContainerID: containerID,
			Cleanup:     cleanup,
		}, nil
	}
}

// SetupLoopContainer creates, configures, and starts a container for loop execution.
//
// Deprecated: Use ResolveLoopImage + MakeCreateContainerFunc for per-iteration containers.
//
// The cleanup function uses context.Background() so it runs even after cancellation.
func SetupLoopContainer(ctx context.Context, cfg *LoopContainerConfig) (*LoopContainerResult, func(), error) {
	ios := cfg.IOStreams
	projectCfg := cfg.Config.Project()

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
	containerOpts.Command = cfg.Command
	// Loop containers don't need stdin/TTY
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
			Logger:      ios.Logger,
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
			ios.Logger.Warn().Err(err).Str("container", containerName).Msg("failed to clean up loop container")
			fmt.Fprintf(ios.ErrOut, "%s Failed to clean up container %s: %v\n",
				cs.WarningIcon(), containerName, err)
		} else {
			ios.Logger.Debug().Str("container", containerName).Msg("cleaned up loop container")
		}
	}

	// --- Phase C: Inject hooks ---
	ios.StartSpinner("Injecting loop hooks")
	if err := InjectLoopHooks(ctx, cfg.Config, containerID, cfg.LoopOpts.HooksFile, containershared.NewCopyToContainerFn(cfg.Client), containershared.NewCopyFromContainerFn(cfg.Client), ios.Logger); err != nil {
		ios.StopSpinner()
		cleanup()
		return nil, nil, fmt.Errorf("injecting hooks: %w", err)
	}
	ios.StopSpinner()

	// --- Phase D: Start container ---
	ios.StartSpinner("Starting loop container")
	if _, err := cfg.Client.ContainerStart(ctx, docker.ContainerStartOptions{ContainerID: containerID}); err != nil {
		ios.StopSpinner()
		cleanup()
		return nil, nil, fmt.Errorf("starting loop container: %w", err)
	}
	ios.StopSpinner()

	return &LoopContainerResult{
		ContainerID:   containerID,
		ContainerName: containerName,
		AgentName:     agentName,
		ProjectCfg:    projectCfg,
		WorkDir:       o.result.WorkDir,
	}, cleanup, nil
}

// InjectLoopHooks injects hook configuration and scripts into a created (not started) container.
// If hooksFile is empty, default hooks are used. If provided, the file is read as a
// complete replacement. Hook scripts referenced by default hooks are also injected.
//
// Hooks are merged into the existing settings.json to preserve any pre-existing
// configuration (MCP servers, skills, extensions) set up by containerfs or post-init.
func InjectLoopHooks(ctx context.Context, cfgGateway config.Config, containerID string, hooksFile string, copyFn containershared.CopyToContainerFn, readFn containershared.CopyFromContainerFn, log iostreams.Logger) error {
	hooks, hookFiles, err := ResolveHooks(hooksFile)
	if err != nil {
		return err
	}

	// Read existing settings.json from the container so we can merge hooks
	// into it rather than replacing it entirely. This preserves MCP servers,
	// skills, and extensions set up by containerfs or the init process.
	existing := readExistingSettings(ctx, containerID, readFn, log)

	// Add hooks to the existing settings.
	existing["hooks"] = hooks

	settingsJSON, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling merged settings: %w", err)
	}

	settingsTar, err := buildSettingsTar(cfgGateway, settingsJSON)
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
	log.Debug().Str("containerID", shortID).Msg("injected loop hooks into container")
	return nil
}

// readExistingSettings reads and parses the existing settings.json from the container.
// Returns an empty map if the file doesn't exist or can't be read (non-fatal).
func readExistingSettings(ctx context.Context, containerID string, readFn containershared.CopyFromContainerFn, log iostreams.Logger) map[string]any {
	if readFn == nil {
		return make(map[string]any)
	}

	settingsPath := containerHomeDir + "/.claude/settings.json"
	rc, err := readFn(ctx, containerID, settingsPath)
	if err != nil {
		log.Debug().Err(err).Msg("no existing settings.json to merge (will create fresh)")
		return make(map[string]any)
	}
	defer rc.Close()

	// CopyFromContainer returns a tar stream — extract settings.json from it.
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if filepath.Base(hdr.Name) == "settings.json" {
			data, err := io.ReadAll(tr)
			if err != nil {
				log.Debug().Err(err).Msg("failed to read settings.json from tar")
				return make(map[string]any)
			}
			var settings map[string]any
			if err := json.Unmarshal(data, &settings); err != nil {
				log.Debug().Err(err).Msg("failed to parse existing settings.json")
				return make(map[string]any)
			}
			log.Debug().Int("keys", len(settings)).Msg("loaded existing settings.json for hook merge")
			return settings
		}
	}

	return make(map[string]any)
}

// buildSettingsTar creates a tar archive containing settings.json with the given content.
// The file is owned by the container user (UID/GID 1001).
func buildSettingsTar(cfg config.Config, content []byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	hdr := &tar.Header{
		Name:    "settings.json",
		Mode:    0o644,
		Size:    int64(len(content)),
		Uid:     cfg.ContainerUID(),
		Gid:     cfg.ContainerGID(),
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
