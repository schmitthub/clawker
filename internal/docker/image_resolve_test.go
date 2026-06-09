package docker

import (
	"context"
	"errors"
	"strings"
	"testing"

	moby "github.com/moby/moby/client"
)

func TestFindGlobalImage(t *testing.T) {
	ctx := context.Background()

	t.Run("finds global image and filters by managed label + global reference", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)

		var gotOpts moby.ImageListOptions
		fakeAPI.ImageListFn = func(_ context.Context, opts moby.ImageListOptions) (moby.ImageListResult, error) {
			gotOpts = opts
			return moby.ImageListResult{
				Items: []ImageSummary{
					{RepoTags: []string{ImageTag("")}},
				},
			}, nil
		}

		result, err := client.findGlobalImage(ctx)
		if err != nil {
			t.Fatalf("findGlobalImage() unexpected error: %v", err)
		}
		if result != ImageTag("") {
			t.Errorf("findGlobalImage() = %q, want %q", result, ImageTag(""))
		}

		labelFilters := gotOpts.Filters["label"]
		if _, ok := labelFilters[cfg.LabelManaged()+"="+cfg.ManagedLabelValue()]; !ok {
			t.Errorf("ImageList filters missing managed label, got %v", labelFilters)
		}
		refFilters := gotOpts.Filters["reference"]
		if _, ok := refFilters[ImageTag("")]; !ok {
			t.Errorf("ImageList filters missing global reference %q, got %v", ImageTag(""), refFilters)
		}
	})

	t.Run("ignores images without the exact global tag", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)

		fakeAPI.ImageListFn = func(_ context.Context, _ moby.ImageListOptions) (moby.ImageListResult, error) {
			return moby.ImageListResult{
				Items: []ImageSummary{
					{RepoTags: []string{"clawker:v1.0", "clawker-myproject:latest"}},
				},
			}, nil
		}

		result, err := client.findGlobalImage(ctx)
		if err != nil {
			t.Fatalf("findGlobalImage() unexpected error: %v", err)
		}
		if result != "" {
			t.Errorf("findGlobalImage() = %q, want empty string", result)
		}
	})
}

func TestResolveImageWithSource(t *testing.T) {
	ctx := context.Background()

	// imageListByFilter emulates daemon-side filtering: project-label queries
	// return projectItems, global-reference queries return globalItems.
	imageListByFilter := func(projectLabel string, projectItems, globalItems []ImageSummary) func(context.Context, moby.ImageListOptions) (moby.ImageListResult, error) {
		return func(_ context.Context, opts moby.ImageListOptions) (moby.ImageListResult, error) {
			if _, ok := opts.Filters["reference"][ImageTag("")]; ok {
				return moby.ImageListResult{Items: globalItems}, nil
			}
			for key := range opts.Filters["label"] {
				if strings.HasPrefix(key, projectLabel+"=") {
					return moby.ImageListResult{Items: projectItems}, nil
				}
			}
			return moby.ImageListResult{}, nil
		}
	}

	t.Run("project scope resolves project image", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(),
			[]ImageSummary{{RepoTags: []string{"clawker-myproject:latest"}}}, nil)

		result, err := client.ResolveImageWithSource(ctx, "myproject")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Reference != "clawker-myproject:latest" {
			t.Errorf("Reference = %q, want %q", result.Reference, "clawker-myproject:latest")
		}
		if result.Source != ImageSourceProject {
			t.Errorf("Source = %q, want %q", result.Source, ImageSourceProject)
		}
	})

	t.Run("project scope does not ladder to global image", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(),
			nil, []ImageSummary{{RepoTags: []string{ImageTag("")}}})

		result, err := client.ResolveImageWithSource(ctx, "myproject")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil (no project image, global must not leak into project scope), got %+v", result)
		}
	})

	t.Run("global scope resolves global image", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(),
			nil, []ImageSummary{{RepoTags: []string{ImageTag("")}}})

		result, err := client.ResolveImageWithSource(ctx, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result for global image")
		}
		if result.Reference != ImageTag("") {
			t.Errorf("Reference = %q, want %q", result.Reference, ImageTag(""))
		}
		if result.Source != ImageSourceGlobal {
			t.Errorf("Source = %q, want %q", result.Source, ImageSourceGlobal)
		}
	})

	t.Run("global scope never falls back to config build.image", func(t *testing.T) {
		// build.image is a bare base image (no Claude Code, no clawkerd) —
		// handing it to run/create is never right. Locks the removal of the
		// config fallback arm.
		cfg := testConfig(t, `build:
  image: "buildpack-deps:bookworm-scm"`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(), nil, nil)

		result, err := client.ResolveImageWithSource(ctx, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil (config build.image must not resolve), got %+v", result)
		}
	})

	t.Run("project scope never falls back to config build.image", func(t *testing.T) {
		cfg := testConfig(t, `build:
  image: "buildpack-deps:bookworm-scm"`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(), nil, nil)

		result, err := client.ResolveImageWithSource(ctx, "myproject")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil (config build.image must not resolve), got %+v", result)
		}
	})

	t.Run("global scope wraps lookup errors", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)

		listErr := errors.New("daemon unavailable")
		fakeAPI.ImageListFn = func(_ context.Context, _ moby.ImageListOptions) (moby.ImageListResult, error) {
			return moby.ImageListResult{}, listErr
		}

		_, err := client.ResolveImageWithSource(ctx, "")
		if !errors.Is(err, listErr) {
			t.Errorf("error = %v, want wrapped %v", err, listErr)
		}
	})
}
