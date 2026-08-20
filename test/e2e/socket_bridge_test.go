//nolint:testpackage // the e2e suite shares production-wired harness helpers
package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/db"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

const (
	socketTestHarness       = "sockettest"
	socketTestSourceEnv     = "CLAWKER_E2E_SOCKET"
	socketTestSource        = "${" + socketTestSourceEnv + "}"
	socketTestTarget        = "/tmp/clawker-e2e.sock"
	socketTestPurpose       = "Verify the generic socket bridge."
	socketTestResponse      = "clawker-socket-ok"
	socketTestGroup         = "sockettest"
	socketTestMode          = "0660"
	socketTestMissingGroup  = "clawker-missing-group"
	socketTestStartedSuffix = ".started"
	socketTestApproveFlag   = "--" + cmdutil.FlagApproveGrants
	socketTestPrompt        = "Allow this socket bridge?"
	socketTestTempRoot      = "/tmp"
	socketTestTempPrefix    = "clawker-socket-"
)

const socketTestHarnessTemplate = `{{define "cmd" -}}
CMD ["sleep", "infinity"]
{{- end}}
`

const socketTestGroupHarnessTemplate = `{{define "root_before_entrypoint" -}}
RUN groupadd --users ${CLAWKER_USER} sockettest
{{- end}}
{{define "cmd" -}}
CMD ["sleep", "infinity"]
{{- end}}
`

type socketManifestOptions struct {
	Optional bool
	Group    string
	Mode     string
}

type socketGrantSummary struct {
	ID      int64  `json:"id"`
	Harness string `json:"harness"`
	Status  string `json:"status"`
	Purpose string `json:"purpose"`
}

type socketGrantDetails struct {
	ID          int64  `json:"id"`
	HarnessName string `json:"harness_name"`
	HarnessPath string `json:"harness_path"`
	HostPath    string `json:"host_path"`
	Status      string `json:"status"`
	ListenerUID int    `json:"listener_uid"`
	ListenerGID int    `json:"listener_gid"`
}

type socketE2EFixture struct {
	t          *testing.T
	h          *harness.Harness
	setup      *harness.SetupResult
	project    string
	harnessDir string
	sourcePath string
	listener   *socketHTTPListener
	manifest   socketManifestOptions
}

type socketHTTPListener struct {
	listener net.Listener
	close    sync.Once
	wg       sync.WaitGroup
}

// TestSocketCreateDefersApproval proves that create does not authorize or
// activate a declared socket and that the first headless start fails closed
// with both supported remedies.
func TestSocketCreateDefersApproval(t *testing.T) {
	fixture := newSocketE2EFixture(
		t,
		"socket-create",
		socketTestHarness,
		"",
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)

	create := fixture.createContainer("create-agent")
	require.NoError(t, create.Err, runFailure("container create", create))
	assert.NotContains(t, create.Stderr, socketTestPrompt)
	assert.NoFileExists(t, fixture.startedPath("create-agent"))
	containerName, err := docker.ContainerName(fixture.project, "create-agent")
	require.NoError(t, err)
	pidPath, err := consts.BridgePIDFilePath(containerName)
	require.NoError(t, err)
	assert.NoFileExists(t, pidPath)
	assert.NoFileExists(t, filepath.Join(fixture.setup.Dirs.State, consts.SocketGrantsDBFile))

	start := fixture.startContainer("create-agent")
	require.Error(t, start.Err)
	combined := start.Stdout + start.Stderr
	assert.Contains(t, combined, socketTestApproveFlag)
	assert.Contains(t, combined, "interactive start")
	assert.NoFileExists(t, fixture.startedPath("create-agent"))
}

// TestSocketApproveGrantAndUseBridge proves that the explicit approval flag
// stores the observed listener identity and makes the forwarded socket usable.
func TestSocketApproveGrantAndUseBridge(t *testing.T) {
	fixture := newSocketE2EFixture(
		t,
		"socket-approve",
		socketTestHarness,
		"",
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("approve-agent").Err)

	start := fixture.startContainer("approve-agent", socketTestApproveFlag)
	require.NoError(t, start.Err, runFailure("approved container start", start))
	assert.Contains(t, start.Stdout, "Approved socket bridge")
	fixture.requireSocketUsable("approve-agent")

	grants := fixture.listGrants()
	require.Len(t, grants, 1)
	details := fixture.grantDetails(grants[0].ID)
	resolvedSource, err := filepath.EvalSymlinks(fixture.sourcePath)
	require.NoError(t, err)
	assert.Equal(t, socketTestHarness, details.HarnessName)
	assert.Equal(t, grants[0].ID, details.ID)
	assert.Equal(t, fixture.harnessDir, details.HarnessPath)
	assert.Equal(t, resolvedSource, details.HostPath)
	assert.Equal(t, db.GrantAllow, details.Status)
	assert.Equal(t, os.Getuid(), details.ListenerUID)
	assert.Equal(t, os.Getgid(), details.ListenerGID)
}

