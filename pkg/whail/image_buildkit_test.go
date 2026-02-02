package whail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
)

func TestImageBuildKit_NilBuilder(t *testing.T) {
	fake := whailtest.NewFakeAPIClient()
	engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

	err := engine.ImageBuildKit(context.Background(), whail.ImageBuildKitOptions{
		Tags:       []string{"test:latest"},
		ContextDir: "/tmp",
	})
	if err == nil {
		t.Fatal("expected error when BuildKitImageBuilder is nil")
	}

	var dockerErr *whail.DockerError
	if !errors.As(err, &dockerErr) {
		t.Fatalf("expected DockerError, got %T: %v", err, err)
	}
	if dockerErr.Op != "build" {
		t.Errorf("expected op %q, got %q", "build", dockerErr.Op)
	}
}

func TestImageBuildKit_LabelEnforcement(t *testing.T) {
	fake := whailtest.NewFakeAPIClient()
	engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

	var capturedOpts whail.ImageBuildKitOptions
	engine.BuildKitImageBuilder = func(_ context.Context, opts whail.ImageBuildKitOptions) error {
		capturedOpts = opts
		return nil
	}

	callerLabels := map[string]string{
		"app": "myapp",
	}
	err := engine.ImageBuildKit(context.Background(), whail.ImageBuildKitOptions{
		Tags:       []string{"test:latest"},
		ContextDir: "/tmp",
		Labels:     callerLabels,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Managed label must be present
	managedKey := whailtest.TestLabelPrefix + "." + whailtest.TestManagedLabel
	if capturedOpts.Labels[managedKey] != "true" {
		t.Errorf("expected managed label %q=true, got %q", managedKey, capturedOpts.Labels[managedKey])
	}

	// Caller labels must be preserved
	if capturedOpts.Labels["app"] != "myapp" {
		t.Errorf("expected caller label app=myapp, got %q", capturedOpts.Labels["app"])
	}
}

func TestImageBuildKit_ManagedLabelCannotBeOverridden(t *testing.T) {
	fake := whailtest.NewFakeAPIClient()
	engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

	var capturedOpts whail.ImageBuildKitOptions
	engine.BuildKitImageBuilder = func(_ context.Context, opts whail.ImageBuildKitOptions) error {
		capturedOpts = opts
		return nil
	}

	managedKey := whailtest.TestLabelPrefix + "." + whailtest.TestManagedLabel
	err := engine.ImageBuildKit(context.Background(), whail.ImageBuildKitOptions{
		Tags:       []string{"test:latest"},
		ContextDir: "/tmp",
		Labels: map[string]string{
			managedKey: "false", // attempt to override
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Managed label must be forced to "true" regardless of caller
	if capturedOpts.Labels[managedKey] != "true" {
		t.Errorf("managed label was overridden: expected %q=true, got %q", managedKey, capturedOpts.Labels[managedKey])
	}
}

func TestImageBuildKit_BuilderError(t *testing.T) {
	fake := whailtest.NewFakeAPIClient()
	engine := whail.NewFromExisting(fake, whailtest.TestEngineOptions())

	expectedErr := errors.New("solve failed")
	engine.BuildKitImageBuilder = func(_ context.Context, _ whail.ImageBuildKitOptions) error {
		return expectedErr
	}

	err := engine.ImageBuildKit(context.Background(), whail.ImageBuildKitOptions{
		Tags:       []string{"test:latest"},
		ContextDir: "/tmp",
	})
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}
