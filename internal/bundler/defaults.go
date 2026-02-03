package bundler

// FlavorOption represents a Linux flavor choice for image building.
type FlavorOption struct {
	Name        string
	Description string
}

// DefaultFlavorOptions returns the available Linux flavors for base images.
func DefaultFlavorOptions() []FlavorOption {
	return []FlavorOption{
		{Name: "bookworm", Description: "Debian stable (Recommended)"},
		{Name: "trixie", Description: "Debian testing"},
		{Name: "alpine3.22", Description: "Alpine Linux 3.22"},
		{Name: "alpine3.23", Description: "Alpine Linux 3.23"},
	}
}

// FlavorToImage maps a flavor name to its full Docker image reference.
// For known flavors, it returns the appropriate base image.
// For unknown flavors (custom images), it returns the input as-is.
func FlavorToImage(flavor string) string {
	switch flavor {
	case "bookworm":
		return "buildpack-deps:bookworm-scm"
	case "trixie":
		return "buildpack-deps:trixie-scm"
	case "alpine3.22":
		return "alpine:3.22"
	case "alpine3.23":
		return "alpine:3.23"
	default:
		return flavor // Custom image passed through as-is
	}
}