// TestSocketStoredGrantAndRestart proves that a stored approval needs no
// second prompt and that restart creates a usable bridge again.
func TestSocketStoredGrantAndRestart(t *testing.T) {
	fixture := newSocketE2EFixture(
		t,
		"socket-restart",
		socketTestHarness,
		"",
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("restart-agent").Err)
	require.NoError(t, fixture.startContainer("restart-agent", socketTestApproveFlag).Err)
	require.NoError(t, fixture.stopContainer("restart-agent").Err)

	secondStart := fixture.startContainer("restart-agent")
	require.NoError(t, secondStart.Err, runFailure("second container start", secondStart))
	assert.NotContains(t, secondStart.Stderr, socketTestPrompt)
	fixture.requireSocketUsable("restart-agent")

	restart := fixture.h.Run("container", "restart", "--agent", "restart-agent")
	require.NoError(t, restart.Err, runFailure("container restart", restart))
	fixture.requireSocketUsable("restart-agent")
}

// TestSocketListRevokeAndApproveAgain proves the management path and the
// running-container warning, then proves that the next start needs approval.
func TestSocketListRevokeAndApproveAgain(t *testing.T) {
	fixture := newSocketE2EFixture(
		t,
		"socket-revoke",
		socketTestHarness,
		"",
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("revoke-agent").Err)
	require.NoError(t, fixture.startContainer("revoke-agent", socketTestApproveFlag).Err)

	grants := fixture.listGrants()
	require.Len(t, grants, 1)
	assert.Equal(t, socketTestHarness, grants[0].Harness)
	assert.Equal(t, db.GrantAllow, grants[0].Status)
	assert.Equal(t, socketTestPurpose, grants[0].Purpose)

	revoke := fixture.h.Run("sockets", "revoke", strconv.FormatInt(grants[0].ID, 10))
	require.NoError(t, revoke.Err, runFailure("socket revoke", revoke))
	assert.Contains(t, revoke.Stdout, "Running containers keep their socket bridges")
	fixture.requireSocketUsable("revoke-agent")
	require.NoError(t, fixture.stopContainer("revoke-agent").Err)

	start := fixture.startContainer("revoke-agent")
	require.Error(t, start.Err)
	assert.Contains(t, start.Stderr, socketTestApproveFlag)
}

// TestSocketResolvedPathDriftNeedsApproval proves that a source expression
// which resolves to a new real path does not inherit the old approval.
func TestSocketResolvedPathDriftNeedsApproval(t *testing.T) {
	dir := shortSocketTempDir(t)
	firstPath := filepath.Join(dir, "first.sock")
	secondPath := filepath.Join(dir, "second.sock")
	linkPath := filepath.Join(dir, "current.sock")
	first := newSocketHTTPListener(t, firstPath)
	require.NoError(t, os.Symlink(firstPath, linkPath))

	fixture := newSocketE2EFixture(
		t,
		"socket-drift",
		socketTestHarness,
		linkPath,
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("drift-agent").Err)
	require.NoError(t, fixture.startContainer("drift-agent", socketTestApproveFlag).Err)
	require.NoError(t, fixture.stopContainer("drift-agent").Err)
	first.Close()
	require.NoError(t, os.Remove(linkPath))
	newSocketHTTPListener(t, secondPath)
	require.NoError(t, os.Symlink(secondPath, linkPath))

	start := fixture.startContainer("drift-agent")
	require.Error(t, start.Err)
	assert.Contains(t, start.Stderr, "not approved")
	assert.Contains(t, start.Stderr, socketTestApproveFlag)
	assert.Empty(t, fixture.listGrants(), "the stale resolved path must be pruned")
}

