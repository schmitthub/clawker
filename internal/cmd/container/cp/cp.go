// Package cp provides the container cp command.
package cp

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

// CpOptions holds options for the cp command.
type CpOptions struct {
	IOStreams *iostreams.IOStreams
	Client    func(context.Context) (*docker.Client, error)
	Config    func() *config.Config

	Agent      bool
	Archive    bool
	FollowLink bool
	CopyUIDGID bool

	Src string
	Dst string
}

// NewCmdCp creates a new cp command.
func NewCmdCp(f *cmdutil.Factory, runF func(context.Context, *CpOptions) error) *cobra.Command {
	opts := &CpOptions{
		IOStreams: f.IOStreams,
		Client:    f.Client,
		Config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "cp [OPTIONS] CONTAINER:SRC_PATH DEST_PATH\n  clawker container cp [OPTIONS] SRC_PATH CONTAINER:DEST_PATH",
		Short: "Copy files/folders between a container and the local filesystem",
		Long: `Copy files/folders between a container and the local filesystem.

Use '-' as the destination to write a tar archive of the container source
to stdout. Use '-' as the source to read a tar archive from stdin and
extract it to a directory destination in a container.

When --agent is provided, container names in CONTAINER:PATH are resolved
as agent names (clawker.<project>.<agent>).

Container path format: CONTAINER:PATH
Local path format: PATH`,
		Example: `  # Copy file from container using agent name
  clawker container cp --agent dev:/app/config.json ./config.json

  # Copy file to container using agent name
  clawker container cp --agent ./config.json dev:/app/config.json

  # Copy file from container by full name
  clawker container cp clawker.myapp.dev:/app/config.json ./config.json

  # Copy file from local to container
  clawker container cp ./config.json clawker.myapp.dev:/app/config.json

  # Copy directory from container to local
  clawker container cp --agent dev:/app/logs ./logs

  # Stream tar from container to stdout
  clawker container cp --agent dev:/app - > backup.tar`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Src = args[0]
			opts.Dst = args[1]
			if runF != nil {
				return runF(cmd.Context(), opts)
			}
			return cpRun(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Agent, "agent", false, "Treat container names as agent names (resolves to clawker.<project>.<agent>)")
	cmd.Flags().BoolVarP(&opts.Archive, "archive", "a", false, "Archive mode (copy all uid/gid information)")
	cmd.Flags().BoolVarP(&opts.FollowLink, "follow-link", "L", false, "Always follow symbol link in SRC_PATH")
	cmd.Flags().BoolVar(&opts.CopyUIDGID, "copy-uidgid", false, "Copy UID/GID from source to destination (same as -a)")

	return cmd
}

// parseContainerPath parses a container:path specification.
// Returns (container, path, isContainer).
func parseContainerPath(arg string) (string, string, bool) {
	// Check if this is a container path (contains :)
	// But we need to handle Windows paths like C:\path
	if strings.Contains(arg, ":") {
		parts := strings.SplitN(arg, ":", 2)
		if len(parts) == 2 {
			// Check if it looks like a Windows path (single letter before colon)
			if len(parts[0]) == 1 && (parts[0][0] >= 'A' && parts[0][0] <= 'Z' || parts[0][0] >= 'a' && parts[0][0] <= 'z') {
				// This is likely a Windows path, not a container path
				return "", arg, false
			}
			return parts[0], parts[1], true
		}
	}
	return "", arg, false
}

func cpRun(ctx context.Context, opts *CpOptions) error {
	// Parse source and destination
	srcContainer, srcPath, srcIsContainer := parseContainerPath(opts.Src)
	dstContainer, dstPath, dstIsContainer := parseContainerPath(opts.Dst)

	// If --agent is provided, resolve container names as agent names
	if opts.Agent {
		if srcIsContainer && srcContainer != "" {
			var err error
			srcContainer, err = docker.ContainerName(opts.Config().Resolution.ProjectKey, srcContainer)
			if err != nil {
				return err
			}
		}

		if dstIsContainer && dstContainer != "" {
			var err error
			dstContainer, err = docker.ContainerName(opts.Config().Resolution.ProjectKey, dstContainer)
			if err != nil {
				return err
			}
		}
	}

	// Validate that exactly one of src/dst is a container path
	if srcIsContainer && dstIsContainer {
		return fmt.Errorf("copying between containers is not supported")
	}
	if !srcIsContainer && !dstIsContainer {
		return fmt.Errorf("one of source or destination must be a container path (CONTAINER:PATH)")
	}

	// Connect to Docker
	client, err := opts.Client(ctx)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	if srcIsContainer {
		return copyFromContainer(ctx, client, srcContainer, srcPath, dstPath, opts)
	}
	return copyToContainer(ctx, client, dstContainer, srcPath, dstPath, opts)
}

func copyFromContainer(ctx context.Context, client *docker.Client, containerName, srcPath, dstPath string, opts *CpOptions) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", containerName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", containerName)
	}

	// Get tar archive from container
	copyResult, err := client.CopyFromContainer(ctx, c.ID, docker.CopyFromContainerOptions{SourcePath: srcPath})
	if err != nil {
		return fmt.Errorf("copying from container %q: %w", containerName, err)
	}
	defer copyResult.Content.Close()

	// If destination is stdout, just copy the tar
	if dstPath == "-" {
		if _, err := io.Copy(opts.IOStreams.Out, copyResult.Content); err != nil {
			return fmt.Errorf("streaming from container %q to stdout: %w", containerName, err)
		}
		return nil
	}

	// Extract tar to destination
	return extractTar(copyResult.Content, dstPath, copyResult.Stat.Name, opts)
}

