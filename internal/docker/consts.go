package docker

// Docker API filter keys and values used when composing whail.Filters.
const (
	filterLabel  = "label"
	filterName   = "name"
	filterStatus = "status"
	// statusRunning is the ContainerList status filter value for running
	// containers.
	statusRunning = "running"
)

// Image tag vocabulary.
const (
	// latestTag is the default Docker image tag.
	latestTag = "latest"
	// defaultChownImage is the image CopyToVolume's chown step runs when
	// Client.ChownImage is unset.
	defaultChownImage = "busybox:" + latestTag
)
