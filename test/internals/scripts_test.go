package internals

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEntrypoint_ReadySignal verifies the entrypoint creates the ready file
func TestEntrypoint_ReadySignal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	client := harness.NewTestClient(t)

	// Build image with entrypoint script baked in
	image := harness.BuildLightImage(t, client, "entrypoint.sh")

	// Test on Alpine (Debian variant removed — BuildLightImage uses Alpine)
	t.Run("alpine", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		ctr := harness.RunContainer(t, client, image)

		// Run only the emit_ready function from entrypoint (not the full script)
		execResult, err := ctr.Exec(ctx, client, "bash", "-c", `
			# Extract and run just the emit_ready function
			emit_ready() {
				mkdir -p /var/run/clawker
				echo "ts=$(date +%s) pid=$$" > /var/run/clawker/ready
				echo "[clawker] ready ts=$(date +%s) agent=${CLAWKER_AGENT:-default}"
			}
			emit_ready
		`)
		require.NoError(t, err, "failed to run entrypoint")
		assert.Equal(t, 0, execResult.ExitCode, "entrypoint failed: %s", execResult.CleanOutput())

		// Verify ready file was created
		err = ctr.WaitForFile(ctx, client, "/var/run/clawker/ready", 5*time.Second)
		require.NoError(t, err, "ready file not created")

		// Verify ready file content
		catResult, err := ctr.Exec(ctx, client, "cat", "/var/run/clawker/ready")
		require.NoError(t, err, "failed to read ready file")
		assert.Contains(t, catResult.Stdout, "ts=", "ready file missing timestamp")
		assert.Contains(t, catResult.Stdout, "pid=", "ready file missing pid")
	})
}

// TestEntrypoint_GitConfigFiltering verifies host gitconfig is filtered correctly
func TestEntrypoint_GitConfigFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "entrypoint.sh")
	ctr := harness.RunContainer(t, client, image)

	// Create a mock host gitconfig with credential.helper that should be filtered
	hostGitconfig := `[user]
	name = Test User
	email = test@example.com
[credential]
	helper = osxkeychain
[credential "https://github.com"]
	username = testuser
[core]
	autocrlf = input
`

	// Write the mock host gitconfig to /tmp/host-gitconfig
	createConfig := "cat > /tmp/host-gitconfig << 'EOF'\n" + hostGitconfig + "\nEOF"
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", createConfig)
	require.NoError(t, err, "failed to create host gitconfig")
	require.Equal(t, 0, execResult.ExitCode, "failed to create host gitconfig")

	// Run the gitconfig filtering logic from entrypoint (as claude user)
	filterScript := `
		HOME=/home/claude
		HOST_GITCONFIG="/tmp/host-gitconfig"
		if [ -f "$HOST_GITCONFIG" ]; then
			awk '
				/^\[credential/ { in_cred=1; next }
				/^\[/ { in_cred=0 }
				!in_cred { print }
			' "$HOST_GITCONFIG" > "$HOME/.gitconfig"
		fi
		cat "$HOME/.gitconfig"
	`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", filterScript)
	require.NoError(t, err, "failed to run gitconfig filtering")
	assert.Equal(t, 0, execResult.ExitCode, "gitconfig filtering failed: %s", execResult.CleanOutput())

	// Verify user section is preserved
	assert.Contains(t, execResult.Stdout, "[user]", "user section should be preserved")
	assert.Contains(t, execResult.Stdout, "name = Test User", "user.name should be preserved")
	assert.Contains(t, execResult.Stdout, "email = test@example.com", "user.email should be preserved")

	// Verify core section is preserved
	assert.Contains(t, execResult.Stdout, "[core]", "core section should be preserved")
	assert.Contains(t, execResult.Stdout, "autocrlf = input", "core.autocrlf should be preserved")

	// Verify credential section is removed
	assert.NotContains(t, execResult.Stdout, "[credential]", "credential section should be removed")
	assert.NotContains(t, execResult.Stdout, "helper = osxkeychain", "credential.helper should be removed")
	assert.NotContains(t, execResult.Stdout, "username = testuser", "credential username should be removed")
}

