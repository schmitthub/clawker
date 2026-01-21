//go:build integration

package cmdutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/spf13/cobra"
)

// Test images created during setup for FindProjectImage tests
var (
	testDockerClient    *docker.Client
	testProjectName     string
	testLatestImageTag  string
	testVersionedTag    string
	testOtherProjectTag string
	testLatestImageID   string
	testVersionedImageID string
	testOtherProjectID  string
	dockerAvailable     bool
)

const testImageBase = "alpine:latest"

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Check Docker is available
	cli, err := client.New(client.FromEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Docker not available, running non-Docker tests only: %v\n", err)
		dockerAvailable = false
		os.Exit(m.Run())
	}
	defer cli.Close()

	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		fmt.Fprintf(os.Stderr, "Docker not running, running non-Docker tests only: %v\n", err)
		dockerAvailable = false
		os.Exit(m.Run())
	}

	dockerAvailable = true

	// Create unique identifiers for this test run
	timestamp := time.Now().UnixNano()
	testProjectName = fmt.Sprintf("cmdutil-test-%d", timestamp)
	testLatestImageTag = fmt.Sprintf("cmdutil-test-%d:latest", timestamp)        // :latest suffix for matching
	testVersionedTag = fmt.Sprintf("cmdutil-test-%d:v1.0", timestamp)             // versioned tag (no :latest)
	testOtherProjectTag = fmt.Sprintf("cmdutil-test-other-%d:latest", timestamp)  // different project with :latest

	// Setup: Create test client and images
	if err := setupDockerTests(ctx, cli); err != nil {
		fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
		cleanupDockerTests(ctx, cli)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Cleanup
	cleanupDockerTests(ctx, cli)

	os.Exit(code)
}

func setupDockerTests(ctx context.Context, cli *client.Client) error {
	var err error

	// Pull base image
	reader, err := cli.ImagePull(ctx, testImageBase, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull base image: %w", err)
	}
	defer reader.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)

	// Create docker.Client for tests
	testDockerClient, err = docker.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	// Build test image with :latest-suffixed tag and matching project label
	testLatestImageID, err = buildTestImage(ctx, cli, testLatestImageTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: testProjectName,
	})
	if err != nil {
		return fmt.Errorf("failed to build latest image: %w", err)
	}

	// Build test image with versioned tag (no :latest)
	testVersionedImageID, err = buildTestImage(ctx, cli, testVersionedTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: testProjectName,
	})
	if err != nil {
		return fmt.Errorf("failed to build versioned image: %w", err)
	}

	// Build test image for different project
	testOtherProjectID, err = buildTestImage(ctx, cli, testOtherProjectTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: "other-project",
	})
	if err != nil {
		return fmt.Errorf("failed to build other project image: %w", err)
	}

	return nil
}

func buildTestImage(ctx context.Context, cli *client.Client, tag string, labels map[string]string) (string, error) {
	dockerfile := "FROM " + testImageBase + "\nCMD [\"echo\", \"test\"]\n"
	buildOpts := client.ImageBuildOptions{
		Tags:       []string{tag},
		Labels:     labels,
		Dockerfile: "Dockerfile",
		Remove:     true,
	}

	tarBuf := new(bytes.Buffer)
	if err := createTarWithDockerfile(tarBuf, dockerfile); err != nil {
		return "", err
	}

	resp, err := cli.ImageBuild(ctx, tarBuf, buildOpts)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)

	inspect, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		return "", err
	}

	return inspect.ID, nil
}

