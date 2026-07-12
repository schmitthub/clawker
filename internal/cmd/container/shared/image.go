package shared

import (
	"context"
	"fmt"
	"strings"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// ParseImagePlaceholder splits the "@" / "@:tag" image placeholder. ok is
// false when image is a literal reference. The tag names a registered
// harness — the caller resolves and validates it.
func ParseImagePlaceholder(image string) (string, bool) {
	if image == "@" {
		return "", true
	}
	if rest, found := strings.CutPrefix(image, "@:"); found && rest != "" {
		return rest, true
	}
	return "", false
}

// ResolvePlaceholderImage resolves the "@" / "@:tag" placeholder to a built
// image reference. An explicit tag must name a known harness; the bare
// placeholder prefers the :default alias and falls back to the legacy
// :latest with a rebuild hint.
func ResolvePlaceholderImage(
	ctx context.Context,
	client *docker.Client,
	cfg config.Config,
	ios *iostreams.IOStreams,
	projectName, harnessTag, commandVerb string,
) (string, error) {
	if err := validatePlaceholderHarness(cfg, harnessTag); err != nil {
		return "", err
	}

	resolvedImage, err := client.ResolveImageWithSource(ctx, projectName, harnessTag)
	if err != nil {
		return "", fmt.Errorf("resolving image: %w", err)
	}
	if resolvedImage == nil {
		printPlaceholderNotFound(ios, harnessTag, commandVerb)
		return "", cmdutil.SilentError
	}

	if strings.HasSuffix(resolvedImage.Reference, ":"+consts.ImageTagLatest) {
		cs := ios.ColorScheme()
		fmt.Fprintf(
			ios.ErrOut,
			"%s Using legacy image %s — rebuild with `clawker build` to move to harness-tagged images\n",
			cs.WarningIcon(), resolvedImage.Reference,
		)
	}
	return resolvedImage.Reference, nil
}

// validatePlaceholderHarness rejects @:tag selections that don't name a
// known harness (a shipped bundle or a project-registered one).
func validatePlaceholderHarness(cfg config.Config, harnessTag string) error {
	if harnessTag == "" {
		return nil
	}
	if _, err := intbuild.ResolveHarnessName(cfg, harnessTag); err != nil {
		return fmt.Errorf("resolving harness for @:%s: %w", harnessTag, err)
	}
	if !intbuild.IsKnownHarness(cfg, harnessTag) {
		return fmt.Errorf(
			"@:%s does not name a known harness (a built-in, a loose harness dir, or an installed bundle)", harnessTag)
	}
	return nil
}

// printPlaceholderNotFound emits the no-built-image guidance for the "@" /
// "@:tag" placeholder.
func printPlaceholderNotFound(ios *iostreams.IOStreams, harnessTag, commandVerb string) {
	cs := ios.ColorScheme()
	placeholder := "@"
	if harnessTag != "" {
		placeholder = "@:" + harnessTag
	}
	fmt.Fprintf(ios.ErrOut, "%s No built image found for %q\n", cs.FailureIcon(), placeholder)
	fmt.Fprintf(ios.ErrOut, "\n%s Next steps:\n", cs.InfoIcon())
	if harnessTag != "" {
		fmt.Fprintf(ios.ErrOut, "  1. Build the harness image first: clawker build -t %s\n", harnessTag)
	} else {
		fmt.Fprintln(ios.ErrOut, "  1. Build an image first: clawker build")
	}
	fmt.Fprintf(ios.ErrOut, "  2. Or specify an image: clawker container %s IMAGE\n", commandVerb)
}