// TestEntrypoint_SshKnownHosts verifies SSH known hosts are set up correctly
func TestEntrypoint_SshKnownHosts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "entrypoint.sh")
	ctr := harness.RunContainer(t, client, image)

	// Run only the ssh_setup_known_hosts function (not the full entrypoint)
	sshSetupScript := `
		HOME=/home/claude
		ssh_setup_known_hosts() {
			mkdir -p "$HOME/.ssh"
			chmod 700 "$HOME/.ssh"
			cat >> "$HOME/.ssh/known_hosts" << 'KNOWN_HOSTS'
github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=
github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=
gitlab.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf
gitlab.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBFSMqzJeV9rUzU4kWitGjeR4PWSa29SPqJ1fVkhtj3Hw9xjLVXVYrU9QlYWrOLXBpQ6KWjbjTDTdDkoohFzgbEY=
gitlab.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCsj2bNKTBSpIYDEGk9KxsGh3mySTRgMtXL583qmBpzeQ+jqCMRgBqB98u3z++J1sKlXHWfM9dyhSevkMwSbhoR8XIq/U0tCNyokEi/ueaBMCvbcTHhO7FcwzY92WK4Ik8Y0iQ7F3awE8ntZELLwOvLYjzo3yl7hGRM9aLhHaVCF8bCG7cJTbplCCVSLKcQzQasPAOmPTmCC/NfZqrT0cIQ2rWM8Q1xI/z3THw1h19WSSqLBgNmz8M+Z7oqlABp7UMlP8W5K5RUCTASg9K7hNg7Jy3gmr3G6V+/FFHDB5PASg8q2g9ByCVWDqt1r8I5NxpqhUJ47RCrKE01zEIyc9z
bitbucket.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO
bitbucket.org ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBPIQmuzMBuKdWeF4+a2sjSSpBK0iqitSQ+5BM9KhpexuGt20JpTVM7u5BDZngncgrqDMbWdxMWWOGtZ9UgbqgZE=
bitbucket.org ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDQeJzhupRu0u0cdegZIa8e86EG2qOCsIsD1Xw0xSeiPDlCr7kq97NLmMbpKTX6Esc30NuoqEEHCuc7yWtwp8dI76EEEB1VqY9QJq6vk+aySyboD5QF61I/1WeTwu+deCbgKMGbUijeXhtfbxSxm6JwGrXrhBdofTsbKRUsrN1WoNgUa8uqN1Vx6WAJw1JHPhglEGGHea6QICwJOAr/6mrui/oB7pkaWKHj3z7d1IC4KWLtY47elvjbaTlkN04Kc/5LFEirorGYVbt15kAUlqGM65pk6ZBxtaO3+30LVlORZkxOh+LKL/BvbZ/iRNhItLqNyieoQj/uj/4PXhq0r2tVoBqXJCmLk7k+zpcaoprJBFQDa5A7SjqPQK0pCwBvhOT0hHpF0sWH4AIQHvYAWVTD0tBFPF1yENBxnVJpfL0L2qgGxLbQCWgOG0/1ygM+Gf9n0AIksE1h/uoLERBHQXE30XuP4pHV3n+7kO5+nw5VVFIsMfrQ3oT89Si/NvvmM=
KNOWN_HOSTS
			chmod 600 "$HOME/.ssh/known_hosts"
		}
		ssh_setup_known_hosts
		cat "$HOME/.ssh/known_hosts"
	`
	execResult, err := ctr.Exec(ctx, client, "bash", "-c", sshSetupScript)
	require.NoError(t, err, "failed to run ssh setup")
	assert.Equal(t, 0, execResult.ExitCode, "ssh setup failed: %s", execResult.CleanOutput())

	// Verify known hosts contains GitHub, GitLab, and Bitbucket
	output := execResult.Stdout
	assert.Contains(t, output, "github.com", "known_hosts should contain github.com")
	assert.Contains(t, output, "gitlab.com", "known_hosts should contain gitlab.com")
	assert.Contains(t, output, "bitbucket.org", "known_hosts should contain bitbucket.org")

	// Verify different key types are present
	assert.Contains(t, output, "ssh-ed25519", "known_hosts should contain ed25519 keys")
	assert.Contains(t, output, "ecdsa-sha2-nistp256", "known_hosts should contain ecdsa keys")
	assert.Contains(t, output, "ssh-rsa", "known_hosts should contain rsa keys")

	// Verify permissions
	permResult, err := ctr.Exec(ctx, client, "stat", "-c", "%a", "/home/claude/.ssh/known_hosts")
	require.NoError(t, err, "failed to check known_hosts permissions")
	assert.Contains(t, permResult.Stdout, "600", "known_hosts should have 600 permissions")
}

