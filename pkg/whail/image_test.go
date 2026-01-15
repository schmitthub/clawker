package whail

import (
	"context"
	"slices"
	"testing"

	"bytes"

	"github.com/docker/docker/api/types"
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
	tests := []struct {
		name      string
		imageTag  string
		shouldErr bool
	}{
		{
			name:      "should remove managed image",
			imageTag:  testImageTag,
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
			ctx := context.Background()

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
