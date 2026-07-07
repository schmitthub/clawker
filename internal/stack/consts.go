package stack

// ManifestFile is the manifest filename inside a stack definition
// directory.
const ManifestFile = "stack.yaml"

// Fragment filenames inside a stack definition directory. A definition
// ships either or both; at least one must be present. The root fragment
// renders in a root-USER region of the generated Dockerfile, the user
// fragment in the unprivileged-USER region — one declaration can therefore
// provision a full language stack (e.g. node = root LTS install + user
// nvm setup).
const (
	RootFragmentFile = "Dockerfile.stack-root.tmpl"
	UserFragmentFile = "Dockerfile.stack-user.tmpl"
)

// StacksSubdir is the directory under the user config dir where shipped
// definitions are materialized and user definitions live by convention. It
// is also the subdirectory of a harness bundle holding bundle-embedded
// definitions.
const StacksSubdir = "stacks"