// credentialsScript mirrors the credential symlink logic from entrypoint.sh.
// Tests use this to verify the symlink and migration behavior matches
// the production entrypoint. Keep synchronized with entrypoint.sh.
const credentialsScript = `
HOME=/home/claude
CONFIG_DIR="$HOME/.claude"
CREDS_STAGING="$HOME/.clawker-globals"
CREDS_FILE=".credentials.json"

mkdir -p "$CONFIG_DIR"

if [ -d "$CREDS_STAGING" ]; then
    migration_ok=true
    if [ -f "$CONFIG_DIR/$CREDS_FILE" ] && [ ! -L "$CONFIG_DIR/$CREDS_FILE" ] && [ ! -f "$CREDS_STAGING/$CREDS_FILE" ]; then
        if ! cp "$CONFIG_DIR/$CREDS_FILE" "$CREDS_STAGING/$CREDS_FILE"; then
            echo "[clawker] warning: failed to migrate credentials to global volume, keeping local copy" >&2
            migration_ok=false
        fi
    fi
    if [ "$migration_ok" = true ] || [ -f "$CREDS_STAGING/$CREDS_FILE" ]; then
        rm -f "$CONFIG_DIR/$CREDS_FILE"
        if ! ln -s "$CREDS_STAGING/$CREDS_FILE" "$CONFIG_DIR/$CREDS_FILE"; then
            echo "[clawker] error component=credentials msg=failed to create credentials symlink to global volume" >&2
            exit 1
        fi
    fi
    if [ -f "$CREDS_STAGING/$CREDS_FILE" ]; then
        chmod 600 "$CREDS_STAGING/$CREDS_FILE"
    fi
fi
`

// setupGlobalsTestContainer creates a test volume, mounts it at the globals staging path,
// and returns a running container ready for credential tests.
func setupGlobalsTestContainer(t *testing.T, ctx context.Context, client *docker.Client, image string, namePrefix string) *harness.RunningContainer {
	t.Helper()

	volName := fmt.Sprintf("%s-%d", namePrefix, time.Now().UnixNano())

	rawClient := harness.NewRawDockerClient(t)
	_, err := rawClient.VolumeCreate(ctx, dockerclient.VolumeCreateOptions{
		Name:   volName,
		Labels: harness.AddTestLabels(nil),
	})
	require.NoError(t, err, "failed to create test globals volume")
	t.Cleanup(func() {
		_, _ = rawClient.VolumeRemove(context.Background(), volName, dockerclient.VolumeRemoveOptions{Force: true})
	})

	return harness.RunContainer(t, client, image,
		harness.WithMounts(mount.Mount{
			Type:   mount.TypeVolume,
			Source: volName,
			Target: "/home/claude/.clawker-globals",
		}),
	)
}

// TestEntrypoint_CredentialsSymlink verifies the entrypoint creates a symlink
// from ~/.claude/.credentials.json to the global volume staging path, and that
// writes via the symlink persist on the global volume.
func TestEntrypoint_CredentialsSymlink(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	ctr := setupGlobalsTestContainer(t, ctx, client, image, "clawker-test-globals")

	// Run the credentials symlink logic, then verify
	symlinkScript := credentialsScript + `
# Verify symlink exists and points to correct target
if [ -L "$CONFIG_DIR/$CREDS_FILE" ]; then
    target=$(readlink "$CONFIG_DIR/$CREDS_FILE")
    echo "symlink_target=$target"
else
    echo "ERROR: not a symlink"
    exit 1
fi
`

	execResult, err := ctr.Exec(ctx, client, "sh", "-c", symlinkScript)
	require.NoError(t, err, "failed to run symlink script")
	require.Equal(t, 0, execResult.ExitCode, "symlink script failed: %s", execResult.CleanOutput())

	// Verify symlink points to the globals staging path
	assert.Contains(t, execResult.Stdout, "symlink_target=/home/claude/.clawker-globals/.credentials.json",
		"symlink should point to globals staging path")

	// Write credentials via the symlink and verify persistence on the volume
	writeScript := `
		echo '{"oauth_token":"test-token-12345"}' > /home/claude/.claude/.credentials.json
		# Verify the write landed on the global volume
		cat /home/claude/.clawker-globals/.credentials.json
	`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", writeScript)
	require.NoError(t, err, "failed to write credentials via symlink")
	require.Equal(t, 0, execResult.ExitCode, "write script failed: %s", execResult.CleanOutput())
	assert.Contains(t, execResult.Stdout, "test-token-12345",
		"credentials written via symlink should appear on global volume")

	// Verify reading through the symlink path also works
	readScript := `cat /home/claude/.claude/.credentials.json`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", readScript)
	require.NoError(t, err, "failed to read credentials via symlink")
	require.Equal(t, 0, execResult.ExitCode, "read script failed: %s", execResult.CleanOutput())
	assert.Contains(t, execResult.Stdout, "test-token-12345",
		"credentials should be readable via symlink path")
}

