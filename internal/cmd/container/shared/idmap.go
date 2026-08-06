package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/idmap"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/sudo"
)

// rootlessSecurityOption is what a rootless daemon reports in its info
// SecurityOptions list. It is the daemon telling us about itself, which is
// the only trustworthy source — the CLI's own privileges say nothing about
// how the daemon runs.
const rootlessSecurityOption = "name=rootless"

// localDaemonScheme is the address scheme of a daemon on this machine. An
// ID-mapped view is a mount on the CLI's own host, which only helps a daemon
// that shares that host.
const localDaemonScheme = "unix://"

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

// readSubIDFiles reads the subordinate ID tables. A seam variable in the
// established test-seam shape: production always reads the real files, tests
// substitute contents so mapping outcomes do not depend on the host's
// /etc/subuid.
//
//nolint:gochecknoglobals // test seam, same contract as the manager package's seam block
var readSubIDFiles = func() (string, string, error) {
	uids, err := os.ReadFile(idmap.SubUIDFile)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", idmap.SubUIDFile, err)
	}
	gids, err := os.ReadFile(idmap.SubGIDFile)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", idmap.SubGIDFile, err)
	}
	return string(uids), string(gids), nil
}

// ensureIDMappedWorkspace makes the container's host binds usable on a
// rootless daemon, and is a no-op everywhere else. It returns the workspace
// roots it attached views for, which the create path stamps onto the
// container so every later start can re-check the same state.
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
// at reboot, so its absence is simply re-provisioned — one sudo prompt per
// workspace root (two in worktree mode), the first time after a boot. Docker
// re-resolves bind sources at every container start, which is why the start
// path re-checks the views (see ensureIDMappedViewsAtStart): a start after a
// reboot would otherwise bind the bare mount-point directory and hand the
// container an empty workspace.
func ensureIDMappedWorkspace(
	ctx context.Context,
	client *docker.Client,
	hostConfig *container.HostConfig,
	roots []string,
	ios *iostreams.IOStreams,
	log *logger.Logger,
) ([]string, error) {
	if runtime.GOOS != linuxOS {
		// An ID-mapped mount has to be made on the kernel the daemon runs
		// on. A CLI elsewhere (a Docker Desktop VM, say) cannot reach it —
		// and has no reason to, since that daemon runs as root.
		return nil, nil
	}
	roots = workspaceRoots(hostConfig, roots)
	if len(roots) == 0 {
		log.Debug().Msg("no host binds under a workspace root; ID-mapped views not needed")
		return nil, nil
	}

	rootless, err := daemonIsRootless(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("asking the daemon how it runs: %w", err)
	}
	if !rootless {
		log.Debug().Msg("daemon reports itself rootful; ID-mapped views not needed")
		return nil, nil
	}
	if host := client.DaemonHost(); !strings.HasPrefix(host, localDaemonScheme) {
		// The view would be mounted on THIS machine, from THIS machine's
		// subordinate ID tables — meaningless to a daemon running elsewhere.
		return nil, fmt.Errorf(
			"the daemon at %s runs rootless on another machine; "+
				"clawker can only attach the ID-mapped workspace view a rootless daemon needs on its own host",
			host)
	}

	viewBase, err := consts.IDMapSubdir()
	if err != nil {
		return nil, fmt.Errorf("resolving the ID-mapped view directory: %w", err)
	}

	for _, root := range roots {
		view, viewErr := ensureViewForRoot(ctx, root, viewBase, ios, log)
		if viewErr != nil {
			return nil, viewErr
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
	return roots, nil
}

// ensureIDMappedViewsAtStart re-establishes the views a container was created
// against, before Docker resolves its bind sources. The create path stamped
// the workspace roots as a label; each start recomputes the mapping and
// verifies the mounted view actually presents it, because the mounts die at
// reboot and the inputs (workspace owner, subordinate ranges) can drift
// between boots. Without this, a start after a reboot binds the bare
// mount-point directory and the container gets an empty workspace.
func ensureIDMappedViewsAtStart(
	ctx context.Context,
	client *docker.Client,
	containerName string,
	ios *iostreams.IOStreams,
	log *logger.Logger,
) error {
	if runtime.GOOS != linuxOS {
		return nil
	}

	inspect, err := client.ContainerInspect(ctx, containerName, docker.ContainerInspectOptions{Size: false})
	if err != nil {
		return fmt.Errorf("inspecting %s for ID-mapped workspace roots: %w", containerName, err)
	}
	if inspect.Container.Config == nil {
		return nil
	}
	labelValue := inspect.Container.Config.Labels[consts.LabelIDMapRoots]
	if labelValue == "" {
		return nil
	}

	var roots []string
	if err = json.Unmarshal([]byte(labelValue), &roots); err != nil {
		return fmt.Errorf("reading the %s label on %s: %w", consts.LabelIDMapRoots, containerName, err)
	}

	viewBase, err := consts.IDMapSubdir()
	if err != nil {
		return fmt.Errorf("resolving the ID-mapped view directory: %w", err)
	}
	for _, root := range roots {
		if _, err = ensureViewForRoot(ctx, root, viewBase, ios, log); err != nil {
			return err
		}
	}
	return nil
}

// ensureViewForRoot brings the view for one workspace root into a proven
// state: mounted and actually presenting the IDs the current mapping
// computes. An existing mount is trusted only after that check — a mount
// whose translation is stale (the helper failed mid-way once, the workspace
// owner changed, the subordinate ranges moved) is re-attached, which the
// helper does by detaching the old view first.
func ensureViewForRoot(
	ctx context.Context,
	root, viewBase string,
	ios *iostreams.IOStreams,
	log *logger.Logger,
) (string, error) {
	view := idmap.ViewPath(viewBase, root)
	mapping, err := workspaceMapping(root)
	if err != nil {
		return "", err
	}

	if idmap.Mounted(view) {
		uid, gid, statErr := pathOwner(view)
		if statErr == nil && uid == mapping.ToUID && gid == mapping.ToGID {
			return view, nil
		}
		log.Debug().
			Str("view", view).
			Uint32("want_uid", mapping.ToUID).
			Uint32("want_gid", mapping.ToGID).
			Msg("mounted view does not present the current mapping; re-attaching")
	}

	if err = attachIDMappedView(ctx, root, view, mapping, ios, log); err != nil {
		return "", err
	}
	return view, nil
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

// attachIDMappedView runs the elevated helper that attaches the view. The
// mount point is created here, unprivileged, because the helper refuses to
// create it — that way a run as root can never leave a root-owned directory
// where the CLI expects to manage its own state.
func attachIDMappedView(
	ctx context.Context,
	workspaceRoot, view string,
	mapping idmap.Mapping,
	ios *iostreams.IOStreams,
	log *logger.Logger,
) error {
	if ios == nil || !ios.CanPrompt() {
		// Elevation means a sudo prompt, which needs a person at a terminal.
		// There is nothing to instruct a headless run to do — the remedy IS
		// an interactive run — so the error just names the situation.
		return idmapUnavailableError(workspaceRoot, nil)
	}
	if err := os.MkdirAll(view, viewDirMode); err != nil {
		return fmt.Errorf("creating the ID-mapped view directory: %w", err)
	}

	// The narration and the prompt land on the stream a caller's spinner may
	// still be animating over.
	ios.StopSpinner()
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

	if err := sudo.RunElevated(ctx, ios, sudo.ElevatedHelper{
		Name:   idmapHelperName,
		Binary: IDMapMountBinary,
		Args: []string{
			workspaceRoot, view,
			idmap.FormatIDPair(mapping.FromUID, mapping.ToUID),
			idmap.FormatIDPair(mapping.FromGID, mapping.ToGID),
		},
	}); err != nil {
		return idmapUnavailableError(workspaceRoot, err)
	}
	return nil
}

// idmapUnavailableError reports that the workspace cannot reach the container
// usably. The remedy is an interactive run, so that is what the error says —
// there is no command to hand a headless operator, because attaching the view
// takes a sudo prompt.
func idmapUnavailableError(workspaceRoot string, cause error) error {
	msg := fmt.Sprintf(
		"the daemon runs rootless, so %s reaches the container owned by root and the agent cannot use it; "+
			"attaching the ID-mapped view that fixes this needs a sudo prompt — "+
			"run this once from an interactive terminal (once per boot)",
		workspaceRoot)
	if cause != nil {
		return fmt.Errorf("%s: %w", msg, cause)
	}
	return fmt.Errorf("%s", msg)
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
	subuid, subgid, err := readSubIDFiles()
	if err != nil {
		return idmap.Mapping{}, err
	}

	//nolint:gosec // a uid from the OS is never negative
	mapping, err := idmap.ComputeMapping(idmap.MappingInputs{
		OwnerUID: ownerUID,
		OwnerGID: ownerGID,
		UserName: current.Username,
		UserUID:  uint32(os.Getuid()),
		Subuid:   subuid,
		Subgid:   subgid,
	})
	if err != nil {
		return idmap.Mapping{}, fmt.Errorf("computing the workspace ID mapping: %w", err)
	}
	return mapping, nil
}