func copyToContainer(ctx context.Context, client *docker.Client, containerName, srcPath, dstPath string, opts *CpOptions) error {
	// Find container by name
	c, err := client.FindContainerByName(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to find container %q: %w", containerName, err)
	}
	if c == nil {
		return fmt.Errorf("container %q not found", containerName)
	}

	// If source is stdin, read tar directly
	if srcPath == "-" {
		copyOpts := docker.CopyToContainerOptions{
			DestinationPath:           dstPath,
			Content:                   opts.IOStreams.In,
			AllowOverwriteDirWithFile: true,
			CopyUIDGID:                opts.Archive || opts.CopyUIDGID,
		}
		if _, err := client.CopyToContainer(ctx, c.ID, copyOpts); err != nil {
			return fmt.Errorf("copying to container %q: %w", containerName, err)
		}
		return nil
	}

	// Create tar archive from source
	tarReader, err := createTar(srcPath, opts)
	if err != nil {
		return err
	}

	// Copy to container
	copyOpts := docker.CopyToContainerOptions{
		DestinationPath:           dstPath,
		Content:                   tarReader,
		AllowOverwriteDirWithFile: true,
		CopyUIDGID:                opts.Archive || opts.CopyUIDGID,
	}
	if _, err = client.CopyToContainer(ctx, c.ID, copyOpts); err != nil {
		return fmt.Errorf("copying to container %q: %w", containerName, err)
	}
	return nil
}

// extractTar extracts a tar archive to a local path.
func extractTar(reader io.Reader, dstPath, _ string, _ *CpOptions) error {
	tr := tar.NewReader(reader)

	// Get info about destination
	dstInfo, err := os.Stat(dstPath)
	dstExists := err == nil
	dstIsDir := dstExists && dstInfo.IsDir()

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %w", err)
		}

		// Determine target path
		var target string
		if dstIsDir {
			target = filepath.Join(dstPath, header.Name)
		} else if dstExists {
			target = dstPath
		} else {
			// Destination doesn't exist - create it as file or directory
			// based on what we're extracting
			if header.Typeflag == tar.TypeDir {
				target = filepath.Join(dstPath, header.Name)
			} else {
				target = dstPath
			}
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", target, err)
			}
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("failed to write file %s: %w", target, err)
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("failed to create symlink %s: %w", target, err)
			}
		case tar.TypeLink:
			if err := os.Link(header.Linkname, target); err != nil {
				return fmt.Errorf("failed to create hard link %s: %w", target, err)
			}
		}
	}
	return nil
}

// createTar creates a tar archive from a local path.
func createTar(srcPath string, opts *CpOptions) (io.Reader, error) {
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("source path %q not found: %w", srcPath, err)
	}

	// Create a pipe for streaming tar
	pr, pw := io.Pipe()

	go func() {
		tw := tar.NewWriter(pw)
		defer func() {
			tw.Close()
			pw.Close()
		}()

		if srcInfo.IsDir() {
			err = filepath.Walk(srcPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				// Get relative path
				relPath, err := filepath.Rel(srcPath, path)
				if err != nil {
					return err
				}
				if relPath == "." {
					relPath = filepath.Base(srcPath)
				} else {
					relPath = filepath.Join(filepath.Base(srcPath), relPath)
				}

				return addToTar(tw, path, relPath, info, opts)
			})
		} else {
			err = addToTar(tw, srcPath, filepath.Base(srcPath), srcInfo, opts)
		}

		if err != nil {
			pw.CloseWithError(err)
		}
	}()

	return pr, nil
}

// addToTar adds a file/directory to a tar writer.
func addToTar(tw *tar.Writer, path, name string, info os.FileInfo, opts *CpOptions) error {
	// Handle symlinks
	link := ""
	if info.Mode()&os.ModeSymlink != 0 {
		var err error
		link, err = os.Readlink(path)
		if err != nil {
			return fmt.Errorf("failed to read symlink: %w", err)
		}
		if opts.FollowLink {
			// Follow the link
			realPath, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("failed to follow symlink: %w", err)
			}
			realInfo, err := os.Stat(realPath)
			if err != nil {
				return fmt.Errorf("failed to stat symlink target: %w", err)
			}
			info = realInfo
			path = realPath
		}
	}

	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return fmt.Errorf("failed to create tar header: %w", err)
	}
	header.Name = name

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header: %w", err)
	}

	// Write file content for regular files
	if info.Mode().IsRegular() {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("failed to copy file to tar: %w", err)
		}
	}

	return nil
}
