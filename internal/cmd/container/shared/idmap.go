package shared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/idmap"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

// rootlessSecurityOption is what a rootless daemon reports in its info
// SecurityOptions list. It is the daemon telling us about itself, which is
// the only trustworthy source — the CLI's own privileges say nothing about
// how the daemon runs.
const rootlessSecurityOption = "name=rootless"

// idmapHelperName is what the elevated helper is called on disk while it
// runs. It shows up in the sudo prompt and the audit log, so it says what it
// does.
const idmapHelperName = consts.NamePrefix + "-idmap-mount"

// viewDirMode is the mount point's own mode. Only the owner traverses it —
// what the container sees is the ID-mapped filesystem attached on top, whose
// permissions come from the workspace itself.
const viewDirMode = 0o700

// linuxOS is the only platform where an ID-mapped mount can be made.
const linuxOS = "linux"

// ensureIDMappedWorkspace makes the container's host binds usable on a
// rootless daemon, and is a no-op everywhere else.
//
// A rootless daemon's user namespace maps the invoking user to container
// root, so a bind-mounted workspace arrives root-owned inside the container
// and the unprivileged clawker user cannot read or write it. Docker exposes
// no per-mount ID mapping, so clawker provisions one itself: an ID-mapped
// bind of the workspace root, attached once at a clawker-owned path, that
// presents the owner's files as the IDs the container's clawker user
// occupies. Every bind source at or under the workspace root is then pointed
// at the corresponding path inside that view — including the user's own -v
// binds, which hit exactly the same wall.
//
// The host keeps using the real path with its ownership untouched, and files
// written from either side land owned by the right identity on both.
//
// The view is state, not configuration: it survives daemon restarts and dies
// at reboot, so its absence is simply re-provisioned. Attaching it needs
// init-namespace CAP_SYS_ADMIN — hence one sudo prompt, the first time a
// container is created after a reboot.
func ensureIDMappedWorkspace(
	ctx context.Context,
	client *docker.Client,
	hostConfig *container.HostConfig,
	roots []string,
	ios *iostreams.IOStreams,
	log *logger.Logger,
) error {
	if runtime.GOOS != linuxOS {
		// An ID-mapped mount has to be made on the kernel the daemon runs
		// on. A CLI elsewhere (a Docker Desktop VM, say) cannot reach it —
		// and has no reason to, since that daemon runs as root.
		return nil
	}
	roots = workspaceRoots(hostConfig, roots)
	if len(roots) == 0 {
		return nil
	}

	rootless, err := daemonIsRootless(ctx, client)
	if err != nil {
		return fmt.Errorf("asking the daemon how it runs: %w", err)
	}
	if !rootless {
		return nil
	}

	viewBase, err := consts.IDMapSubdir()
	if err != nil {
		return fmt.Errorf("resolving the ID-mapped view directory: %w", err)
	}

	for _, root := range roots {
		view := idmap.ViewPath(viewBase, root)
		if !idmap.Mounted(view) {
			if err = attachIDMappedView(ctx, root, view, ios, log); err != nil {
				return err
			}
		}

		mounts, rewrittenMounts := idmap.RewriteMounts(hostConfig.Mounts, root, view)
		binds, rewrittenBinds := idmap.RewriteBinds(hostConfig.Binds, root, view)
		hostConfig.Mounts = mounts
		hostConfig.Binds = binds

		log.Debug().
			Str("workspace", root).
			Str("view", view).
			Int("mounts", rewrittenMounts).
			Int("binds", rewrittenBinds).
			Msg("repointed host binds at the ID-mapped workspace view")
	}
	return nil
}

// workspaceRoots reduces the candidate roots to the ones this container
// actually binds something under, deepest first.
//
// Deepest first is what keeps a worktree living inside its own repository
// honest: the worktree's own view claims its paths before the repository
// root sees them, and the repository view then only picks up what is left
// (the git directory it is there for). A snapshot workspace binds nothing
// under any root and drops out here, as does a container whose only host
// binds live elsewhere on the filesystem.
func workspaceRoots(hostConfig *container.HostConfig, candidates []string) []string {
	var roots []string
	seen := make(map[string]struct{}, len(candidates))
	for _, root := range candidates {
		if root == "" {
			continue
		}
		if _, dup := seen[root]; dup {
			continue
		}
		seen[root] = struct{}{}
		if _, mounts := idmap.RewriteMounts(hostConfig.Mounts, root, ""); mounts > 0 {
			roots = append(roots, root)
			continue
		}
		if _, binds := idmap.RewriteBinds(hostConfig.Binds, root, ""); binds > 0 {
			roots = append(roots, root)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) > len(roots[j]) })
	return roots
}

