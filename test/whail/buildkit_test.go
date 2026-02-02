package whail_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/pkg/whail"
)

func TestBuildKit_MinimalImage(t *testing.T) {
	requireBuildKit(t)

	engine := newTestEngine(t)
	tag := uniqueTag("minimal")
	dir := writeDockerfile(t, `FROM alpine:latest
RUN echo hello > /hello.txt`)

	buildImage(t, engine, tag, dir, nil)

	out := execInImage(t, engine, tag, "cat", "/hello.txt")
	if out != "hello" {
		t.Fatalf("expected 'hello', got %q", out)
	}
}

func TestBuildKit_LabelsApplied(t *testing.T) {
	requireBuildKit(t)

	engine := newTestEngine(t)
	rawCli := newRawClient(t)
	tag := uniqueTag("labels")
	dir := writeDockerfile(t, `FROM alpine:latest`)

	customLabels := map[string]string{"com.whail.test.custom": "value123"}
	buildImage(t, engine, tag, dir, customLabels)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use raw client to inspect — we want to see ALL labels, not just managed
	inspect, err := rawCli.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatalf("image inspect: %v", err)
	}

	labels := inspect.Config.Labels

	// Managed label must be present
	if v, ok := labels[testManagedKey]; !ok || v != testManagedValue {
		t.Errorf("managed label %s=%s not found, labels: %v", testManagedKey, testManagedValue, labels)
	}

	// Custom label must be preserved
	if v, ok := labels["com.whail.test.custom"]; !ok || v != "value123" {
		t.Errorf("custom label not preserved, labels: %v", labels)
	}
}

func TestBuildKit_MultipleTags(t *testing.T) {
	requireBuildKit(t)

	engine := newTestEngine(t)
	rawCli := newRawClient(t)
	tag1 := uniqueTag("multi-a")
	tag2 := uniqueTag("multi-b")
	dir := writeDockerfile(t, `FROM alpine:latest`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err := engine.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
		Tags:           []string{tag1, tag2},
		ContextDir:     dir,
		SuppressOutput: true,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Cleanup both tags
	t.Cleanup(func() {
		cleanCtx := context.Background()
		_, _ = rawCli.ImageRemove(cleanCtx, tag1, client.ImageRemoveOptions{Force: true})
		_, _ = rawCli.ImageRemove(cleanCtx, tag2, client.ImageRemoveOptions{Force: true})
	})

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer inspectCancel()

	inspect1, err := rawCli.ImageInspect(inspectCtx, tag1)
	if err != nil {
		t.Fatalf("inspect tag1: %v", err)
	}
	inspect2, err := rawCli.ImageInspect(inspectCtx, tag2)
	if err != nil {
		t.Fatalf("inspect tag2: %v", err)
	}

	if inspect1.ID != inspect2.ID {
		t.Fatalf("tags point to different images: %s vs %s", inspect1.ID, inspect2.ID)
	}
}

func TestBuildKit_BuildArgs(t *testing.T) {
	requireBuildKit(t)

	engine := newTestEngine(t)
	tag := uniqueTag("args")
	dir := writeDockerfile(t, `FROM alpine:latest
ARG MSG=default
RUN echo $MSG > /msg.txt`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	msgVal := "custom-build-arg-value"
	err := engine.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
		Tags:           []string{tag},
		ContextDir:     dir,
		BuildArgs:      map[string]*string{"MSG": &msgVal},
		SuppressOutput: true,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	rawCli := newRawClient(t)
	t.Cleanup(func() {
		cleanCtx := context.Background()
		_, _ = rawCli.ImageRemove(cleanCtx, tag, client.ImageRemoveOptions{Force: true})
	})

	out := execInImage(t, engine, tag, "cat", "/msg.txt")
	if out != msgVal {
		t.Fatalf("expected %q, got %q", msgVal, out)
	}
}

func TestBuildKit_ContextFiles(t *testing.T) {
	requireBuildKit(t)

	engine := newTestEngine(t)
	tag := uniqueTag("context")
	dir := writeDockerfile(t, `FROM alpine:latest
COPY testfile.txt /testfile.txt`)

	writeContextFile(t, dir, "testfile.txt", "context-file-content")
	buildImage(t, engine, tag, dir, nil)

	out := execInImage(t, engine, tag, "cat", "/testfile.txt")
	if out != "context-file-content" {
		t.Fatalf("expected 'context-file-content', got %q", out)
	}
}

func TestBuildKit_CacheMounts(t *testing.T) {
	requireBuildKit(t)

	engine := newTestEngine(t)
	tag := uniqueTag("cache")
	dir := writeDockerfile(t, `FROM alpine:latest
RUN --mount=type=cache,target=/var/cache/test echo "cached-step-1" > /var/cache/test/data.txt
RUN --mount=type=cache,target=/var/cache/test cat /var/cache/test/data.txt > /result.txt`)

	buildImage(t, engine, tag, dir, nil)

	out := execInImage(t, engine, tag, "cat", "/result.txt")
	if !strings.Contains(out, "cached-step-1") {
		t.Fatalf("expected 'cached-step-1' in output, got %q", out)
	}
}

func TestBuildKit_InvalidDockerfile(t *testing.T) {
	requireBuildKit(t)

	engine := newTestEngine(t)
	tag := uniqueTag("invalid")
	dir := writeDockerfile(t, `FORM alpine:latest`)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err := engine.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
		Tags:           []string{tag},
		ContextDir:     dir,
		SuppressOutput: true,
	})
	if err == nil {
		// If it somehow succeeded, clean up
		rawCli := newRawClient(t)
		_, _ = rawCli.ImageRemove(ctx, tag, client.ImageRemoveOptions{Force: true})
		t.Fatal("expected error for invalid Dockerfile, got nil")
	}
}