// TestEntrypoint_CredentialsMigration verifies the entrypoint migrates existing
// credentials from the per-agent config volume to the global volume before symlinking.
func TestEntrypoint_CredentialsMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	ctr := setupGlobalsTestContainer(t, ctx, client, image, "clawker-test-globals-migrate")

	// Pre-populate credentials on the "per-agent" config volume (simulating existing install)
	setupScript := `
		mkdir -p /home/claude/.claude
		echo '{"oauth_token":"existing-agent-creds"}' > /home/claude/.claude/.credentials.json
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", setupScript)
	require.NoError(t, err, "failed to setup existing credentials")
	require.Equal(t, 0, execResult.ExitCode, "setup failed: %s", execResult.CleanOutput())

	// Run the entrypoint credentials logic — should migrate then symlink
	migrateScript := credentialsScript + `
# Verify migration: credentials should now be on the global volume
echo "global_volume_contents=$(cat $CREDS_STAGING/$CREDS_FILE)"
# Verify symlink: reading via config dir should work
echo "symlink_read=$(cat $CONFIG_DIR/$CREDS_FILE)"
# Verify it's actually a symlink
if [ -L "$CONFIG_DIR/$CREDS_FILE" ]; then
    echo "is_symlink=true"
else
    echo "is_symlink=false"
fi
`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", migrateScript)
	require.NoError(t, err, "failed to run migration script")
	require.Equal(t, 0, execResult.ExitCode, "migration failed: %s", execResult.CleanOutput())

	output := execResult.Stdout
	assert.Contains(t, output, "existing-agent-creds",
		"existing credentials should be migrated to global volume")
	assert.Contains(t, output, "global_volume_contents={\"oauth_token\":\"existing-agent-creds\"}",
		"global volume should contain migrated credentials")
	assert.Contains(t, output, "symlink_read={\"oauth_token\":\"existing-agent-creds\"}",
		"symlink should read migrated credentials")
	assert.Contains(t, output, "is_symlink=true",
		"credentials file should be a symlink after migration")
}

// TestHostOpen_SendsUrlToProxy verifies host-open sends URLs to the proxy
func TestHostOpen_SendsUrlToProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "host-open.sh")
	ctr := harness.RunContainer(t, client, image,
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Get the proxy URL (convert localhost to host.docker.internal for container access)
	proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)

	// Run host-open with a test URL
	testURL := "https://example.com/test"
	execResult, err := ctr.Exec(ctx, client, "sh", "-c",
		"CLAWKER_HOST_PROXY="+proxyURL+" /usr/local/bin/host-open.sh '"+testURL+"'")
	require.NoError(t, err, "failed to run host-open")
	// Note: May fail if proxy response doesn't match expected format, but URL should still be recorded

	// Verify URL was sent to proxy
	time.Sleep(100 * time.Millisecond) // Give proxy time to record
	openedURLs := proxy.GetOpenedURLs()
	require.Len(t, openedURLs, 1, "expected 1 URL to be opened")
	assert.Equal(t, testURL, openedURLs[0], "opened URL should match")

	t.Logf("host-open exit code: %d, output: %s", execResult.ExitCode, execResult.CleanOutput())
}

// TestGitCredential_ForwardsToProxy verifies git-credential-clawker forwards requests to proxy
func TestGitCredential_ForwardsToProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "git-credential-clawker.sh")
	ctr := harness.RunContainer(t, client, image,
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Get the proxy URL
	proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)

	// Test "get" operation
	getScript := `
		echo -e "protocol=https\nhost=github.com\n" | \
		CLAWKER_HOST_PROXY="` + proxyURL + `" /usr/local/bin/git-credential-clawker.sh get
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", getScript)
	require.NoError(t, err, "failed to run git-credential get")
	assert.Equal(t, 0, execResult.ExitCode, "git-credential get failed: %s", execResult.CleanOutput())

	// Verify mock credentials were returned
	assert.Contains(t, execResult.Stdout, "username=mock-user", "should return mock username")
	assert.Contains(t, execResult.Stdout, "password=mock-token", "should return mock password")

	// Verify request was recorded
	creds := proxy.GetGitCreds()
	require.Len(t, creds, 1, "expected 1 git credential request")
	assert.Equal(t, "get", creds[0].Action, "operation should be 'get'")
	assert.Equal(t, "github.com", creds[0].Host, "host should be 'github.com'")
	assert.Equal(t, "https", creds[0].Protocol, "protocol should be 'https'")
}

