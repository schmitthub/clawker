package clawker

// MountableHostSchemes lists the Docker daemon-address schemes whose remainder
// is a host filesystem path — the only address forms a socket bind mount can
// take as a source. This is the domain's supported set: expanding socket-mount
// support to another address form starts by adding its scheme here. Each
// package that mounts the socket enforces the constraint itself against this
// list.
//
//nolint:gochecknoglobals // immutable domain vocabulary — the one spot socket-mount support expands
var MountableHostSchemes = []string{"unix://"}