// TestSocketLooseShadowNeedsGrantButFloorDoesNotUseDB proves that trust follows
// provenance, not the harness name, and that a floor harness with no sockets
// does not open the socket grant database.
func TestSocketLooseShadowNeedsGrantButFloorDoesNotUseDB(t *testing.T) {
	t.Run("loose shadow prompts", func(t *testing.T) {
		fixture := newSocketE2EFixture(
			t,
			"socket-shadow",
			"claude",
			"",
			socketManifestOptions{},
			socketTestHarnessTemplate,
		)
		require.NoError(t, fixture.createContainer("shadow-agent").Err)
		fixture.h.QueuePromptInput("yes\n")
		start := fixture.startContainer("shadow-agent")
		require.NoError(t, start.Err, runFailure("shadow harness start", start))
		assert.Contains(t, start.Stderr, `Harness "claude"`)
		assert.Contains(t, start.Stderr, socketTestPrompt)
		assert.Empty(t, fixture.listGrants(), "a one-time yes must not persist")
	})

	t.Run("floor harness does not open grant database", func(t *testing.T) {
		fixture := newSocketE2EFixture(
			t,
			"socket-floor",
			"claude",
			"",
			socketManifestOptions{},
			socketTestHarnessTemplate,
		)
		require.NoError(t, fixture.createContainer("floor-agent").Err)
		require.NoError(t, os.RemoveAll(fixture.harnessDir))
		dbPath := filepath.Join(fixture.setup.Dirs.State, consts.SocketGrantsDBFile)
		assert.NoFileExists(t, dbPath)

		start := fixture.startContainer("floor-agent")
		require.NoError(t, start.Err, runFailure("floor harness start", start))
		assert.NotContains(t, start.Stderr, socketTestPrompt)
		assert.NoFileExists(t, dbPath)
	})
}

// TestSocketDeletedListenerDoesNotRunCMDOrLeaveBridge proves that a granted
// but missing listener fails before Docker start and before a bridge daemon.
func TestSocketDeletedListenerDoesNotRunCMDOrLeaveBridge(t *testing.T) {
	fixture := newSocketE2EFixture(
		t,
		"socket-deleted",
		socketTestHarness,
		"",
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("deleted-agent").Err)
	require.NoError(t, fixture.startContainer("deleted-agent", socketTestApproveFlag).Err)
	fixture.requireCMDStarted("deleted-agent")
	containerName, err := docker.ContainerName(fixture.project, "deleted-agent")
	require.NoError(t, err)
	require.NoError(t, fixture.stopContainer("deleted-agent").Err)
	require.NoError(t, os.Remove(fixture.startedPath("deleted-agent")))
	fixture.listener.Close()

	start := fixture.startContainer("deleted-agent")
	require.Error(t, start.Err)
	assert.Contains(t, start.Stderr, "resolve host path")
	assert.Contains(t, start.Stderr, fixture.sourcePath)
	assert.NoFileExists(t, fixture.startedPath("deleted-agent"))
	pidPath, err := consts.BridgePIDFilePath(containerName)
	require.NoError(t, err)
	assert.NoFileExists(t, pidPath)
	assert.False(t, start.Factory.SocketBridge().IsRunning(containerName))
}

// TestSocketPruneDeletedHarness proves that an unresolved loose principal is
// removed by the normal prune command.
func TestSocketPruneDeletedHarness(t *testing.T) {
	fixture := newSocketE2EFixture(
		t,
		"socket-prune",
		socketTestHarness,
		"",
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("prune-agent").Err)
	require.NoError(t, fixture.startContainer("prune-agent", socketTestApproveFlag).Err)
	require.Len(t, fixture.listGrants(), 1)
	require.NoError(t, os.RemoveAll(fixture.harnessDir))

	prune := fixture.h.Run("sockets", "prune", "--yes")
	require.NoError(t, prune.Err, runFailure("socket prune", prune))
	assert.Empty(t, fixture.listGrants())
}

// TestSocketBannedResolvedPathFailsBeforePrompt proves that a harmless-looking
// source expression cannot use a symlink to request the Docker socket.
func TestSocketBannedResolvedPathFailsBeforePrompt(t *testing.T) {
	if _, err := os.Stat(consts.DockerSocketPath); err != nil {
		t.Skipf("Docker socket path is not present on this host: %v", err)
	}
	linkPath := filepath.Join(t.TempDir(), "banned.sock")
	require.NoError(t, os.Symlink(consts.DockerSocketPath, linkPath))
	fixture := newSocketE2EFixture(
		t,
		"socket-banned",
		socketTestHarness,
		linkPath,
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("banned-agent").Err)
	dbPath := filepath.Join(fixture.setup.Dirs.State, consts.SocketGrantsDBFile)
	fixture.h.QueuePromptInput("always\n")

	start := fixture.startContainer("banned-agent")
	require.Error(t, start.Err)
	assert.Contains(t, start.Stderr, "is banned")
	assert.NotContains(t, start.Stderr, socketTestPrompt)
	assert.NoFileExists(t, dbPath)
}

