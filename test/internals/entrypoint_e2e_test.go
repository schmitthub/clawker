package internals

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/cmd/container/shared"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	whail "github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEntrypoint_FullInitSequence runs the entire entrypoint.sh as it runs in production —
// as the claude user with all env vars set — then verifies every internal component works,
// including host proxy scripts and the socket bridge binary via a lightweight muxrpc protocol mock.
//
// This catches interaction bugs between init steps that isolated script tests miss.
// The firewall stopped working in production despite all isolated tests passing — this test
// exists to prevent that class of regression.
func TestEntrypoint_FullInitSequence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dc := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, dc)

	// Start mock host proxy — real HTTP server on localhost, reachable via host.docker.internal
	mockProxy := hostproxytest.NewMockHostProxy(t)
	proxyURL := mockProxy.URL()
	// Extract port from URL for container-side env var
	proxyPort := proxyURL[strings.LastIndex(proxyURL, ":")+1:]
	containerProxyURL := fmt.Sprintf("http://host.docker.internal:%s", proxyPort)

	agent := harness.UniqueAgentName(t)
	project := "test-e2e"

	// Create config volume
	volumeName, err := docker.VolumeName(project, agent, "config")
	require.NoError(t, err)
	_, err = dc.EnsureVolume(ctx, volumeName, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := dc.VolumeRemove(cleanupCtx, volumeName, true); err != nil {
			t.Logf("WARNING: failed to remove config volume %s: %v", volumeName, err)
		}
	})

	// Create container (NOT started yet) — manual create-inject-start pattern
	containerName := harness.UniqueContainerName(t)
	labels := harness.AddTestLabels(map[string]string{
		harness.ClawkerManagedLabel: "true",
	})

	createResp, err := dc.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Config: &container.Config{
			Image:      image,
			Entrypoint: []string{"/usr/local/bin/entrypoint.sh"},
			Cmd:        []string{"sleep", "infinity"},
			Labels:     labels,
			Env: []string{
				"CLAWKER_PROJECT=" + project,
				"CLAWKER_AGENT=" + agent,
				// Use github+google IP ranges to produce >10KB of firewall output.
				// This catches the SIGPIPE bug: head -c 10000 in the entrypoint closes
				// the pipe, killing the firewall script before DROP policies are set.
				`CLAWKER_FIREWALL_IP_RANGE_SOURCES=[{"name":"github"},{"name":"google"}]`,
				`CLAWKER_FIREWALL_DOMAINS=["charm.land","code.gitea.io","golang.org","proxy.golang.org","sum.golang.org","gocloud.dev","gopkg.in","software.sslmate.com","cloud.google.com","sigs.k8s.io","google.golang.org","storage.googleapis.com","go.opentelemetry.io","go.uber.org","lukechampine.com","go.yaml.in","cel.dev","files.pythonhosted.org","pkg.go.dev","go.dev","raw.githubusercontent.com","objects.githubusercontent.com","crates.io","npmjs.com","hub.docker.com","ghcr.io"]`,
				"CLAWKER_GIT_HTTPS=true",
				"CLAWKER_HOST_PROXY=" + containerProxyURL,
				"CLAWKER_WORKSPACE_MODE=snapshot",
				"CLAWKER_VERSION=test",
			},
		},
		HostConfig: &container.HostConfig{
			CapAdd:     []string{"NET_ADMIN", "NET_RAW"},
			ExtraHosts: []string{"host.docker.internal:host-gateway"},
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeVolume,
					Source: volumeName,
					Target: containerHomeDir + "/.claude",
				},
			},
		},
		Name: containerName,
	})
	require.NoError(t, err, "ContainerCreate failed")

	containerID := createResp.ID
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := dc.ContainerStop(cleanupCtx, containerID, nil); err != nil {
			t.Logf("WARNING: failed to stop container %s: %v", containerName, err)
		}
		if _, err := dc.ContainerRemove(cleanupCtx, containerID, true); err != nil {
			t.Logf("WARNING: failed to remove container %s: %v", containerName, err)
		}
	})

	// Pre-start injections
	copyFn := shared.NewCopyToContainerFn(dc)

	// Inject ~/.claude-init/ with statusline.sh + settings.json
	injectClaudeInit(t, ctx, dc, containerID)

	// Inject /tmp/host-gitconfig with test fixture
	injectHostGitconfig(t, ctx, dc, containerID)

	// Inject post-init script
	err = shared.InjectPostInitScript(ctx, shared.InjectPostInitOpts{
		ContainerID:     containerID,
		Script:          `echo "post-init-executed" > /tmp/e2e-post-init-marker`,
		CopyToContainer: copyFn,
	})
	require.NoError(t, err, "InjectPostInitScript failed")

	// Start container
	_, err = dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: containerID})
	require.NoError(t, err, "ContainerStart failed")

	// Wait for ready signal (5 min timeout via ctx)
	t.Log("Waiting for container to become ready...")
	err = harness.WaitForReadyFile(ctx, dc, containerID)
	require.NoError(t, err, "container never became ready")
	t.Log("Container ready")

	// Wrap in RunningContainer for exec helpers
	ctr := &harness.RunningContainer{ID: containerID, Name: containerName}

	// ---------------------------------------------------------------------------
	// Firewall subtests
	// ---------------------------------------------------------------------------
	t.Run("firewall", func(t *testing.T) {
		t.Run("iptables_output_drop_policy", func(t *testing.T) {
			result, err := ctr.ExecAsUser(ctx, dc, "root", "iptables", "-L", "OUTPUT", "-n")
			require.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode, "iptables failed: %s", result.Stderr)
			assert.Contains(t, result.CleanOutput(), "policy DROP",
				"OUTPUT chain should have DROP policy")
		})

		t.Run("ipset_exists", func(t *testing.T) {
			result, err := ctr.ExecAsUser(ctx, dc, "root", "ipset", "list", "allowed-domains")
			require.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode,
				"ipset allowed-domains should exist: %s", result.Stderr)
		})

		t.Run("blocks_unauthorized", func(t *testing.T) {
			result, err := ctr.Exec(ctx, dc, "curl", "--connect-timeout", "3", "https://example.com")
			require.NoError(t, err)
			assert.NotEqual(t, 0, result.ExitCode,
				"curl to example.com should be blocked by firewall")
		})

		t.Run("allows_github", func(t *testing.T) {
			result, err := ctr.Exec(ctx, dc,
				"curl", "--connect-timeout", "10", "-s", "-o", "/dev/null", "-w", "%{http_code}", "https://api.github.com/zen")
			require.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode,
				"curl to api.github.com should be allowed: stderr=%s", result.Stderr)
		})
	})

	// ---------------------------------------------------------------------------
	// Config volume subtests
	// ---------------------------------------------------------------------------
	t.Run("config", func(t *testing.T) {
		t.Run("settings_json_merged", func(t *testing.T) {
			assert.True(t, ctr.FileExists(ctx, dc, containerHomeDir+"/.claude/settings.json"),
				"settings.json should exist")
			content, err := ctr.ReadFile(ctx, dc, containerHomeDir+"/.claude/settings.json")
			require.NoError(t, err)
			assert.Contains(t, content, "statusLine",
				"settings.json should contain statusLine from claude-settings.json")
		})

		t.Run("statusline_copied", func(t *testing.T) {
			assert.True(t, ctr.FileExists(ctx, dc, containerHomeDir+"/.claude/statusline.sh"),
				"statusline.sh should be copied from init dir")
		})
	})

	// ---------------------------------------------------------------------------
	// Git configuration subtests
	// ---------------------------------------------------------------------------
	t.Run("git", func(t *testing.T) {
		t.Run("gitconfig_filtered", func(t *testing.T) {
			content, err := ctr.ReadFile(ctx, dc, containerHomeDir+"/.gitconfig")
			require.NoError(t, err)
			assert.Contains(t, content, "[user]",
				"gitconfig should contain [user] section")
			assert.Contains(t, content, "[core]",
				"gitconfig should contain [core] section")
			// The entrypoint filters out the original [credential] section (osxkeychain, store)
			// then adds credential.helper = clawker via git config --global.
			assert.NotContains(t, content, "osxkeychain",
				"original credential helper should be filtered out")
			assert.Contains(t, content, "helper = clawker",
				"clawker credential helper should be configured")
		})

		t.Run("credential_helper_configured", func(t *testing.T) {
			result, err := ctr.Exec(ctx, dc, "git", "config", "--global", "credential.helper")
			require.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode)
			assert.Contains(t, result.CleanOutput(), "clawker",
				"credential.helper should be 'clawker'")
		})
	})

	// ---------------------------------------------------------------------------
	// SSH known hosts subtests
	// ---------------------------------------------------------------------------
	t.Run("ssh", func(t *testing.T) {
		t.Run("known_hosts_setup", func(t *testing.T) {
			content, err := ctr.ReadFile(ctx, dc, containerHomeDir+"/.ssh/known_hosts")
			require.NoError(t, err)
			assert.Contains(t, content, "github.com")
			assert.Contains(t, content, "gitlab.com")
			assert.Contains(t, content, "bitbucket.org")

			// Verify permissions
			result, err := ctr.Exec(ctx, dc, "stat", "-c", "%a", containerHomeDir+"/.ssh/known_hosts")
			require.NoError(t, err)
			assert.Equal(t, "600", strings.TrimSpace(result.CleanOutput()),
				"known_hosts should have 600 permissions")
		})
	})

	// ---------------------------------------------------------------------------
	// Post-init subtests
	// ---------------------------------------------------------------------------
	t.Run("postinit", func(t *testing.T) {
		t.Run("script_executed", func(t *testing.T) {
			assert.True(t, ctr.FileExists(ctx, dc, "/tmp/e2e-post-init-marker"),
				"post-init marker should exist")
			content, err := ctr.ReadFile(ctx, dc, "/tmp/e2e-post-init-marker")
			require.NoError(t, err)
			assert.Equal(t, "post-init-executed", content)
		})

		t.Run("marker_created", func(t *testing.T) {
			assert.True(t, ctr.FileExists(ctx, dc, containerHomeDir+"/.claude/post-initialized"),
				"post-initialized marker should exist")
		})
	})

	// ---------------------------------------------------------------------------
	// Ready signal subtests
	// ---------------------------------------------------------------------------
	t.Run("ready", func(t *testing.T) {
		t.Run("signal_file", func(t *testing.T) {
			content, err := ctr.ReadFile(ctx, dc, harness.ReadyFilePath)
			require.NoError(t, err)
			assert.Contains(t, content, "ts=", "ready file should contain timestamp")
			assert.Contains(t, content, "pid=", "ready file should contain pid")
		})

		t.Run("log_output", func(t *testing.T) {
			logs, err := harness.GetContainerLogs(ctx, dc, containerID)
			require.NoError(t, err)
			assert.Contains(t, logs, "[clawker] ready",
				"container logs should contain ready signal")
		})
	})

	// ---------------------------------------------------------------------------
	// Ordering subtests
	// ---------------------------------------------------------------------------
	t.Run("ordering", func(t *testing.T) {
		t.Run("postinit_before_ready", func(t *testing.T) {
			logs, err := harness.GetContainerLogs(ctx, dc, containerID)
			require.NoError(t, err)
			postInitIdx := strings.Index(logs, "[clawker] running post-init")
			readyIdx := strings.Index(logs, "[clawker] ready")
			require.NotEqual(t, -1, postInitIdx, "should find post-init log line")
			require.NotEqual(t, -1, readyIdx, "should find ready log line")
			assert.Less(t, postInitIdx, readyIdx,
				"post-init should run before ready signal")
		})
	})

	// ---------------------------------------------------------------------------
	// Host proxy script subtests
	// ---------------------------------------------------------------------------
	t.Run("hostproxy", func(t *testing.T) {
		t.Run("git_credential_get", func(t *testing.T) {
			result, err := ctr.Exec(ctx, dc, "bash", "-c",
				fmt.Sprintf(`printf 'protocol=https\nhost=github.com\n\n' | CLAWKER_HOST_PROXY=%s git-credential-clawker.sh get`, containerProxyURL))
			require.NoError(t, err)
			output := result.CleanOutput()
			assert.Equal(t, 0, result.ExitCode, "git-credential-clawker.sh get failed: %s", result.Stderr)
			assert.Contains(t, output, "username=mock-user")
			assert.Contains(t, output, "password=mock-token")

			// Verify mock received the request
			creds := mockProxy.GetGitCreds()
			require.NotEmpty(t, creds, "mock should have received git credential request")
			lastCred := creds[len(creds)-1]
			assert.Equal(t, "get", lastCred.Action)
			assert.Equal(t, "github.com", lastCred.Host)
		})

		t.Run("git_credential_store", func(t *testing.T) {
			result, err := ctr.Exec(ctx, dc, "bash", "-c",
				fmt.Sprintf(`printf 'protocol=https\nhost=github.com\nusername=test\npassword=test\n\n' | CLAWKER_HOST_PROXY=%s git-credential-clawker.sh store`, containerProxyURL))
			require.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode,
				"git-credential-clawker.sh store failed: %s", result.Stderr)

			creds := mockProxy.GetGitCreds()
			var foundStore bool
			for _, c := range creds {
				if c.Action == "store" {
					foundStore = true
					break
				}
			}
			assert.True(t, foundStore, "mock should have received store request")
		})

		t.Run("host_open_sends_url", func(t *testing.T) {
			result, err := ctr.Exec(ctx, dc, "bash", "-c",
				fmt.Sprintf(`CLAWKER_HOST_PROXY=%s host-open.sh "https://example.com/test"`, containerProxyURL))
			require.NoError(t, err)
			// host-open.sh sends the URL to /open/url. The mock receives it (verified below)
			// but the script may exit non-zero if the mock response doesn't contain "success":true.
			// The important thing is the URL was received by the proxy.
			_ = result

			urls := mockProxy.GetOpenedURLs()
			assert.Contains(t, urls, "https://example.com/test",
				"mock should have received the URL")
		})

		t.Run("callback_forwarder_polls", func(t *testing.T) {
			// Register a callback session on the mock
			sessionID := "test-e2e-session"
			mockProxy.Callbacks[sessionID] = &hostproxytest.CallbackData{
				SessionID:    sessionID,
				OriginalPort: "8080",
				CallbackPath: "/callback",
			}
			// Mark callback as ready immediately
			mockProxy.SetCallbackReady(sessionID, "/callback", "code=abc123")

			result, err := ctr.Exec(ctx, dc, "bash", "-c",
				fmt.Sprintf(`CLAWKER_HOST_PROXY=%s CALLBACK_SESSION=%s CALLBACK_PORT=8080 CB_FORWARDER_TIMEOUT=10 CB_FORWARDER_POLL_INTERVAL=1 callback-forwarder -v 2>&1`,
					containerProxyURL, sessionID))
			require.NoError(t, err)
			output := result.CleanOutput()
			t.Logf("callback-forwarder output: %s", output)
			// The forwarder will receive the callback from the mock but can't forward it
			// to localhost:8080 (no real server). What matters is it successfully polled and
			// received the callback data.
			assert.Contains(t, output, "Callback received",
				"callback-forwarder should report callback received")
		})
	})

	// ---------------------------------------------------------------------------
	// Socket bridge subtests — lightweight muxrpc protocol mock
	// ---------------------------------------------------------------------------
	t.Run("socketbridge", func(t *testing.T) {
		t.Run("protocol_handshake", func(t *testing.T) {
			testSocketBridgeProtocol(t, ctx, dc, containerID)
		})
	})
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// injectClaudeInit creates a tar with ~/.claude-init/{statusline.sh, settings.json}
// from bundler assets and copies it into the container.
func injectClaudeInit(t *testing.T, ctx context.Context, dc *docker.Client, containerID string) {
	t.Helper()

	projectRoot, err := harness.FindProjectRoot()
	require.NoError(t, err)

	assetsDir := filepath.Join(projectRoot, "internal", "bundler", "assets")

	statusline, err := os.ReadFile(filepath.Join(assetsDir, "statusline.sh"))
	require.NoError(t, err, "failed to read statusline.sh")

	settings, err := os.ReadFile(filepath.Join(assetsDir, "claude-settings.json"))
	require.NoError(t, err, "failed to read claude-settings.json")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()

	// Directory entry
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir,
		Name:     ".claude-init/",
		Mode:     0755,
		ModTime:  now,
	}))

	// statusline.sh
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:    ".claude-init/statusline.sh",
		Mode:    0755,
		Size:    int64(len(statusline)),
		ModTime: now,
	}))
	_, err = tw.Write(statusline)
	require.NoError(t, err)

	// settings.json
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:    ".claude-init/settings.json",
		Mode:    0644,
		Size:    int64(len(settings)),
		ModTime: now,
	}))
	_, err = tw.Write(settings)
	require.NoError(t, err)

	require.NoError(t, tw.Close())

	copyFn := shared.NewCopyToContainerFn(dc)
	require.NoError(t, copyFn(ctx, containerID, containerHomeDir, &buf),
		"failed to inject claude-init")
}

