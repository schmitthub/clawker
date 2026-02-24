package container_test

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestMain(m *testing.M) {
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cli, err := client.New(client.FromEnv)
		if err != nil {
			return
		}
		defer cli.Close()

		_ = cleanupTestResources(ctx, cli)
	}

	// Catch SIGINT/SIGTERM so Ctrl+C still cleans up.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cleanup()
		os.Exit(1)
	}()

	// Clean stale resources from previous runs.
	cleanup()

	code := m.Run()

	signal.Stop(sig)
	cleanup()

	os.Exit(code)
}

// requireDocker skips the test if Docker is unavailable.
func requireDocker(t *testing.T) {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("Docker client unavailable: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("Docker daemon not responding: %v", err)
	}
}

// cleanupTestResources removes all Docker resources with the dev.clawker.test=true label.
func cleanupTestResources(ctx context.Context, cli *client.Client) error {
	// Remove containers first (must stop before volumes can be removed).
	containers, err := cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", "dev.clawker.test=true"),
	})
	if err == nil {
		for _, c := range containers.Items {
			_, _ = cli.ContainerStop(ctx, c.ID, client.ContainerStopOptions{})
			_, _ = cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		}
	}

	// Remove volumes.
	volumes, err := cli.VolumeList(ctx, client.VolumeListOptions{
		Filters: client.Filters{}.Add("label", "dev.clawker.test=true"),
	})
	if err == nil {
		for _, v := range volumes.Items {
			_, _ = cli.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{Force: true})
		}
	}

	// Remove networks.
	networks, err := cli.NetworkList(ctx, client.NetworkListOptions{
		Filters: client.Filters{}.Add("label", "dev.clawker.test=true"),
	})
	if err == nil {
		for _, n := range networks.Items {
			_, _ = cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{})
		}
	}

	return nil
}