// TestSocketOptionalNoAndAlways proves both optional outcomes: decline starts
// without persistence or a bridge, while always stores and waits for a bridge.
func TestSocketOptionalNoAndAlways(t *testing.T) {
	fixture := newSocketE2EFixture(
		t,
		"socket-optional",
		socketTestHarness,
		"",
		socketManifestOptions{Optional: true},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("optional-agent").Err)
	fixture.h.QueuePromptInput("no\n")

	declined := fixture.startContainer("optional-agent")
	require.NoError(t, declined.Err, runFailure("optional declined start", declined))
	assert.Contains(t, declined.Stderr, "Optional socket bridge")
	assert.Empty(t, fixture.listGrants())
	fixture.requireCMDStarted("optional-agent")
	missing := fixture.h.Run("container", "exec", "--agent", "optional-agent", "test", "-S", socketTestTarget)
	require.Error(t, missing.Err, "the declined optional socket must be absent in the container")
	require.NoError(t, fixture.stopContainer("optional-agent").Err)
	require.NoError(t, os.Remove(fixture.startedPath("optional-agent")))

	fixture.h.QueuePromptInput("always\n")
	approved := fixture.startContainer("optional-agent")
	require.NoError(t, approved.Err, runFailure("optional approved start", approved))
	require.Len(t, fixture.listGrants(), 1)
	fixture.requireSocketUsable("optional-agent")
}

// TestSocketNeverPersistsUntilRevoke proves that a stored denial blocks all
// automatic approval, names its grant, and can only be removed by revoke.
func TestSocketNeverPersistsUntilRevoke(t *testing.T) {
	fixture := newSocketE2EFixture(
		t,
		"socket-never",
		socketTestHarness,
		"",
		socketManifestOptions{},
		socketTestHarnessTemplate,
	)
	require.NoError(t, fixture.createContainer("never-agent").Err)
	fixture.h.QueuePromptInput("never\n")

	denied := fixture.startContainer("never-agent")
	require.Error(t, denied.Err)
	grants := fixture.listGrants()
	require.Len(t, grants, 1)
	assert.Equal(t, db.GrantDeny, grants[0].Status)
	id := strconv.FormatInt(grants[0].ID, 10)

	stored := fixture.startContainer("never-agent")
	require.Error(t, stored.Err)
	assert.Contains(t, stored.Stderr, "grant "+id)
	assert.NotContains(t, stored.Stderr, socketTestPrompt)

	flagged := fixture.startContainer("never-agent", socketTestApproveFlag)
	require.Error(t, flagged.Err)
	assert.Contains(t, flagged.Stderr, "grant "+id)

	revoke := fixture.h.Run("sockets", "revoke", id)
	require.NoError(t, revoke.Err, runFailure("socket denial revoke", revoke))
	fixture.h.QueuePromptInput("yes\n")
	afterRevoke := fixture.startContainer("never-agent")
	require.NoError(t, afterRevoke.Err, runFailure("start after denial revoke", afterRevoke))
	assert.Contains(t, afterRevoke.Stderr, socketTestPrompt)
	fixture.requireSocketUsable("never-agent")
}