// daemonIsRootless asks the daemon whether it runs rootless. This is a state
// question about the daemon, not a guess from the CLI's own environment: the
// two can differ (a remote daemon, a rootful daemon addressed by a
// non-privileged user), and only the daemon's answer decides whether bind
// ownership needs translating.
func daemonIsRootless(ctx context.Context, client *docker.Client) (bool, error) {
	info, err := client.Info(ctx, mobyclient.InfoOptions{})
	if err != nil {
		return false, fmt.Errorf("querying the daemon: %w", err)
	}
	for _, opt := range info.Info.SecurityOptions {
		if strings.Contains(opt, rootlessSecurityOption) {
			return true, nil
		}
	}
	return false, nil
}

// attachIDMappedView computes the mapping and runs the elevated helper that
// attaches the view. The mount point is created here, unprivileged, because
// the helper refuses to create it — that way a run as root can never leave a
// root-owned directory where the CLI expects to manage its own state.
func attachIDMappedView(
	ctx context.Context,
	workspaceRoot, view string,
	ios *iostreams.IOStreams,
	log *logger.Logger,
) error {
	mapping, err := workspaceMapping(workspaceRoot)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(view, viewDirMode); err != nil {
		return fmt.Errorf("creating the ID-mapped view directory: %w", err)
	}

	if ios == nil || !ios.CanPrompt() {
		// Elevation means running something privileged, which is only
		// reasonable with a human present to authorize it. Say what is
		// needed and let the caller's operator run it.
		return idmapUnavailableError(workspaceRoot, view, mapping, nil)
	}

	cs := ios.ColorScheme()
	fmt.Fprintf(ios.ErrOut, "%s This daemon runs rootless, so %s reaches the container root-owned.\n",
		cs.WarningIcon(), workspaceRoot)
	fmt.Fprintf(ios.ErrOut, "%s Mapping it for the container user needs sudo (once per boot).\n",
		cs.InfoIcon())

	log.Debug().
		Str("workspace", workspaceRoot).
		Str("view", view).
		Uint32("uid_to", mapping.ToUID).
		Uint32("gid_to", mapping.ToGID).
		Msg("attaching ID-mapped workspace view")

	if err = cmdutil.RunElevated(ctx, ios, cmdutil.ElevatedHelper{
		Name:   idmapHelperName,
		Binary: IDMapMountBinary,
		Args: []string{
			workspaceRoot, view,
			idmap.FormatIDPair(mapping.FromUID, mapping.ToUID),
			idmap.FormatIDPair(mapping.FromGID, mapping.ToGID),
		},
	}); err != nil {
		return idmapUnavailableError(workspaceRoot, view, mapping, err)
	}
	return nil
}

// idmapUnavailableError explains what could not be done and hands over the
// exact command that does it, so a headless run is actionable rather than
// merely failed.
func idmapUnavailableError(workspaceRoot, view string, mapping idmap.Mapping, cause error) error {
	msg := fmt.Sprintf(
		"the daemon runs rootless, so %s reaches the container owned by root and the agent cannot use it.\n\n"+
			"Mapping it for the container user needs one elevated command:\n\n"+
			"    sudo %s %s %s %s %s\n",
		workspaceRoot, idmapHelperName, workspaceRoot, view,
		idmap.FormatIDPair(mapping.FromUID, mapping.ToUID),
		idmap.FormatIDPair(mapping.FromGID, mapping.ToGID))
	if cause != nil {
		return fmt.Errorf("%s\n%w", msg, cause)
	}
	return errors.New(msg)
}

// workspaceMapping resolves the ID pairs for the workspace: the owner IDs on
// disk, and the kernel IDs those same IDs occupy inside the daemon's user
// namespace.
//
// The container's clawker user carries the host user's uid (the image bakes
// it that way), so the workspace owner's IDs are also the container-side IDs
// the rootless formula translates. The daemon user is the invoking user: a
// rootless daemon belongs to whoever started it, and clawker talks to it as
// that person.
func workspaceMapping(workspaceRoot string) (idmap.Mapping, error) {
	ownerUID, ownerGID, err := pathOwner(workspaceRoot)
	if err != nil {
		return idmap.Mapping{}, fmt.Errorf("reading the owner of %s: %w", workspaceRoot, err)
	}

	current, err := user.Current()
	if err != nil {
		return idmap.Mapping{}, fmt.Errorf("resolving the current user: %w", err)
	}
	subuid, err := os.ReadFile(idmap.SubUIDFile)
	if err != nil {
		return idmap.Mapping{}, fmt.Errorf("reading %s: %w", idmap.SubUIDFile, err)
	}
	subgid, err := os.ReadFile(idmap.SubGIDFile)
	if err != nil {
		return idmap.Mapping{}, fmt.Errorf("reading %s: %w", idmap.SubGIDFile, err)
	}

	//nolint:gosec // a uid from the OS is never negative
	mapping, err := idmap.ComputeMapping(idmap.MappingInputs{
		OwnerUID: ownerUID,
		OwnerGID: ownerGID,
		UserName: current.Username,
		UserUID:  uint32(os.Getuid()),
		Subuid:   string(subuid),
		Subgid:   string(subgid),
	})
	if err != nil {
		return idmap.Mapping{}, fmt.Errorf("computing the workspace ID mapping: %w", err)
	}
	return mapping, nil
}
