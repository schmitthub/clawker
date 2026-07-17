package docker

import (
	"context"
	"errors"
	"strings"
	"testing"

	moby "github.com/moby/moby/client"
)

// defaultWantTags is the no-selector preference order: :default alias first,
// legacy :latest fallback.
func defaultWantTags(project string) []string {
	return []string{DefaultAliasImageTag(project), ImageTag(project)}
}

// summaryWithTags builds an ImageSummary carrying only repo tags.
func summaryWithTags(tags ...string) ImageSummary {
	var s ImageSummary
	s.RepoTags = tags
	return s
}

func TestFindGlobalImage(t *testing.T) {
	ctx := context.Background()

	t.Run("finds global image and filters by managed label + global repo", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)

		var gotOpts moby.ImageListOptions
		fakeAPI.ImageListFn = func(_ context.Context, opts moby.ImageListOptions) (moby.ImageListResult, error) {
			gotOpts = opts
			return moby.ImageListResult{
				Items: []ImageSummary{
					summaryWithTags(ImageTag("")),
				},
			}, nil
		}

		result, err := client.findGlobalImage(ctx, defaultWantTags(""))
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
		if _, ok := refFilters[NamePrefix]; !ok {
			t.Errorf("ImageList filters missing global repo reference %q, got %v", NamePrefix, refFilters)
		}
	})

	t.Run("prefers the :default alias over legacy :latest", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)

		fakeAPI.ImageListFn = func(_ context.Context, _ moby.ImageListOptions) (moby.ImageListResult, error) {
			return moby.ImageListResult{
				Items: []ImageSummary{
					summaryWithTags(ImageTag("")),
					summaryWithTags(DefaultAliasImageTag("")),
				},
			}, nil
		}

		result, err := client.findGlobalImage(ctx, defaultWantTags(""))
		if err != nil {
			t.Fatalf("findGlobalImage() unexpected error: %v", err)
		}
		if result != DefaultAliasImageTag("") {
			t.Errorf("findGlobalImage() = %q, want the :default alias %q", result, DefaultAliasImageTag(""))
		}
	})

	t.Run("ignores images without a wanted tag", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)

		fakeAPI.ImageListFn = func(_ context.Context, _ moby.ImageListOptions) (moby.ImageListResult, error) {
			return moby.ImageListResult{
				Items: []ImageSummary{
					summaryWithTags("clawker:v1.0", "clawker-myproject:latest"),
				},
			}, nil
		}

		result, err := client.findGlobalImage(ctx, defaultWantTags(""))
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
			if _, ok := opts.Filters["reference"][NamePrefix]; ok {
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

	t.Run("project scope resolves legacy :latest when no :default exists", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(),
			[]ImageSummary{summaryWithTags("clawker-myproject:latest")}, nil)

		result, err := client.ResolveImageWithSource(ctx, "myproject", "")
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

	t.Run("project scope prefers :default over legacy :latest", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(),
			[]ImageSummary{
				summaryWithTags(ImageTag("myproject")),
				summaryWithTags(DefaultAliasImageTag("myproject"), HarnessImageTag("myproject", "claude")),
			}, nil)

		result, err := client.ResolveImageWithSource(ctx, "myproject", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Reference != DefaultAliasImageTag("myproject") {
			t.Errorf("Reference = %q, want %q", result.Reference, DefaultAliasImageTag("myproject"))
		}
	})

	t.Run("explicit harness tag resolves exactly and never falls back", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(),
			[]ImageSummary{
				summaryWithTags(DefaultAliasImageTag("myproject"), HarnessImageTag("myproject", "claude")),
			}, nil)

		// codex image not built: exact selection must miss, not fall back to default.
		result, err := client.ResolveImageWithSource(ctx, "myproject", "codex")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil for unbuilt @:codex, got %+v", result)
		}

		result, err = client.ResolveImageWithSource(ctx, "myproject", "claude")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result for built @:claude")
		}
		if result.Reference != HarnessImageTag("myproject", "claude") {
			t.Errorf("Reference = %q, want %q", result.Reference, HarnessImageTag("myproject", "claude"))
		}
	})

	t.Run("project scope does not ladder to global image", func(t *testing.T) {
		cfg := testConfig(t, `{}`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(),
			nil, []ImageSummary{summaryWithTags(ImageTag(""))})

		result, err := client.ResolveImageWithSource(ctx, "myproject", "")
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
			nil, []ImageSummary{summaryWithTags(ImageTag(""))})

		result, err := client.ResolveImageWithSource(ctx, "", "")
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
		// build.image is a bare base image (no harness, no clawkerd) —
		// handing it to run/create is never right. Locks the removal of the
		// config fallback arm.
		cfg := testConfig(t, `build:
  image: "buildpack-deps:bookworm-scm"`)
		client, fakeAPI := newTestClientWithConfig(cfg)
		fakeAPI.ImageListFn = imageListByFilter(cfg.LabelProject(), nil, nil)

		result, err := client.ResolveImageWithSource(ctx, "", "")
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

		result, err := client.ResolveImageWithSource(ctx, "myproject", "")
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

		_, err := client.ResolveImageWithSource(ctx, "", "")
		if !errors.Is(err, listErr) {
			t.Errorf("error = %v, want wrapped %v", err, listErr)
		}
	})
}