// TestSocketContainerGroupAndMode proves declared permissions and proves that
// a missing image group keeps the user CMD behind the socket wait gate.
func TestSocketContainerGroupAndMode(t *testing.T) {
	t.Run("declared group and mode", func(t *testing.T) {
		manifest := socketManifestOptions{Group: socketTestGroup, Mode: socketTestMode}
		fixture := newSocketE2EFixture(
			t,
			"socket-perms",
			socketTestHarness,
			"",
			manifest,
			socketTestGroupHarnessTemplate,
		)
		require.NoError(t, fixture.createContainer("perms-agent").Err)
		require.NoError(t, fixture.startContainer("perms-agent", socketTestApproveFlag).Err)
		fixture.requireCMDStarted("perms-agent")

		stat := fixture.h.Run("container", "exec", "--agent", "perms-agent", "stat", "-c", "%G %a", socketTestTarget)
		require.NoError(t, stat.Err, runFailure("socket permission probe", stat))
		assert.Equal(t, socketTestGroup+" "+strings.TrimPrefix(socketTestMode, "0"), strings.TrimSpace(stat.Stdout))
	})

	t.Run("missing group", func(t *testing.T) {
		manifest := socketManifestOptions{Group: socketTestMissingGroup, Mode: socketTestMode}
		fixture := newSocketE2EFixture(
			t,
			"socket-perms-missing",
			socketTestHarness,
			"",
			manifest,
			socketTestHarnessTemplate,
		)
		require.NoError(t, fixture.createContainer("perms-agent").Err)
		start := fixture.startContainer("perms-agent", socketTestApproveFlag)
		if start.Err != nil {
			t.Logf("start reported the forwarder failure: %v", start.Err)
		}
		assert.Never(t, func() bool {
			_, err := os.Stat(fixture.startedPath("perms-agent"))
			return err == nil
		}, 65*time.Second, 250*time.Millisecond, "CMD must not run when the declared group is absent")
	})
}

func newSocketE2EFixture(
	t *testing.T,
	projectName string,
	harnessName string,
	sourcePath string,
	manifest socketManifestOptions,
	template string,
) *socketE2EFixture {
	t.Helper()
	var listener *socketHTTPListener
	if sourcePath == "" {
		sourcePath = filepath.Join(shortSocketTempDir(t), "service.sock")
		listener = newSocketHTTPListener(t, sourcePath)
	}
	t.Setenv(socketTestSourceEnv, sourcePath)

	h := &harness.Harness{T: t, Opts: socketHarnessOpts()}
	setup := h.NewIsolatedFS(&harness.FSOptions{ProjectDir: projectName})
	harness.EnsureNoControlPlane(t, 30*time.Second)
	initResult := h.Run("project", "init", projectName, "--yes", "--preset", "Bare", "--vcs", "github")
	require.NoError(t, initResult.Err, runFailure("project init", initResult))

	fixture := &socketE2EFixture{
		t:          t,
		h:          h,
		setup:      setup,
		project:    projectName,
		harnessDir: filepath.Join(setup.ProjectDir, consts.DotClawkerDir, bundle.ComponentHarness.Dir(), harnessName),
		sourcePath: sourcePath,
		listener:   listener,
		manifest:   manifest,
	}
	require.NoError(t, os.MkdirAll(fixture.harnessDir, 0o755))
	fixture.writeManifest()
	require.NoError(
		t,
		os.WriteFile(filepath.Join(fixture.harnessDir, bundler.HarnessTemplateFile), []byte(template), 0o600),
	)
	writeSocketProjectConfig(t, setup.ProjectDir, harnessName)

	build := h.Run("build", "--progress=none")
	require.NoError(t, build.Err, runFailure("socket harness build", build))
	return fixture
}

func socketHarnessOpts() *harness.FactoryOptions {
	return &harness.FactoryOptions{
		Config:         config.NewConfig,
		Client:         docker.NewClient,
		ProjectManager: project.NewProjectManager,
		SocketBridge: func(cfg config.Config, log *logger.Logger) socketbridge.SocketBridgeManager {
			return socketbridge.NewManager(cfg, log)
		},
		UseRealControlPlane: true,
		UseRealAdminClient:  true,
	}
}

func writeSocketProjectConfig(t *testing.T, projectDir, harnessName string) {
	t.Helper()
	doc := fmt.Sprintf(`build:
  harness: %s
security:
  enable_host_proxy: false
  git_credentials:
    forward_https: false
    forward_ssh: false
    forward_gpg: false
    copy_git_config: false
`, harnessName)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".clawker.yaml"), []byte(doc), 0o600))
}

func (f *socketE2EFixture) writeManifest() {
	f.t.Helper()
	var optional, container string
	if f.manifest.Optional {
		optional = "    optional: true\n"
	}
	if f.manifest.Group != "" || f.manifest.Mode != "" {
		container = "    container:\n"
		if f.manifest.Group != "" {
			container += "      group: " + f.manifest.Group + "\n"
		}
		if f.manifest.Mode != "" {
			container += "      mode: \"" + f.manifest.Mode + "\"\n"
		}
	}
	doc := fmt.Sprintf(`version: { resolver: none }
sockets:
  - source: %s
    target: %s
    purpose: %s
%s%s`, socketTestSource, socketTestTarget, socketTestPurpose, optional, container)
	require.NoError(f.t, os.WriteFile(filepath.Join(f.harnessDir, bundler.HarnessManifestFile), []byte(doc), 0o600))
}

