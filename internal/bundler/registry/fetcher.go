package registry

import "context"

// Fetcher defines the interface for fetching package version information.
// This interface enables mocking for tests and supports multiple registry implementations.
type Fetcher interface {
	// FetchVersions retrieves all published versions of a package.
	FetchVersions(ctx context.Context, pkg string) ([]string, error)

	// FetchDistTags retrieves dist-tags (latest, stable, next, etc.) for a package.
	FetchDistTags(ctx context.Context, pkg string) (DistTags, error)
}
