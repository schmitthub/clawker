package shared

import _ "embed"

// IDMapMountBinary is the pre-compiled static Linux binary for the elevated
// ID-mapped mount helper.
// Built by: make idmap-mount-binary
// Target: GOOS=linux CGO_ENABLED=0 go build ./cmd/idmap-mount
//
// Like the bpffs-delegate embed it never goes into a container image: it runs
// on the Docker host, under sudo, for the moment it takes to attach an
// ID-mapped view of the workspace — an operation the kernel reserves for
// init-namespace CAP_SYS_ADMIN. Embedding it is what lets the CLI offer that
// on a rootless host without shipping a second package or asking the user to
// install anything. It is written to a private temporary directory when a
// view is needed and removed immediately afterwards — see RunElevated.
//
//go:embed assets/idmap-mount
var IDMapMountBinary []byte
