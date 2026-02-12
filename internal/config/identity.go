package config

// Domain is the actual domain name for clawker.
// Used in help text, URLs, and user-facing output.
const Domain = "clawker.dev"

// LabelDomain is the reverse-DNS form of Domain, per Docker/OCI label conventions.
// Used as the prefix for Docker labels (dev.clawker.*).
const LabelDomain = "dev.clawker"

// ContainerUID is the default UID for the non-root user inside clawker containers.
// Used by bundler (Dockerfile generation), docker (volume tar headers, chown),
// containerfs (onboarding tar), and test harness (test Dockerfiles).
const ContainerUID = 1001

// ContainerGID is the default GID for the non-root user inside clawker containers.
const ContainerGID = 1001
