package whail

// Docker API filter keys and values used when building Filters. whail is
// a standalone moby decoration layer — it must not import clawker
// internal packages, so this vocabulary is package-local.
const (
	filterLabel    = "label"
	filterName     = "name"
	filterDangling = "dangling"
	filterTrue     = "true"
	filterFalse    = "false"
	// defaultManagedLabelValue is the value every managed-resource label carries.
	defaultManagedLabelValue = "true"
)
