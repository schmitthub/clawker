package whail_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

// TestJail_RejectsUnmanaged verifies that every REJECT-category method refuses
// to operate on resources that lack the managed label.
//
// Each subtest:
//  1. Configures the FakeAPIClient inspect to return an unmanaged resource.
//  2. Calls the Engine method.
//  3. Asserts a non-nil *DockerError is returned.
//  4. Asserts the downstream moby method was never forwarded to.
func TestJail_RejectsUnmanaged(t *testing.T) {
	// Common setup functions — one per resource type.
	unmanagedContainer := func(fake *whailtest.FakeAPIClient) {
		fake.ContainerInspectFn = func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return whailtest.UnmanagedContainerInspect(id), nil
		}
	}
	unmanagedVolume := func(fake *whailtest.FakeAPIClient) {
		fake.VolumeInspectFn = func(_ context.Context, id string, _ client.VolumeInspectOptions) (client.VolumeInspectResult, error) {
			return whailtest.UnmanagedVolumeInspect(id), nil
		}
	}
	unmanagedNetwork := func(fake *whailtest.FakeAPIClient) {
		fake.NetworkInspectFn = func(_ context.Context, name string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
			return whailtest.UnmanagedNetworkInspect(name), nil
		}
	}
	unmanagedImage := func(fake *whailtest.FakeAPIClient) {
		fake.ImageInspectFn = func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return whailtest.UnmanagedImageInspect(ref), nil
		}
	}

	tests := []struct {
		name      string
		setup     func(fake *whailtest.FakeAPIClient)
		call      func(e *whail.Engine) error
		dangerous string // moby method that must NOT be forwarded
		// inspectSelf is true when the Engine method and its managed check both
		// use the same underlying moby method (e.g., Engine.ContainerInspect calls
		// APIClient.ContainerInspect for the managed check). In that case we assert
		// the method was called exactly once (the check), not zero times.
		inspectSelf bool
	}{
		// ── Container methods (21) ──────────────────────────────────────

		{
			name:      "ContainerStop",
			setup:     unmanagedContainer,
			call:      func(e *whail.Engine) error { _, err := e.ContainerStop(context.Background(), "c1", nil); return err },
			dangerous: "ContainerStop",
		},
		{
			name:  "ContainerRemove",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerRemove(context.Background(), "c1", false)
				return err
			},
			dangerous: "ContainerRemove",
		},
		{
			name:  "ContainerKill",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerKill(context.Background(), "c1", "SIGTERM")
				return err
			},
			dangerous: "ContainerKill",
		},
		{
			name:      "ContainerPause",
			setup:     unmanagedContainer,
			call:      func(e *whail.Engine) error { _, err := e.ContainerPause(context.Background(), "c1"); return err },
			dangerous: "ContainerPause",
		},
		{
			name:      "ContainerUnpause",
			setup:     unmanagedContainer,
			call:      func(e *whail.Engine) error { _, err := e.ContainerUnpause(context.Background(), "c1"); return err },
			dangerous: "ContainerUnpause",
		},
		{
			name:      "ContainerRestart",
			setup:     unmanagedContainer,
			call:      func(e *whail.Engine) error { _, err := e.ContainerRestart(context.Background(), "c1", nil); return err },
			dangerous: "ContainerRestart",
		},
		{
			name:  "ContainerRename",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerRename(context.Background(), "c1", "newname")
				return err
			},
			dangerous: "ContainerRename",
		},
		{
			name:  "ContainerResize",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerResize(context.Background(), "c1", 24, 80)
				return err
			},
			dangerous: "ContainerResize",
		},
		{
			name:  "ContainerAttach",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerAttach(context.Background(), "c1", client.ContainerAttachOptions{})
				return err
			},
			dangerous: "ContainerAttach",
		},
		{
			name:  "ContainerWait",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				result := e.ContainerWait(context.Background(), "c1", container.WaitConditionNotRunning)
				// Error channel is buffered — reads immediately when unmanaged.
				return <-result.Error
			},
			dangerous: "ContainerWait",
		},
		{
			name:  "ContainerLogs",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerLogs(context.Background(), "c1", client.ContainerLogsOptions{})
				return err
			},
			dangerous: "ContainerLogs",
		},
		{
			name:  "ContainerTop",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerTop(context.Background(), "c1", nil)
				return err
			},
			dangerous: "ContainerTop",
		},
		{
			name:  "ContainerStats",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerStats(context.Background(), "c1", false)
				return err
			},
			dangerous: "ContainerStats",
		},
		{
			name:  "ContainerStatsOneShot",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerStatsOneShot(context.Background(), "c1")
				return err
			},
			dangerous: "ContainerStats", // StatsOneShot delegates to APIClient.ContainerStats
		},
		{
			name:  "ContainerUpdate",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerUpdate(context.Background(), "c1", nil, nil)
				return err
			},
			dangerous: "ContainerUpdate",
		},
		{
			name:  "ContainerInspect",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerInspect(context.Background(), "c1", client.ContainerInspectOptions{})
				return err
			},
			dangerous:   "ContainerInspect",
			inspectSelf: true, // managed check calls ContainerInspect once
		},
		{
			name:  "ContainerStart",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerStart(context.Background(), whail.ContainerStartOptions{ContainerID: "c1"})
				return err
			},
			dangerous: "ContainerStart",
		},
		{
			name:  "ExecCreate",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ExecCreate(context.Background(), "c1", client.ExecCreateOptions{})
				return err
			},
			dangerous: "ExecCreate",
		},
		{
			name:  "CopyToContainer",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.CopyToContainer(context.Background(), "c1", client.CopyToContainerOptions{})
				return err
			},
			dangerous: "CopyToContainer",
		},
		{
			name:  "CopyFromContainer",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.CopyFromContainer(context.Background(), "c1", client.CopyFromContainerOptions{})
				return err
			},
			dangerous: "CopyFromContainer",
		},
		{
			name:  "ContainerStatPath",
			setup: unmanagedContainer,
			call: func(e *whail.Engine) error {
				_, err := e.ContainerStatPath(context.Background(), "c1", client.ContainerStatPathOptions{})
				return err
			},
			dangerous: "ContainerStatPath",
		},

		// ── Volume methods (2) ──────────────────────────────────────────

		{
			name:      "VolumeRemove",
			setup:     unmanagedVolume,
			call:      func(e *whail.Engine) error { _, err := e.VolumeRemove(context.Background(), "v1", false); return err },
			dangerous: "VolumeRemove",
		},
		{
			name:        "VolumeInspect",
			setup:       unmanagedVolume,
			call:        func(e *whail.Engine) error { _, err := e.VolumeInspect(context.Background(), "v1"); return err },
			dangerous:   "VolumeInspect",
			inspectSelf: true,
		},

		// ── Network methods (4) ──────────────────────────────────────────

		{
			name:      "NetworkRemove",
			setup:     unmanagedNetwork,
			call:      func(e *whail.Engine) error { _, err := e.NetworkRemove(context.Background(), "n1"); return err },
			dangerous: "NetworkRemove",
		},
		{
			name:  "NetworkInspect",
			setup: unmanagedNetwork,
			call: func(e *whail.Engine) error {
				_, err := e.NetworkInspect(context.Background(), "n1", client.NetworkInspectOptions{})
				return err
			},
			dangerous:   "NetworkInspect",
			inspectSelf: true,
		},
		{
			name:  "NetworkConnect",
			setup: unmanagedNetwork,
			call: func(e *whail.Engine) error {
				_, err := e.NetworkConnect(context.Background(), "n1", "c1", &network.EndpointSettings{})
				return err
			},
			dangerous: "NetworkConnect",
		},
		{
			name:  "NetworkDisconnect",
			setup: unmanagedNetwork,
			call: func(e *whail.Engine) error {
				_, err := e.NetworkDisconnect(context.Background(), "n1", "c1", false)
				return err
			},
			dangerous: "NetworkDisconnect",
		},

		// ── Image methods (2) ───────────────────────────────────────────

		{
			name:  "ImageRemove",
			setup: unmanagedImage,
			call: func(e *whail.Engine) error {
				_, err := e.ImageRemove(context.Background(), "img1", client.ImageRemoveOptions{})
				return err
			},
			dangerous: "ImageRemove",
		},
		{
			name:        "ImageInspect",
			setup:       unmanagedImage,
			call:        func(e *whail.Engine) error { _, err := e.ImageInspect(context.Background(), "img1"); return err },
			dangerous:   "ImageInspect",
			inspectSelf: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := whailtest.NewFakeAPIClient()
			tc.setup(fake)
			eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

			err := tc.call(eng)

			// Must return an error.
			if err == nil {
				t.Fatal("expected error for unmanaged resource, got nil")
			}

			// Error must be a *DockerError.
			var dockerErr *whail.DockerError
			if !errors.As(err, &dockerErr) {
				t.Errorf("expected *whail.DockerError, got %T: %v", err, err)
			}

			// The downstream moby operation must not have been forwarded.
			if tc.inspectSelf {
				// Inspect methods: the managed check calls the same moby method once.
				// The actual operation call (second invocation) must not happen.
				whailtest.AssertCalledN(t, fake, tc.dangerous, 1)
			} else {
				whailtest.AssertNotCalled(t, fake, tc.dangerous)
			}
		})
	}
}

