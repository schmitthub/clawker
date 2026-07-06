package toolchain

// ManifestFile is the manifest filename inside a toolchain definition
// directory.
const ManifestFile = "toolchain.yaml"

// Fragment filenames inside a toolchain definition directory. A definition
// ships either or both; at least one must be present. The root fragment
// renders in a root-USER region of the generated Dockerfile, the user
// fragment in the unprivileged-USER region — one declaration can therefore
// provision a full language toolchain (e.g. node = root LTS install + user
// nvm setup).
const (
	RootFragmentFile = "Dockerfile.toolchain-root.tmpl"
	UserFragmentFile = "Dockerfile.toolchain-user.tmpl"
)

// ToolchainsSubdir is the directory under the user config dir where shipped
// definitions are materialized and user definitions live by convention. It
// is also the subdirectory of a harness bundle holding bundle-embedded
// definitions.
const ToolchainsSubdir = "toolchains"