// injectHostGitconfig creates a tar with /tmp/host-gitconfig fixture and copies it
// into the container. The fixture has [user], [credential], and [core] sections
// so the entrypoint's awk filter can be verified.
func injectHostGitconfig(t *testing.T, ctx context.Context, dc *docker.Client, containerID string) {
	t.Helper()

	gitconfig := []byte(`[user]
	name = Test User
	email = test@example.com
[credential]
	helper = osxkeychain
[credential "https://github.com"]
	helper = store
[core]
	autocrlf = input
	editor = vim
`)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "host-gitconfig",
		Mode: 0644,
		Size: int64(len(gitconfig)),
	}))
	_, err := tw.Write(gitconfig)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	copyFn := shared.NewCopyToContainerFn(dc)
	require.NoError(t, copyFn(ctx, containerID, "/tmp", &buf),
		"failed to inject host-gitconfig")
}

// testSocketBridgeProtocol launches clawker-socket-server inside the container via
// docker exec and verifies the muxrpc READY handshake. Uses SSH-only config to avoid
// GPG pubkey requirement.
func testSocketBridgeProtocol(t *testing.T, ctx context.Context, dc *docker.Client, containerID string) {
	t.Helper()

	// Use /tmp/test-ssh.sock to avoid interfering with SSH known_hosts test
	socketConfig := `[{"path":"/tmp/test-ssh.sock","type":"ssh-agent"}]`

	// Create exec with stdin attached — we need bidirectional communication
	execResp, err := dc.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"clawker-socket-server"},
		Env:          []string{"CLAWKER_REMOTE_SOCKETS=" + socketConfig},
	})
	require.NoError(t, err, "ExecCreate for socket-server failed")

	hijacked, err := dc.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{
		TTY: false,
	})
	require.NoError(t, err, "ExecAttach for socket-server failed")
	defer hijacked.Close()

	// Read muxrpc message — expect READY
	// Docker multiplexes stdout/stderr when TTY=false: 8-byte header + payload
	// Header: [stream_type(1)][0][0][0][size(4)]
	// Stream type: 1=stdout, 2=stderr
	//
	// The muxrpc message is inside the stdout payload:
	// [4-byte length][1-byte type][4-byte streamID][payload]

	// Read stdout frames until we get the READY message or timeout
	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()

	readyCh := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		// Read Docker multiplex frames from the hijacked connection
		for {
			// Read 8-byte Docker multiplex header
			dockerHeader := make([]byte, 8)
			if _, err := io.ReadFull(hijacked.Reader, dockerHeader); err != nil {
				errCh <- fmt.Errorf("failed to read docker header: %w", err)
				return
			}

			streamType := dockerHeader[0]
			frameSize := binary.BigEndian.Uint32(dockerHeader[4:8])

			// Read the frame payload
			payload := make([]byte, frameSize)
			if _, err := io.ReadFull(hijacked.Reader, payload); err != nil {
				errCh <- fmt.Errorf("failed to read frame payload: %w", err)
				return
			}

			if streamType == 2 {
				// stderr — log it for debugging
				t.Logf("socket-server stderr: %s", strings.TrimSpace(string(payload)))
				continue
			}

			// stdout — parse muxrpc message
			if streamType == 1 && len(payload) >= 9 {
				// Muxrpc: [4-byte total_len][1-byte type][4-byte streamID][payload_bytes]
				msgType, _, _, err := readMuxrpcMessage(bytes.NewReader(payload))
				if err != nil {
					t.Logf("failed to parse muxrpc message: %v", err)
					continue
				}

				const MsgReady byte = 5
				if msgType == MsgReady {
					close(readyCh)
					return
				}
			}
		}
	}()

	select {
	case <-readyCh:
		t.Log("Received muxrpc READY message from socket-server")
	case err := <-errCh:
		t.Fatalf("socket-server communication error: %v", err)
	case <-readCtx.Done():
		t.Fatal("timeout waiting for muxrpc READY from socket-server")
	}

	// Verify socket was created — check BEFORE closing stdin, since the
	// server cleans up sockets on exit. Use test -S (socket) not test -f (regular file).
	ctr := &harness.RunningContainer{ID: containerID, Name: ""}
	result, err := ctr.Exec(ctx, dc, "test", "-S", "/tmp/test-ssh.sock")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode,
		"socket-server should have created /tmp/test-ssh.sock (unix socket)")

	// Close stdin to trigger server exit
	hijacked.CloseWrite()
}

