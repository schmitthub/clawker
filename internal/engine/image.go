package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/docker/docker/api/types"
)

// ImageManager handles Docker image operations
type ImageManager struct {
	engine *Engine
}

// NewImageManager creates a new image manager
func NewImageManager(engine *Engine) *ImageManager {
	return &ImageManager{engine: engine}
}

// EnsureImage ensures an image is available locally, pulling if necessary
func (im *ImageManager) EnsureImage(imageRef string) error {
	exists, err := im.engine.ImageExists(imageRef)
	if err != nil {
		return err
	}

	if exists {
		logger.Debug().Str("image", imageRef).Msg("image exists locally")
		return nil
	}

	logger.Info().Str("image", imageRef).Msg("pulling image")

	reader, err := im.engine.ImagePull(imageRef)
	if err != nil {
		return err
	}
	defer reader.Close()

	// Process the pull output
	return im.processPullOutput(reader)
}

// BuildImage builds a Docker image from a build context
func (im *ImageManager) BuildImage(buildContext io.Reader, tag string, dockerfile string, buildArgs map[string]*string) error {
	options := types.ImageBuildOptions{
		Tags:       []string{tag},
		Dockerfile: dockerfile,
		Remove:     true,
		BuildArgs:  buildArgs,
	}

	resp, err := im.engine.ImageBuild(buildContext, options)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Process the build output
	return im.processBuildOutput(resp.Body)
}

// processPullOutput processes and displays Docker pull output
func (im *ImageManager) processPullOutput(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)
	var lastStatus string

	for scanner.Scan() {
		var event struct {
			Status         string `json:"status"`
			Progress       string `json:"progress"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
			Error string `json:"error"`
		}

		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}

		if event.Error != "" {
			return fmt.Errorf("pull error: %s", event.Error)
		}

		// Only log status changes
		status := event.Status
		if event.Progress != "" {
			status = fmt.Sprintf("%s %s", event.Status, event.Progress)
		}

		if status != lastStatus && event.Status != "" {
			// Only show download progress for interesting statuses
			if strings.Contains(event.Status, "Pull") ||
				strings.Contains(event.Status, "Download") ||
				strings.Contains(event.Status, "Extracting") {
				logger.Debug().Str("status", event.Status).Msg("pull progress")
			}
			lastStatus = status
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading pull output: %w", err)
	}

	logger.Info().Msg("image pull complete")
	return nil
}

// processBuildOutput processes and displays Docker build output
func (im *ImageManager) processBuildOutput(reader io.Reader) error {
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		var event struct {
			Stream      string `json:"stream"`
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}

		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}

		if event.Error != "" {
			return fmt.Errorf("build error: %s", event.Error)
		}

		if event.ErrorDetail.Message != "" {
			return fmt.Errorf("build error: %s", event.ErrorDetail.Message)
		}

		// Log build output (trimmed)
		if stream := strings.TrimSpace(event.Stream); stream != "" {
			// Only show step progress in debug mode
			if strings.HasPrefix(stream, "Step ") {
				logger.Info().Msg(stream)
			} else {
				logger.Debug().Msg(stream)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading build output: %w", err)
	}

	logger.Info().Msg("image build complete")
	return nil
}

// ImageTag returns the standard tag for a claucker-built image
func ImageTag(projectName string) string {
	return fmt.Sprintf("claucker/%s:latest", projectName)
}

// IsAlpineImage checks if an image reference appears to be Alpine-based
func IsAlpineImage(imageRef string) bool {
	imageRef = strings.ToLower(imageRef)
	return strings.Contains(imageRef, "alpine")
}

// IsDebianImage checks if an image reference appears to be Debian-based
func IsDebianImage(imageRef string) bool {
	imageRef = strings.ToLower(imageRef)
	return strings.Contains(imageRef, "debian") ||
		strings.Contains(imageRef, "ubuntu") ||
		strings.Contains(imageRef, "bookworm") ||
		strings.Contains(imageRef, "bullseye") ||
		strings.Contains(imageRef, "trixie") ||
		strings.Contains(imageRef, "slim")
}