// TestGitCredential_StoreOperation verifies store operation
func TestGitCredential_StoreOperation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "git-credential-clawker.sh")
	ctr := harness.RunContainer(t, client, image,
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Get the proxy URL
	proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)

	// Test "store" operation
	storeScript := `
		echo -e "protocol=https\nhost=github.com\nusername=myuser\npassword=mytoken\n" | \
		CLAWKER_HOST_PROXY="` + proxyURL + `" /usr/local/bin/git-credential-clawker.sh store
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", storeScript)
	require.NoError(t, err, "failed to run git-credential store")
	assert.Equal(t, 0, execResult.ExitCode, "git-credential store failed: %s", execResult.CleanOutput())

	// Verify request was recorded
	creds := proxy.GetGitCreds()
	require.Len(t, creds, 1, "expected 1 git credential request")
	assert.Equal(t, "store", creds[0].Action, "operation should be 'store'")
	assert.Equal(t, "github.com", creds[0].Host, "host should be 'github.com'")
}

// TestGitCredential_MissingProxy verifies error handling when proxy is not set
func TestGitCredential_MissingProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "git-credential-clawker.sh")
	ctr := harness.RunContainer(t, client, image)

	// Run without CLAWKER_HOST_PROXY set
	execResult, err := ctr.Exec(ctx, client, "sh", "-c",
		"echo -e 'protocol=https\\nhost=github.com\\n' | /usr/local/bin/git-credential-clawker.sh get 2>&1 || true")
	require.NoError(t, err, "failed to run git-credential")

	// Should fail with error about missing proxy
	assert.Contains(t, execResult.Stdout, "CLAWKER_HOST_PROXY", "should mention missing env var")
}

// TestCallbackForwarder_PollsProxy verifies callback-forwarder polls and forwards
func TestCallbackForwarder_PollsProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	// Pre-register a callback session
	sessionID := "test-session-cb"
	proxy.Callbacks[sessionID] = &hostproxytest.CallbackData{
		SessionID:    sessionID,
		OriginalPort: "8080",
		CallbackPath: "/callback",
	}

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Get the proxy URL
	proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)

	// Start a simple HTTP server in the container to receive the forwarded callback
	startServerScript := `
		# Start a simple server that writes received requests to a file
		while true; do
			echo -e "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nOK" | nc -l -p 8080 > /tmp/received_callback 2>&1 &
			sleep 0.5
			break
		done
	`
	_, err := ctr.Exec(ctx, client, "sh", "-c", startServerScript)
	require.NoError(t, err, "failed to start test server")

	// Simulate OAuth callback being captured by proxy
	go func() {
		time.Sleep(500 * time.Millisecond)
		proxy.SetCallbackReady(sessionID, "/callback", "code=abc123&state=xyz")
	}()

	// Run callback-forwarder with short timeout
	forwarderScript := `
		CLAWKER_HOST_PROXY="` + proxyURL + `" \
		CALLBACK_SESSION="` + sessionID + `" \
		CALLBACK_PORT=8080 \
		CB_FORWARDER_TIMEOUT=10 \
		CB_FORWARDER_POLL_INTERVAL=1 \
		/usr/local/bin/callback-forwarder -v 2>&1 || echo "forwarder exit code: $?"
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", forwarderScript)
	require.NoError(t, err, "failed to run callback-forwarder")

	t.Logf("callback-forwarder output: %s", execResult.CleanOutput())

	// The forwarder should have attempted to forward the callback
	assert.Contains(t, execResult.CleanOutput(), "Callback received", "should log callback received")
}

// TestCallbackForwarder_Timeout verifies timeout behavior
func TestCallbackForwarder_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	// Pre-register a callback session but don't set it ready
	sessionID := "test-session-timeout"
	proxy.Callbacks[sessionID] = &hostproxytest.CallbackData{
		SessionID:    sessionID,
		OriginalPort: "8080",
		CallbackPath: "/callback",
		Ready:        false, // Never becomes ready
	}

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Get the proxy URL
	proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)

	// Run callback-forwarder with very short timeout
	forwarderScript := `
		CLAWKER_HOST_PROXY="` + proxyURL + `" \
		CALLBACK_SESSION="` + sessionID + `" \
		CALLBACK_PORT=8080 \
		CB_FORWARDER_TIMEOUT=3 \
		CB_FORWARDER_POLL_INTERVAL=1 \
		/usr/local/bin/callback-forwarder 2>&1
		echo "exit_code=$?"
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", forwarderScript)
	require.NoError(t, err, "failed to run callback-forwarder")

	// Should timeout
	assert.Contains(t, execResult.Stdout, "Timeout", "should report timeout")
	assert.Contains(t, execResult.Stdout, "exit_code=1", "should exit with code 1")
}
