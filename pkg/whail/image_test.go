package whail

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
)

func TestImageBuild(t *testing.T) {
	imageLabels := testEngine.imageLabels()
	test := []struct {
		name      string
		labels    map[string]string
		shouldErr bool
	}{
		{
			name:      "should build image with managed labels",
			labels:    imageLabels,
			shouldErr: false,
		},
	}
	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dockerfile := "FROM " + testImageBase + "\nCMD [\"echo\", \"test\"]\n"
			buildOpts := types.ImageBuildOptions{
				Tags:       []string{"test-build-image:latest"},
				Labels:     imageLabels,
				Dockerfile: "Dockerfile",
				Remove:     true,
			}

			tarBuf := new(bytes.Buffer)
			if err := createTarWithDockerfile(tarBuf, dockerfile); err != nil {
				t.Fatalf("Failed to create build context: %v", err)
			}

			resp, err := testEngine.ImageBuild(ctx, tarBuf, buildOpts)
			if err != nil {
				t.Fatalf("ImageBuild failed: %v", err)
			}
			defer resp.Body.Close()

			// verify labels applied
			inspect, _, err := testEngine.APIClient.ImageInspectWithRaw(ctx, "test-build-image:latest")
			if err != nil {
				t.Fatalf("Failed to inspect built image: %v", err)
			}
			t.Logf("Built image labels: %+v", inspect.Config.Labels)
			for k, v := range imageLabels {
				if inspect.Config.Labels[k] != v {
					t.Errorf("Expected label %q=%q, got %q", k, v, inspect.Config.Labels[k])
				}
			}

			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ImageBuild failed: %v", err)
			}
		})
	}
}

func TestImageList(t *testing.T) {
	tests := []struct {
		name          string
		searchTag     string
		shouldBeFound bool
	}{
		{
			name:          "should return managed image",
			searchTag:     testImageTag,
			shouldBeFound: true,
		},
		{
			name:          "should not return unmanaged image",
			searchTag:     unmanagedTag,
			shouldBeFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			images, err := testEngine.ImageList(ctx, image.ListOptions{})
			if len(images) == 0 {
				t.Fatalf("No images found, ensure test setup is correct")
			}
			if err != nil {
				t.Fatalf("ImageList failed: %v", err)
			}

			found := false
			for _, img := range images {
				if slices.Contains(img.RepoTags, tt.searchTag) {
					found = true
				}
				if found {
					break
				}
			}

			if found != tt.shouldBeFound {
				t.Errorf("Expected tag %q to be found: %v, but got: %v", tt.searchTag, tt.shouldBeFound, found)
			}
		})
	}
}

func TestImageRemove(t *testing.T) {
	ctx := context.Background()

	// Create a dedicated image for removal test (don't use testImageTag which other tests need)
	removeTestTag := "whail-test-remove:latest"
	tarBuf := new(bytes.Buffer)
	dockerfile := "FROM " + testImageBase + "\nCMD [\"echo\", \"remove-test\"]\n"
	if err := createTarWithDockerfile(tarBuf, dockerfile); err != nil {
		t.Fatalf("Failed to create build context: %v", err)
	}

	buildOpts := types.ImageBuildOptions{
		Tags:       []string{removeTestTag},
		Labels:     testEngine.imageLabels(),
		Dockerfile: "Dockerfile",
		Remove:     true,
	}

	resp, err := testEngine.ImageBuild(ctx, tarBuf, buildOpts)
	if err != nil {
		t.Fatalf("Failed to build test image for removal: %v", err)
	}
	defer resp.Body.Close()

	// Wait for build to complete by draining the response
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)

	tests := []struct {
		name      string
		imageTag  string
		shouldErr bool
	}{
		{
			name:      "should remove managed image",
			imageTag:  removeTestTag,
			shouldErr: false,
		},
		{
			name:      "should not remove unmanaged image",
			imageTag:  unmanagedTag,
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testEngine.ImageRemove(ctx, tt.imageTag, image.RemoveOptions{Force: true})
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ImageRemove failed: %v", err)
			}

			// Verify removal
			_, _, err = testEngine.APIClient.ImageInspectWithRaw(ctx, tt.imageTag)
			if err == nil {
				t.Fatalf("Expected image to be removed, but it still exists")
			}
		})
	}
}


