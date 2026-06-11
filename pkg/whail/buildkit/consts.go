package buildkit

// BuildKit solve vocabulary — frontend/exporter identifiers and attribute
// keys of the bkclient.SolveOpt wire contract.
const (
	// frontendDockerfile is the BuildKit frontend that interprets
	// Dockerfiles.
	frontendDockerfile = "dockerfile.v0"
	// exporterMoby loads the built image into Docker's local image store.
	// Docker's embedded BuildKit registers this in place of the standard
	// "image" exporter, which only exists in standalone buildkitd.
	exporterMoby = "moby"
	// attrNoCache is the frontend attribute requesting cache verification;
	// pair with empty CacheImports to actually disable cache reuse.
	attrNoCache = "no-cache"
	// attrPush / attrName are exporter attributes (registry push toggle,
	// image tag list).
	attrPush = "push"
	attrName = "name"
	// localMountContext / localMountDockerfile are the local mount keys
	// the dockerfile frontend reads the build context and Dockerfile from.
	localMountContext    = "context"
	localMountDockerfile = "dockerfile"
)