func (f *socketE2EFixture) createContainer(agent string) *harness.RunResult {
	f.t.Helper()
	marker := filepath.Base(f.startedPath(agent))
	command := "printf socket-cmd-ran > " + marker + "; exec sleep infinity"
	return f.h.Run("container", "create", "--agent", agent, "@", "sh", "-c", command)
}

func (f *socketE2EFixture) startContainer(agent string, extra ...string) *harness.RunResult {
	f.t.Helper()
	args := []string{"container", "start", "--agent"}
	args = append(args, extra...)
	args = append(args, agent)
	return f.h.Run(args...)
}

func (f *socketE2EFixture) stopContainer(agent string) *harness.RunResult {
	f.t.Helper()
	return f.h.Run("container", "stop", "--agent", agent)
}

func (f *socketE2EFixture) startedPath(agent string) string {
	return filepath.Join(f.setup.ProjectDir, agent+socketTestStartedSuffix)
}

func (f *socketE2EFixture) requireSocketUsable(agent string) {
	f.t.Helper()
	f.requireCMDStarted(agent)
	probe := f.h.Run(
		"container",
		"exec",
		"--agent",
		agent,
		"curl",
		"--silent",
		"--show-error",
		"--unix-socket",
		socketTestTarget,
		"http://localhost/",
	)
	require.NoError(f.t, probe.Err, runFailure("socket probe", probe))
	assert.Equal(f.t, socketTestResponse, strings.TrimSpace(probe.Stdout))
}

func (f *socketE2EFixture) requireCMDStarted(agent string) {
	f.t.Helper()
	require.Eventually(f.t, func() bool {
		_, err := os.Stat(f.startedPath(agent))
		return err == nil
	}, 15*time.Second, 100*time.Millisecond, "container user CMD did not start")
}

func (f *socketE2EFixture) listGrants() []socketGrantSummary {
	f.t.Helper()
	result := f.h.Run("sockets", "list", "--json")
	require.NoError(f.t, result.Err, runFailure("socket grant list", result))
	var grants []socketGrantSummary
	require.NoError(f.t, json.Unmarshal([]byte(result.Stdout), &grants), "parse socket grant list: %q", result.Stdout)
	return grants
}

func (f *socketE2EFixture) grantDetails(id int64) socketGrantDetails {
	f.t.Helper()
	result := f.h.Run("sockets", "info", strconv.FormatInt(id, 10), "--json")
	require.NoError(f.t, result.Err, runFailure("socket grant info", result))
	var details socketGrantDetails
	require.NoError(f.t, json.Unmarshal([]byte(result.Stdout), &details), "parse socket grant info: %q", result.Stdout)
	return details
}

func runFailure(operation string, result *harness.RunResult) string {
	return fmt.Sprintf("%s failed: %v\nstdout: %s\nstderr: %s", operation, result.Err, result.Stdout, result.Stderr)
}

func shortSocketTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(socketTestTempRoot, socketTestTempPrefix)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(dir))
	})
	return dir
}

func newSocketHTTPListener(t *testing.T, path string) *socketHTTPListener {
	t.Helper()
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	server := &socketHTTPListener{listener: listener}
	server.wg.Add(1)
	go server.serve()
	t.Cleanup(server.Close)
	return server
}

func (s *socketHTTPListener) serve() {
	defer s.wg.Done()
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				// A close error cannot change the result of this test-only response.
				_ = connection.Close()
			}()
			reader := bufio.NewReader(connection)
			for {
				line, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if line == "\r\n" {
					break
				}
			}
			response := fmt.Sprintf(
				"HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
				len(socketTestResponse),
				socketTestResponse,
			)
			if _, err := io.WriteString(connection, response); err != nil {
				return
			}
		}()
	}
}

func (s *socketHTTPListener) Close() {
	if s == nil {
		return
	}
	s.close.Do(func() {
		// Closing an already failed test listener has no recovery action.
		_ = s.listener.Close()
		s.wg.Wait()
	})
}