func TestImagesPrune(t *testing.T) {
	ctx := context.Background()

	// Build a managed image for pruning test
	pruneImageTag := "whail-test-prune:latest"

	// Build first image with the tag
	tarBuf1 := new(bytes.Buffer)
	dockerfile1 := "FROM " + testImageBase + "\nCMD [\"echo\", \"prune-test-1\"]\n"
	if err := createTarWithDockerfile(tarBuf1, dockerfile1); err != nil {
		t.Fatalf("Failed to create build context: %v", err)
	}

	buildOpts := types.ImageBuildOptions{
		Tags:       []string{pruneImageTag},
		Labels:     testEngine.imageLabels(),
		Dockerfile: "Dockerfile",
		Remove:     true,
	}

	resp1, err := testEngine.ImageBuild(ctx, tarBuf1, buildOpts)
	if err != nil {
		t.Fatalf("Failed to build first test image: %v", err)
	}
	// Wait for build to complete by draining the response
	buf1 := new(bytes.Buffer)
	buf1.ReadFrom(resp1.Body)
	resp1.Body.Close()

	// Get the first image's ID before it becomes dangling
	firstImageInfo, _, err := testEngine.APIClient.ImageInspectWithRaw(ctx, pruneImageTag)
	if err != nil {
		t.Fatalf("Failed to inspect first image: %v", err)
	}
	firstImageID := firstImageInfo.ID
	t.Logf("First image ID: %s", firstImageID)

	// Build a second image with the SAME tag - this makes the first image dangling
	// (the first image loses its tag but still exists as an untagged/dangling image)
	tarBuf2 := new(bytes.Buffer)
	dockerfile2 := "FROM " + testImageBase + "\nCMD [\"echo\", \"prune-test-2\"]\n"
	if err := createTarWithDockerfile(tarBuf2, dockerfile2); err != nil {
		t.Fatalf("Failed to create second build context: %v", err)
	}

	resp2, err := testEngine.ImageBuild(ctx, tarBuf2, buildOpts)
	if err != nil {
		t.Fatalf("Failed to build second test image: %v", err)
	}
	buf2 := new(bytes.Buffer)
	buf2.ReadFrom(resp2.Body)
	resp2.Body.Close()

	// Verify the first image is now dangling (exists but untagged)
	danglingFilter := filters.NewArgs()
	danglingFilter.Add("dangling", "true")
	danglingImages, err := testEngine.APIClient.ImageList(ctx, image.ListOptions{Filters: danglingFilter})
	if err != nil {
		t.Fatalf("Failed to list dangling images: %v", err)
	}

	var foundDangling bool
	for _, img := range danglingImages {
		if img.ID == firstImageID {
			foundDangling = true
			t.Logf("Confirmed first image is now dangling: %s", img.ID)
			break
		}
	}
	if !foundDangling {
		t.Skip("First image did not become dangling (may share layers with base image)")
	}

	// Prune dangling images
	report, err := testEngine.ImagesPrune(ctx, true)
	if err != nil {
		t.Fatalf("ImagesPrune failed: %v", err)
	}

	t.Logf("Pruned %d images, reclaimed %d bytes", len(report.ImagesDeleted), report.SpaceReclaimed)

	// Verify the dangling image was pruned
	_, _, err = testEngine.APIClient.ImageInspectWithRaw(ctx, firstImageID)
	if err == nil {
		t.Errorf("Expected first image to be pruned, but it still exists")
	}

	// Verify the second (tagged) image still exists
	_, _, err = testEngine.APIClient.ImageInspectWithRaw(ctx, pruneImageTag)
	if err != nil {
		t.Errorf("Second image should still exist but got error: %v", err)
	}

	// Verify unmanaged image still exists
	_, _, err = testEngine.APIClient.ImageInspectWithRaw(ctx, unmanagedTag)
	if err != nil {
		t.Errorf("Unmanaged image should not be pruned but got error: %v", err)
	}

	// Cleanup: remove the second test image
	testEngine.APIClient.ImageRemove(ctx, pruneImageTag, image.RemoveOptions{Force: true})
}

func TestImageInspect(t *testing.T) {
	tests := []struct {
		name      string
		imageTag  string
		shouldErr bool
	}{
		{
			name:      "should inspect managed image",
			imageTag:  testImageTag,
			shouldErr: false,
		},
		{
			name:      "should not inspect unmanaged image",
			imageTag:  unmanagedTag,
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			info, err := testEngine.ImageInspect(ctx, tt.imageTag)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ImageInspect failed: %v", err)
			}

			if info.ID == "" {
				t.Errorf("Expected non-empty image ID")
			}
		})
	}
}
