package controlplane

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterAndRunInit(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	// --- 1. Build the test image with clawkerd ---
	dc := harness.NewTestClient(t)
	docker.WireBuildKit(dc)

	projectRoot, err := harness.FindProjectRoot()
	require.NoError(t, err, "find project root")

	imageTag := fmt.Sprintf("clawker-cp-test:%d", time.Now().UnixNano())
	dockerfilePath := filepath.Join("test", "controlplane", "testdata", "Dockerfile")

	t.Logf("Building test image %s (BuildKit, context=%s, dockerfile=%s)", imageTag, projectRoot, dockerfilePath)
	err = dc.BuildImage(ctx, nil, docker.BuildImageOpts{
		Tags:            []string{imageTag},
		Dockerfile:      dockerfilePath,
		BuildKitEnabled: true,
		ContextDir:      projectRoot,
		Labels: map[string]string{
			"dev.clawker.test": "true",
		},
	})
	require.NoError(t, err, "build test image with clawkerd")

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		dc.ImageRemove(cleanupCtx, imageTag, whail.ImageRemoveOptions{Force: true})
	})

	// --- 2. Start control plane in-process ---
	secret := fmt.Sprintf("test-secret-%d", time.Now().UnixNano())
	initSpec := &v1.RunInitRequest{
		Steps: []*v1.InitStep{
			{Name: "test-echo", Command: `echo "hello from init"`},
			{Name: "test-env", Command: `echo "USER=$(whoami)"`},
		},
	}

	cp := controlplane.NewServer(controlplane.Config{
		Secret:       secret,
		InitSpec:     initSpec,
		DockerClient: dc,
	})

	lis, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err, "listen for control plane")

	go cp.Serve(lis)
	t.Cleanup(func() { cp.Stop() })

	cpPort := lis.Addr().(*net.TCPAddr).Port
	t.Logf("Control plane listening on port %d", cpPort)

	// --- 3. Create and start container ---
	// The container needs to reach the CP on the host.
	// Use host.docker.internal:host-gateway for host access.
	cpEnv := fmt.Sprintf("CLAWKER_CONTROL_PLANE=host.docker.internal:%d", cpPort)
	secretEnv := fmt.Sprintf("CLAWKER_CONTROL_PLANE_SECRET=%s", secret)

	ctr := harness.RunContainer(t, dc, imageTag,
		harness.WithEnv(cpEnv, secretEnv),
		harness.WithExtraHost("host.docker.internal:host-gateway"),
		harness.WithNetwork("clawker-net"),
		harness.WithPortBinding("50051/tcp"),
	)
	t.Logf("Container started: %s (%s)", ctr.Name, ctr.ID[:12])

	// --- 4. Wait for clawkerd to register with CP ---
	// The container ID inside Docker is the full ID. clawkerd sends os.Hostname()
	// which Docker sets to the first 12 chars of the container ID.
	// We need to figure out which ID clawkerd sends.
	// Actually, Docker sets the hostname to the full container ID (or the first 12 chars
	// depending on version). Let's check both.
	registered := assert.Eventually(t, func() bool {
		return cp.Registry().IsRegistered(ctr.ID) ||
			cp.Registry().IsRegistered(ctr.ID[:12])
	}, 60*time.Second, 500*time.Millisecond, "clawkerd should register with control plane")

	if !registered {
		// Dump container logs for debugging.
		logs, _ := ctr.GetLogs(ctx, harness.NewRawDockerClient(t))
		t.Logf("Container logs:\n%s", logs)
		t.FailNow()
	}

	// Determine the actual container ID used by clawkerd for subsequent checks.
	agentContainerID := ctr.ID
	if !cp.Registry().IsRegistered(agentContainerID) {
		agentContainerID = ctr.ID[:12]
	}
	t.Logf("Agent registered with container ID: %s", agentContainerID)

	// --- 5. Wait for RunInit to complete ---
	require.Eventually(t, func() bool {
		agent := cp.Registry().Get(agentContainerID)
		return agent != nil && agent.InitCompleted
	}, 30*time.Second, 500*time.Millisecond, "init should complete")

	agent := cp.Registry().Get(agentContainerID)
	require.NotNil(t, agent, "agent should exist in registry")
	assert.True(t, agent.InitCompleted, "init should be completed")
	assert.False(t, agent.InitFailed, "init should not have failed")

	// Verify we got the expected events:
	// test-echo STARTED, test-echo COMPLETED, test-env STARTED, test-env COMPLETED, READY
	require.GreaterOrEqual(t, len(agent.InitEvents), 5, "should have at least 5 events")

	// Find the COMPLETED event for test-echo and check its output.
	var echoOutput string
	var envOutput string
	for _, event := range agent.InitEvents {
		if event.StepName == "test-echo" && event.Status == v1.InitEventStatus_INIT_EVENT_STATUS_COMPLETED {
			echoOutput = event.Output
		}
		if event.StepName == "test-env" && event.Status == v1.InitEventStatus_INIT_EVENT_STATUS_COMPLETED {
			envOutput = event.Output
		}
	}
	assert.Contains(t, echoOutput, "hello from init", "echo step should capture output")
	assert.Contains(t, envOutput, "USER=root", "env step should run as root (clawkerd runs as root)")

	// --- 6. Verify main process runs as claude user (su-exec drop) ---
	sleepUID, err := ctr.Exec(ctx, dc, "sh", "-c", `grep "^Uid:" /proc/$(pgrep -x sleep)/status | awk '{print $2}'`)
	require.NoError(t, err, "get sleep UID")
	assert.Contains(t, sleepUID.CleanOutput(), "1001", "main process should run as claude (UID 1001) via su-exec")

	// --- 7. Verify clawkerd runs as root ---
	clawkerdUID, err := ctr.Exec(ctx, dc, "sh", "-c", `cat /proc/$(pgrep clawkerd)/status | grep "^Uid:" | awk '{print $2}'`)
	require.NoError(t, err, "get clawkerd UID")
	assert.Contains(t, clawkerdUID.CleanOutput(), "0", "clawkerd should run as root (UID 0)")

	// --- 8. Verify ready file exists ---
	assert.True(t, ctr.FileExists(ctx, dc, "/var/run/clawker/ready"), "ready file should exist")

	t.Log("POC validation complete: Register + RunInit flow works end-to-end")
}