// TestJail_InjectsLabels verifies that every INJECT_LABELS-category method adds
// the managed label to create/build requests before forwarding to moby.
//
// Each subtest uses an input-spy closure to capture the options that whail sends
// to the underlying moby APIClient, then asserts the managed label is present.
func TestJail_InjectsLabels(t *testing.T) {
	managedKey := whailtest.TestLabelPrefix + "." + whailtest.TestManagedLabel

	t.Run("ContainerCreate", func(t *testing.T) {
		fake := whailtest.NewFakeAPIClient()
		var capturedLabels map[string]string
		fake.ContainerCreateFn = func(_ context.Context, opts client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
			capturedLabels = opts.Config.Labels
			return client.ContainerCreateResult{}, nil
		}
		eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

		_, err := eng.ContainerCreate(context.Background(), whail.ContainerCreateOptions{
			Config: &container.Config{},
			Name:   "test",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLabels[managedKey] != "true" {
			t.Errorf("expected managed label %s=true, got labels: %v", managedKey, capturedLabels)
		}
	})

	t.Run("VolumeCreate", func(t *testing.T) {
		fake := whailtest.NewFakeAPIClient()
		var capturedLabels map[string]string
		fake.VolumeCreateFn = func(_ context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
			capturedLabels = opts.Labels
			return client.VolumeCreateResult{}, nil
		}
		eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

		_, err := eng.VolumeCreate(context.Background(), client.VolumeCreateOptions{Name: "test-vol"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLabels[managedKey] != "true" {
			t.Errorf("expected managed label %s=true, got labels: %v", managedKey, capturedLabels)
		}
	})

	t.Run("NetworkCreate", func(t *testing.T) {
		fake := whailtest.NewFakeAPIClient()
		var capturedLabels map[string]string
		fake.NetworkCreateFn = func(_ context.Context, _ string, opts client.NetworkCreateOptions) (client.NetworkCreateResult, error) {
			capturedLabels = opts.Labels
			return client.NetworkCreateResult{}, nil
		}
		eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

		_, err := eng.NetworkCreate(context.Background(), "test-net", client.NetworkCreateOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLabels[managedKey] != "true" {
			t.Errorf("expected managed label %s=true, got labels: %v", managedKey, capturedLabels)
		}
	})

	t.Run("ImageBuild", func(t *testing.T) {
		fake := whailtest.NewFakeAPIClient()
		var capturedLabels map[string]string
		fake.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			capturedLabels = opts.Labels
			return client.ImageBuildResult{}, nil
		}
		eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

		_, err := eng.ImageBuild(context.Background(), nil, client.ImageBuildOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLabels[managedKey] != "true" {
			t.Errorf("expected managed label %s=true, got labels: %v", managedKey, capturedLabels)
		}
	})
}

// TestJail_InjectsFilter verifies that every INJECT_FILTER-category method adds
// the managed label filter to list/prune queries before forwarding to moby.
//
// Each subtest uses an input-spy closure to capture the Filters that whail sends
// to the underlying moby APIClient, then asserts the managed filter entry is present.
func TestJail_InjectsFilter(t *testing.T) {
	managedFilterEntry := whailtest.TestLabelPrefix + "." + whailtest.TestManagedLabel + "=true"

	tests := []struct {
		name  string
		setup func(fake *whailtest.FakeAPIClient, captured *client.Filters)
		call  func(e *whail.Engine)
	}{
		// ── Container list methods ──────────────────────────────────────

		{
			name: "ContainerList",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.ContainerListFn = func(_ context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
					*captured = opts.Filters
					return client.ContainerListResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.ContainerList(context.Background(), client.ContainerListOptions{})
			},
		},
		{
			name: "ContainerListAll",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.ContainerListFn = func(_ context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
					*captured = opts.Filters
					return client.ContainerListResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.ContainerListAll(context.Background())
			},
		},
		{
			name: "ContainerListRunning",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.ContainerListFn = func(_ context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
					*captured = opts.Filters
					return client.ContainerListResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.ContainerListRunning(context.Background())
			},
		},
		{
			name: "ContainerListByLabels",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.ContainerListFn = func(_ context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
					*captured = opts.Filters
					return client.ContainerListResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.ContainerListByLabels(context.Background(), nil, true)
			},
		},
		{
			name: "FindContainerByName",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.ContainerListFn = func(_ context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
					*captured = opts.Filters
					// Return matching container so the method doesn't error.
					return client.ContainerListResult{
						Items: []container.Summary{{Names: []string{"/test"}}},
					}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.FindContainerByName(context.Background(), "test")
			},
		},

		// ── Volume list/prune methods ──────────────────────────────────

		{
			name: "VolumeList",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.VolumeListFn = func(_ context.Context, opts client.VolumeListOptions) (client.VolumeListResult, error) {
					*captured = opts.Filters
					return client.VolumeListResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.VolumeList(context.Background())
			},
		},
		{
			name: "VolumeListAll",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.VolumeListFn = func(_ context.Context, opts client.VolumeListOptions) (client.VolumeListResult, error) {
					*captured = opts.Filters
					return client.VolumeListResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.VolumeListAll(context.Background())
			},
		},
		{
			name: "VolumesPrune",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.VolumePruneFn = func(_ context.Context, opts client.VolumePruneOptions) (client.VolumePruneResult, error) {
					*captured = opts.Filters
					return client.VolumePruneResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.VolumesPrune(context.Background(), false)
			},
		},

		// ── Network list/prune methods ────────────────────────────────

		{
			name: "NetworkList",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.NetworkListFn = func(_ context.Context, opts client.NetworkListOptions) (client.NetworkListResult, error) {
					*captured = opts.Filters
					return client.NetworkListResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.NetworkList(context.Background())
			},
		},
		{
			name: "NetworksPrune",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.NetworkPruneFn = func(_ context.Context, opts client.NetworkPruneOptions) (client.NetworkPruneResult, error) {
					*captured = opts.Filters
					return client.NetworkPruneResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.NetworksPrune(context.Background())
			},
		},

		// ── Image list/prune methods ──────────────────────────────────

		{
			name: "ImageList",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.ImageListFn = func(_ context.Context, opts client.ImageListOptions) (client.ImageListResult, error) {
					*captured = opts.Filters
					return client.ImageListResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.ImageList(context.Background(), client.ImageListOptions{})
			},
		},
		{
			name: "ImagesPrune",
			setup: func(fake *whailtest.FakeAPIClient, captured *client.Filters) {
				fake.ImagePruneFn = func(_ context.Context, opts client.ImagePruneOptions) (client.ImagePruneResult, error) {
					*captured = opts.Filters
					return client.ImagePruneResult{}, nil
				}
			},
			call: func(e *whail.Engine) {
				_, _ = e.ImagesPrune(context.Background(), false)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := whailtest.NewFakeAPIClient()
			var captured client.Filters
			tc.setup(fake, &captured)
			eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

			tc.call(eng)

			if !captured["label"][managedFilterEntry] {
				t.Errorf("expected managed filter entry %q in filters, got: %v", managedFilterEntry, captured)
			}
		})
	}
}

// TestJail_LabelOverridePrevention verifies that callers cannot override the
// managed label by passing managed=false in extra labels. The engine must always
// enforce managed=true regardless of what callers provide.
func TestJail_LabelOverridePrevention(t *testing.T) {
	managedKey := whailtest.TestLabelPrefix + "." + whailtest.TestManagedLabel
	maliciousLabels := map[string]string{managedKey: "false"}

	t.Run("ContainerCreate", func(t *testing.T) {
		fake := whailtest.NewFakeAPIClient()
		var capturedLabels map[string]string
		fake.ContainerCreateFn = func(_ context.Context, opts client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
			capturedLabels = opts.Config.Labels
			return client.ContainerCreateResult{}, nil
		}
		eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

		_, err := eng.ContainerCreate(context.Background(), whail.ContainerCreateOptions{
			Config:      &container.Config{},
			Name:        "test",
			ExtraLabels: whail.Labels{maliciousLabels},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLabels[managedKey] != "true" {
			t.Errorf("managed label was overridden: expected %s=true, got %s=%s",
				managedKey, managedKey, capturedLabels[managedKey])
		}
	})

	t.Run("VolumeCreate", func(t *testing.T) {
		fake := whailtest.NewFakeAPIClient()
		var capturedLabels map[string]string
		fake.VolumeCreateFn = func(_ context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
			capturedLabels = opts.Labels
			return client.VolumeCreateResult{}, nil
		}
		eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

		_, err := eng.VolumeCreate(context.Background(), client.VolumeCreateOptions{Name: "test-vol"}, maliciousLabels)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLabels[managedKey] != "true" {
			t.Errorf("managed label was overridden: expected %s=true, got %s=%s",
				managedKey, managedKey, capturedLabels[managedKey])
		}
	})

	t.Run("NetworkCreate", func(t *testing.T) {
		fake := whailtest.NewFakeAPIClient()
		var capturedLabels map[string]string
		fake.NetworkCreateFn = func(_ context.Context, _ string, opts client.NetworkCreateOptions) (client.NetworkCreateResult, error) {
			capturedLabels = opts.Labels
			return client.NetworkCreateResult{}, nil
		}
		eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

		_, err := eng.NetworkCreate(context.Background(), "test-net", client.NetworkCreateOptions{}, maliciousLabels)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLabels[managedKey] != "true" {
			t.Errorf("managed label was overridden: expected %s=true, got %s=%s",
				managedKey, managedKey, capturedLabels[managedKey])
		}
	})

	t.Run("ImageBuild", func(t *testing.T) {
		fake := whailtest.NewFakeAPIClient()
		var capturedLabels map[string]string
		fake.ImageBuildFn = func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			capturedLabels = opts.Labels
			return client.ImageBuildResult{}, nil
		}
		eng := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

		_, err := eng.ImageBuild(context.Background(), nil, client.ImageBuildOptions{
			Labels: maliciousLabels,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLabels[managedKey] != "true" {
			t.Errorf("managed label was overridden: expected %s=true, got %s=%s",
				managedKey, managedKey, capturedLabels[managedKey])
		}
	})
}
