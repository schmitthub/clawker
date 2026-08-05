package manager

import _ "embed"

// BPFFSDelegateBinary is the pre-compiled static Linux binary for the
// elevated BPF filesystem helper.
// Built by: make bpffs-delegate-binary
// Target: GOOS=linux CGO_ENABLED=0 go build ./cmd/bpffs-delegate
//
// Unlike its four sibling embeds, this binary never goes into a container
// image: it runs on the Docker host, under sudo, for the seconds it takes to
// complete the two BPF filesystem operations the kernel reserves for
// init-namespace CAP_SYS_ADMIN. Embedding it is what lets the CLI offer that
// assistance without shipping a second package or asking the user to install
// anything. It is written to a private temporary directory at heal time and
// removed immediately afterwards — see healBPFFSDelegation.
//
//go:embed assets/bpffs-delegate
var BPFFSDelegateBinary []byte