// readMuxrpcMessage reads one muxrpc message from a reader.
// Wire format: [4-byte total_len][1-byte type][4-byte streamID][payload_bytes]
// total_len = 1 + 4 + len(payload) (type byte + streamID + payload)
func readMuxrpcMessage(r io.Reader) (msgType byte, streamID uint32, payload []byte, err error) {
	// Read 4-byte length
	lenBuf := make([]byte, 4)
	if _, err = io.ReadFull(r, lenBuf); err != nil {
		return 0, 0, nil, fmt.Errorf("read length: %w", err)
	}
	length := binary.BigEndian.Uint32(lenBuf)

	if length < 5 {
		return 0, 0, nil, fmt.Errorf("message too short: %d", length)
	}

	// Read 1-byte type
	typeBuf := make([]byte, 1)
	if _, err = io.ReadFull(r, typeBuf); err != nil {
		return 0, 0, nil, fmt.Errorf("read type: %w", err)
	}
	msgType = typeBuf[0]

	// Read 4-byte streamID
	streamBuf := make([]byte, 4)
	if _, err = io.ReadFull(r, streamBuf); err != nil {
		return 0, 0, nil, fmt.Errorf("read streamID: %w", err)
	}
	streamID = binary.BigEndian.Uint32(streamBuf)

	// Read payload
	payloadLen := length - 5
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err = io.ReadFull(r, payload); err != nil {
			return 0, 0, nil, fmt.Errorf("read payload: %w", err)
		}
	}

	return msgType, streamID, payload, nil
}