func createTarWithDockerfile(buf *bytes.Buffer, dockerfile string) error {
	name := "Dockerfile"
	content := []byte(dockerfile)
	size := len(content)

	header := make([]byte, 512)
	copy(header[0:100], name)
	copy(header[100:108], fmt.Sprintf("%07o\x00", 0644))
	copy(header[108:116], fmt.Sprintf("%07o\x00", 0))
	copy(header[116:124], fmt.Sprintf("%07o\x00", 0))
	copy(header[124:136], fmt.Sprintf("%011o\x00", size))
	copy(header[136:148], fmt.Sprintf("%011o\x00", time.Now().Unix()))
	header[156] = '0'

	copy(header[148:156], "        ")
	var checksum int64
	for _, b := range header {
		checksum += int64(b)
	}
	copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))

	buf.Write(header)
	buf.Write(content)

	padding := 512 - (size % 512)
	if padding < 512 {
		buf.Write(make([]byte, padding))
	}

	buf.Write(make([]byte, 1024))

	return nil
}

func cleanupDockerTests(ctx context.Context, cli *client.Client) {
	if testDockerClient != nil {
		testDockerClient.Close()
	}

	// Remove test images
	for _, id := range []string{testLatestImageID, testVersionedImageID, testOtherProjectID} {
		if id != "" {
			_, _ = cli.ImageRemove(ctx, id, client.ImageRemoveOptions{Force: true, PruneChildren: true})
		}
	}

	// Also try removing by tag in case IDs didn't work
	for _, tag := range []string{testLatestImageTag, testVersionedTag, testOtherProjectTag} {
		_, _ = cli.ImageRemove(ctx, tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	}
}

func TestResolveDefaultImage(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		settings *config.Settings
		want     string
	}{
		{
			name:     "both nil returns empty",
			cfg:      nil,
			settings: nil,
			want:     "",
		},
		{
			name: "config takes precedence over settings",
			cfg: &config.Config{
				DefaultImage: "config-image:latest",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-image:latest",
				},
			},
			want: "config-image:latest",
		},
		{
			name: "settings fallback when config empty",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-image:latest",
				},
			},
			want: "settings-image:latest",
		},
		{
			name:     "settings only (nil config)",
			cfg:      nil,
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-only:latest",
				},
			},
			want: "settings-only:latest",
		},
		{
			name: "config only (nil settings)",
			cfg: &config.Config{
				DefaultImage: "config-only:latest",
			},
			settings: nil,
			want:     "config-only:latest",
		},
		{
			name: "both empty returns empty",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "",
				},
			},
			want: "",
		},
		{
			name: "empty config with nil settings",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: nil,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveDefaultImage(tt.cfg, tt.settings)
			if got != tt.want {
				t.Errorf("ResolveDefaultImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImage_ExplicitImage(t *testing.T) {
	// When explicit image is provided, it should always be returned
	tests := []struct {
		name          string
		cfg           *config.Config
		settings      *config.Settings
		explicitImage string
		want          string
	}{
		{
			name:          "explicit image takes precedence over all",
			cfg:           &config.Config{DefaultImage: "config-image:latest"},
			settings:      &config.Settings{Project: config.ProjectDefaults{DefaultImage: "settings-image:latest"}},
			explicitImage: "explicit:v1",
			want:          "explicit:v1",
		},
		{
			name:          "explicit image with nil config and settings",
			cfg:           nil,
			settings:      nil,
			explicitImage: "explicit:v2",
			want:          "explicit:v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: We pass nil for dockerClient since we're testing with explicit image
			// which doesn't need Docker lookup
			got, err := ResolveImage(context.TODO(), nil, tt.cfg, tt.settings, tt.explicitImage)
			if err != nil {
				t.Fatalf("ResolveImage() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImage_FallbackToDefault(t *testing.T) {
	// When no explicit image, should fall back to default from config/settings
	tests := []struct {
		name     string
		cfg      *config.Config
		settings *config.Settings
		want     string
	}{
		{
			name: "falls back to config default",
			cfg: &config.Config{
				DefaultImage: "config-default:latest",
			},
			settings: nil,
			want:     "config-default:latest",
		},
		{
			name: "falls back to settings default",
			cfg: &config.Config{
				DefaultImage: "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "settings-default:latest",
				},
			},
			want: "settings-default:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveImage(context.TODO(), nil, tt.cfg, tt.settings, "")
			if err != nil {
				t.Fatalf("ResolveImage() returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveImage_NilParameters(t *testing.T) {
	// Test with all nil parameters - should return empty string, no error
	got, err := ResolveImage(context.TODO(), nil, nil, nil, "")
	if err != nil {
		t.Fatalf("ResolveImage() returned unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("ResolveImage() with all nil = %q, want empty string", got)
	}
}

func TestResolveImage_EmptyExplicitUsesDefaults(t *testing.T) {
	cfg := &config.Config{
		DefaultImage: "default-image:v1",
	}

	// Empty explicit image should use default
	got, err := ResolveImage(context.TODO(), nil, cfg, nil, "")
	if err != nil {
		t.Fatalf("ResolveImage() returned unexpected error: %v", err)
	}
	if got != "default-image:v1" {
		t.Errorf("ResolveImage() = %q, want %q", got, "default-image:v1")
	}
}

func TestFindProjectImage(t *testing.T) {
	ctx := context.Background()

	t.Run("nil docker client returns empty string", func(t *testing.T) {
		result, err := FindProjectImage(ctx, nil, "myproject")
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
		}
		if result != "" {
			t.Errorf("FindProjectImage() = %q, want empty string", result)
		}
	})

	t.Run("empty project string returns empty string", func(t *testing.T) {
		// Even with a valid client, empty project should return empty
		result, err := FindProjectImage(ctx, testDockerClient, "")
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
		}
		if result != "" {
			t.Errorf("FindProjectImage() = %q, want empty string", result)
		}
	})

	// Docker-dependent tests
	if !dockerAvailable {
		t.Skip("Skipping Docker-dependent tests: Docker not available")
	}

	t.Run("image matches with :latest tag", func(t *testing.T) {
		result, err := FindProjectImage(ctx, testDockerClient, testProjectName)
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
			return
		}
		// Our test image tag ends with ":latest"
		if result == "" {
			t.Errorf("FindProjectImage() returned empty string, expected image with :latest suffix")
			return
		}
		// Verify the result ends with ":latest"
		expectedSuffix := ":latest"
		if len(result) < len(expectedSuffix) || result[len(result)-len(expectedSuffix):] != expectedSuffix {
			t.Errorf("FindProjectImage() = %q, want suffix %q", result, expectedSuffix)
		}
	})

	t.Run("no matching images for nonexistent project", func(t *testing.T) {
		result, err := FindProjectImage(ctx, testDockerClient, "nonexistent-project-xyz")
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
			return
		}
		if result != "" {
			t.Errorf("FindProjectImage() = %q, want empty string for nonexistent project", result)
		}
	})

	t.Run("finds correct project image among multiple", func(t *testing.T) {
		// Test that it correctly filters by project label
		result, err := FindProjectImage(ctx, testDockerClient, "other-project")
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
			return
		}
		// other-project also has a :latest tag
		if result == "" {
			t.Errorf("FindProjectImage() returned empty string, expected image for other-project")
			return
		}
		// Verify it ends with :latest and is the other project's image
		if result != testOtherProjectTag {
			t.Errorf("FindProjectImage() = %q, want %q", result, testOtherProjectTag)
		}
	})
}

func TestFindProjectImage_NoLatestTag(t *testing.T) {
	if !dockerAvailable {
		t.Skip("Skipping Docker-dependent test: Docker not available")
	}

	ctx := context.Background()

	// Test with a project name that has no images at all
	// This verifies the function returns empty string when no :latest tag exists
	result, err := FindProjectImage(ctx, testDockerClient, "project-with-absolutely-no-images")
	if err != nil {
		t.Errorf("FindProjectImage() unexpected error: %v", err)
		return
	}
	if result != "" {
		t.Errorf("FindProjectImage() = %q, want empty string for project with no images", result)
	}
}

func TestAgentArgsValidator(t *testing.T) {
	tests := []struct {
		name       string
		minArgs    int
		agentFlag  string
		args       []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:      "valid with positional arg",
			minArgs:   1,
			agentFlag: "",
			args:      []string{"container1"},
			wantErr:   false,
		},
		{
			name:      "valid with multiple positional args",
			minArgs:   1,
			agentFlag: "",
			args:      []string{"container1", "container2"},
			wantErr:   false,
		},
		{
			name:      "valid with agent flag",
			minArgs:   1,
			agentFlag: "ralph",
			args:      []string{},
			wantErr:   false,
		},
		{
			name:       "error: agent and positional args",
			minArgs:    1,
			agentFlag:  "ralph",
			args:       []string{"container1"},
			wantErr:    true,
			wantErrMsg: "--agent and positional container arguments are mutually exclusive",
		},
		{
			name:       "error: no args and no agent",
			minArgs:    1,
			agentFlag:  "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
		{
			name:       "error: not enough args (minArgs=2)",
			minArgs:    2,
			agentFlag:  "",
			args:       []string{"container1"},
			wantErr:    true,
			wantErrMsg: "requires at least 2 container arguments or --agent flag",
		},
		{
			name:      "valid: minArgs=2 with enough args",
			minArgs:   2,
			agentFlag: "",
			args:      []string{"container1", "container2"},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal cobra command with the agent flag
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().String("agent", tt.agentFlag, "")
			if tt.agentFlag != "" {
				cmd.Flags().Set("agent", tt.agentFlag)
			}

			validator := AgentArgsValidator(tt.minArgs)
			err := validator(cmd, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("AgentArgsValidator() expected error, got nil")
					return
				}
				if err.Error() != tt.wantErrMsg {
					t.Errorf("AgentArgsValidator() error = %q, want %q", err.Error(), tt.wantErrMsg)
				}
			} else {
				if err != nil {
					t.Errorf("AgentArgsValidator() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestAgentArgsValidatorExact(t *testing.T) {
	tests := []struct {
		name       string
		n          int
		agentFlag  string
		args       []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:      "valid with exact args",
			n:         2,
			agentFlag: "",
			args:      []string{"container1", "newname"},
			wantErr:   false,
		},
		{
			name:      "valid with agent flag",
			n:         2,
			agentFlag: "ralph",
			args:      []string{},
			wantErr:   false,
		},
		{
			name:       "error: agent and positional args",
			n:          2,
			agentFlag:  "ralph",
			args:       []string{"container1"},
			wantErr:    true,
			wantErrMsg: "--agent and positional container arguments are mutually exclusive",
		},
		{
			name:       "error: too few args (n=2)",
			n:          2,
			agentFlag:  "",
			args:       []string{"container1"},
			wantErr:    true,
			wantErrMsg: "requires exactly 2 container arguments or --agent flag",
		},
		{
			name:       "error: too many args (n=2)",
			n:          2,
			agentFlag:  "",
			args:       []string{"a", "b", "c"},
			wantErr:    true,
			wantErrMsg: "requires exactly 2 container arguments or --agent flag",
		},
		{
			name:       "error: no args (n=1)",
			n:          1,
			agentFlag:  "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires exactly 1 container argument or --agent flag",
		},
		{
			name:      "valid: n=1 with single arg",
			n:         1,
			agentFlag: "",
			args:      []string{"container1"},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal cobra command with the agent flag
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().String("agent", tt.agentFlag, "")
			if tt.agentFlag != "" {
				cmd.Flags().Set("agent", tt.agentFlag)
			}

			validator := AgentArgsValidatorExact(tt.n)
			err := validator(cmd, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("AgentArgsValidatorExact() expected error, got nil")
					return
				}
				if err.Error() != tt.wantErrMsg {
					t.Errorf("AgentArgsValidatorExact() error = %q, want %q", err.Error(), tt.wantErrMsg)
				}
			} else {
				if err != nil {
					t.Errorf("AgentArgsValidatorExact() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestResolveContainerName(t *testing.T) {
	tests := []struct {
		name       string
		project    string
		agentName  string
		wantResult string
		wantErr    bool
	}{
		{
			name:       "valid resolution",
			project:    "myapp",
			agentName:  "ralph",
			wantResult: "clawker.myapp.ralph",
			wantErr:    false,
		},
		{
			name:       "empty project returns error",
			project:    "",
			agentName:  "ralph",
			wantResult: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Factory{}
			f.configData = &config.Config{Project: tt.project}

			result, err := ResolveContainerName(f, tt.agentName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveContainerName() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("ResolveContainerName() unexpected error: %v", err)
					return
				}
				if result != tt.wantResult {
					t.Errorf("ResolveContainerName() = %q, want %q", result, tt.wantResult)
				}
			}
		})
	}
}

func TestResolveContainerName_ConfigError(t *testing.T) {
	f := &Factory{}
	f.configErr = fmt.Errorf("config load error")

	_, err := ResolveContainerName(f, "ralph")
	if err == nil {
		t.Errorf("ResolveContainerName() expected error when config fails, got nil")
	}
}

func TestResolveContainerNames(t *testing.T) {
	tests := []struct {
		name          string
		project       string
		agentName     string
		containerArgs []string
		wantResult    []string
		wantErr       bool
	}{
		{
			name:          "with agent name",
			project:       "myapp",
			agentName:     "ralph",
			containerArgs: nil,
			wantResult:    []string{"clawker.myapp.ralph"},
			wantErr:       false,
		},
		{
			name:          "with container args",
			project:       "myapp",
			agentName:     "",
			containerArgs: []string{"container1", "container2"},
			wantResult:    []string{"container1", "container2"},
			wantErr:       false,
		},
		{
			name:          "empty agent and empty args",
			project:       "myapp",
			agentName:     "",
			containerArgs: []string{},
			wantResult:    []string{},
			wantErr:       false,
		},
		{
			name:          "agent with empty project returns error",
			project:       "",
			agentName:     "ralph",
			containerArgs: nil,
			wantResult:    nil,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Factory{}
			f.configData = &config.Config{Project: tt.project}

			result, err := ResolveContainerNames(f, tt.agentName, tt.containerArgs)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveContainerNames() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("ResolveContainerNames() unexpected error: %v", err)
					return
				}
				if len(result) != len(tt.wantResult) {
					t.Errorf("ResolveContainerNames() len = %d, want %d", len(result), len(tt.wantResult))
					return
				}
				for i, r := range result {
					if r != tt.wantResult[i] {
						t.Errorf("ResolveContainerNames()[%d] = %q, want %q", i, r, tt.wantResult[i])
					}
				}
			}
		})
	}
}

func TestResolveImageWithSource(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		cfg           *config.Config
		settings      *config.Settings
		explicitImage string
		wantRef       string
		wantSource    ImageSource
		wantNil       bool
	}{
		{
			name:          "explicit image takes precedence",
			cfg:           &config.Config{DefaultImage: "config-image:latest", Project: "myproject"},
			settings:      &config.Settings{Project: config.ProjectDefaults{DefaultImage: "settings-image:latest"}},
			explicitImage: "explicit:v1",
			wantRef:       "explicit:v1",
			wantSource:    ImageSourceExplicit,
			wantNil:       false,
		},
		{
			name:          "explicit image with nil config and settings",
			cfg:           nil,
			settings:      nil,
			explicitImage: "explicit:v2",
			wantRef:       "explicit:v2",
			wantSource:    ImageSourceExplicit,
			wantNil:       false,
		},
		{
			name:          "falls back to default from config",
			cfg:           &config.Config{DefaultImage: "config-default:latest"},
			settings:      nil,
			explicitImage: "",
			wantRef:       "config-default:latest",
			wantSource:    ImageSourceDefault,
			wantNil:       false,
		},
		{
			name:          "falls back to default from settings",
			cfg:           &config.Config{DefaultImage: ""},
			settings:      &config.Settings{Project: config.ProjectDefaults{DefaultImage: "settings-default:latest"}},
			explicitImage: "",
			wantRef:       "settings-default:latest",
			wantSource:    ImageSourceDefault,
			wantNil:       false,
		},
		{
			name:          "config default takes precedence over settings",
			cfg:           &config.Config{DefaultImage: "config-default:latest"},
			settings:      &config.Settings{Project: config.ProjectDefaults{DefaultImage: "settings-default:latest"}},
			explicitImage: "",
			wantRef:       "config-default:latest",
			wantSource:    ImageSourceDefault,
			wantNil:       false,
		},
		{
			name:          "returns nil when no image found",
			cfg:           nil,
			settings:      nil,
			explicitImage: "",
			wantRef:       "",
			wantSource:    "",
			wantNil:       true,
		},
		{
			name: "returns nil when all sources empty",
			cfg: &config.Config{
				DefaultImage: "",
				Project:      "",
			},
			settings: &config.Settings{
				Project: config.ProjectDefaults{
					DefaultImage: "",
				},
			},
			explicitImage: "",
			wantRef:       "",
			wantSource:    "",
			wantNil:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: We pass nil for dockerClient for tests that don't need project image lookup
			result, err := ResolveImageWithSource(ctx, nil, tt.cfg, tt.settings, tt.explicitImage)

			if err != nil {
				t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
			}

			if tt.wantNil {
				if result != nil {
					t.Errorf("ResolveImageWithSource() = %+v, want nil", result)
				}
				return
			}

			if result == nil {
				t.Fatal("ResolveImageWithSource() returned nil, want non-nil")
			}

			if result.Reference != tt.wantRef {
				t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, tt.wantRef)
			}
			if result.Source != tt.wantSource {
				t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, tt.wantSource)
			}
		})
	}
}

func TestResolveImageWithSource_ProjectImage(t *testing.T) {
	if !dockerAvailable {
		t.Skip("Skipping Docker-dependent test: Docker not available")
	}

	ctx := context.Background()

	t.Run("finds project image with :latest tag", func(t *testing.T) {
		cfg := &config.Config{
			Project:      testProjectName,
			DefaultImage: "fallback:latest",
		}

		result, err := ResolveImageWithSource(ctx, testDockerClient, cfg, nil, "")
		if err != nil {
			t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("ResolveImageWithSource() returned nil, expected project image")
		}

		if result.Source != ImageSourceProject {
			t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, ImageSourceProject)
		}

		// Should match our test image tag
		if result.Reference != testLatestImageTag {
			t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, testLatestImageTag)
		}
	})

	t.Run("falls back to default when no project image", func(t *testing.T) {
		cfg := &config.Config{
			Project:      "nonexistent-project-xyz",
			DefaultImage: "fallback:latest",
		}

		result, err := ResolveImageWithSource(ctx, testDockerClient, cfg, nil, "")
		if err != nil {
			t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("ResolveImageWithSource() returned nil, expected default image")
		}

		if result.Source != ImageSourceDefault {
			t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, ImageSourceDefault)
		}

		if result.Reference != "fallback:latest" {
			t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, "fallback:latest")
		}
	})

	t.Run("explicit takes precedence over project image", func(t *testing.T) {
		cfg := &config.Config{
			Project:      testProjectName,
			DefaultImage: "fallback:latest",
		}

		result, err := ResolveImageWithSource(ctx, testDockerClient, cfg, nil, "explicit:override")
		if err != nil {
			t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("ResolveImageWithSource() returned nil")
		}

		if result.Source != ImageSourceExplicit {
			t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, ImageSourceExplicit)
		}

		if result.Reference != "explicit:override" {
			t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, "explicit:override")
		}
	})
}
