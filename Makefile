.PHONY: help \
        clawker clawker-lint clawker-staticcheck clawker-install clawker-clean \
        bpf-deps ebpf ebpf-binary coredns-binary cp-binary \
        release-embeds verify-release-embeds stage-embeds-amd64 stage-embeds-arm64 \
        test test-unit test-ci test-commands test-whail test-internals test-agents test-acceptance test-all test-coverage test-clean test-e2e \
        changelog-preview \
        licenses licenses-check \
        docs docs-check \
        pre-commit pre-commit-install \
        localenv \
        restart \
        release

# Go Clawker variables
BINARY_NAME := clawker
CLAWKER_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
CLAWKER_REVISION ?= $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
GO ?= go
# Append to (not clobber) any inherited GOFLAGS: worktree containers set
# GOFLAGS=-buildvcs=false because Go cannot stamp linked worktrees there
# (the .git-file walk lands on the mounted main .git and exits 128).
# (make re-exports the merged value to recipes only when GOFLAGS was already
# in the environment; host builds without GOFLAGS are unchanged.)
GOFLAGS := -trimpath $(GOFLAGS)
# Dev builds leave build.Date empty; release goreleaser stamps it via
# {{.CommitDate}} in .goreleaser.yaml.
LDFLAGS := -s -w \
	-X 'github.com/schmitthub/clawker/internal/build.Version=$(CLAWKER_VERSION)' \
	-X 'github.com/schmitthub/clawker/internal/build.Revision=$(CLAWKER_REVISION)'
BIN_DIR := bin
DIST_DIR := dist
# Staging directory for per-arch linux embed sets used by the release pipeline.
# Populated by `make release-embeds`, consumed by `make stage-embeds-{amd64,arm64}`
# from goreleaser's per-build-id pre-hooks. Outside dist/ so `goreleaser release
# --clean` cannot wipe it.
RELEASE_EMBED_STAGE := embeds

# Test runner configuration
# Use gotestsum if available for human-friendly output, fall back to go test
GOTESTSUM := $(shell command -v gotestsum 2>/dev/null)
ifdef GOTESTSUM
	# gotestsum with human-friendly format: icons, colors, package names
	TEST_CMD = gotestsum --format testdox --
	TEST_CMD_VERBOSE = gotestsum --format standard-verbose --
else
	TEST_CMD = $(GO) test
	TEST_CMD_VERBOSE = $(GO) test -v
endif

help:
	@echo "Clawker Makefile"
	@echo ""
	@echo "Test targets:"
	@echo "  test                Unit tests only (fast, no Docker)"
	@echo "  test-unit           Alias for 'test'"
	@echo "  test-ci             Unit tests with race detector, no cache, coverage (CI mode)"
	@echo "  test-commands       Command integration tests (requires Docker)"
	@echo "  test-internals      Internal integration tests (requires Docker)"
	@echo "  test-acceptance     Clawker acceptance tests via testscript (requires Docker)"
	@echo "  test-e2e            End-to-end firewall stack tests (requires Docker)"
	@echo "  test-whail          Whail BuildKit integration tests (requires Docker + BuildKit)"
	@echo "  test-agents         Agent E2E tests (requires Docker)"
	@echo "  test-all            Run all test suites"
	@echo "  test-coverage       Unit tests with coverage"
	@echo "  test-clean          Remove test Docker resources (containers, volumes, networks, images)"
	@echo ""
	@echo "Clawker targets:"
	@echo "  clawker                 Build the clawker Clawker binary"
	@echo "  clawker-lint            Run golangci-lint on Clawker code"
	@echo "  clawker-staticcheck     Run staticcheck on Clawker code"
	@echo "  clawker-install         Install Clawker to GOPATH/bin"
	@echo "  clawker-clean           Remove Clawker build artifacts"
	@echo ""
	@echo "License targets:"
	@echo "  licenses            Generate NOTICE file from go-licenses"
	@echo "  licenses-check      Check NOTICE is up to date (CI)"
	@echo ""
	@echo "Docs targets:"
	@echo "  docs                Generate CLI reference docs"
	@echo "  docs-check          Check CLI docs are up to date (CI)"
	@echo ""
	@echo "Pre-commit targets:"
	@echo "  pre-commit-install  Install pre-commit hooks (run once after clone)"
	@echo "  pre-commit          Run all pre-commit hooks against all files"
	@echo ""
	@echo "Proto targets:"
	@echo "  proto               Regenerate Go code from .proto files (requires proto-tools)"
	@echo "  proto-tools         Install pinned buf + protoc-gen-go + protoc-gen-go-grpc"
	@echo ""
	@echo "Development targets:"
	@echo "  localenv            (Re)create .clawkerlocal/ with XDG dirs and export env vars"
	@echo "  restart             Full rebuild + nuke firewall stack containers/images for clean restart"
	@echo ""
	@echo "Release targets:"
	@echo "  release             Tag and push a release (VERSION=v2026.7.0 MESSAGE=\"...\" required)"
	@echo "  release-embeds      Cross-compile linux/amd64+arm64 embed sets (go build; bpf2go"
	@echo "                      native on Linux, Docker on macOS), staged under embeds/."
	@echo ""
	@echo "Examples:"
	@echo "  make clawker"
	@echo "  make test"
	@echo "  make release VERSION=v2026.7.0 MESSAGE=\"my release\""

# ============================================================================
# Clawker Build Targets
# ============================================================================

# Build the Clawker binary (includes embedded clawkercp control plane,
# ebpf-manager break-glass CLI, and custom CoreDNS with dnsbpf plugin).
# This is the main developer entry point for rebuilding the CLI and
# everything it go:embeds. Editing a `.proto` retriggers codegen; editing
# a `.c` retriggers bpf2go; editing host-side Go triggers only the Go
# build. Collapsed from the previous `clawker → clawker-build` indirection,
# which added a hop with no second consumer.
clawker: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Building $(BINARY_NAME) $(CLAWKER_VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/clawker

# =============================================================================
# Embedded firewall stack binaries
# =============================================================================
#
# The clawker CLI go:embed's three Linux binaries: clawkercp (CP daemon),
# ebpf-manager (break-glass, with compiled BPF bytecode baked in via bpf2go),
# and coredns-clawker (with the dnsbpf plugin baked in). At clawker-run time
# controlplane/bootstrap.go builds the clawkercp image (bundling
# clawkercp + ebpf-manager) and controlplane/firewall/stack.go
# (corednsImageTag, ensureCorednsImage) builds the clawker-coredns image.
# They are NOT sidecars — one control plane + firewall stack is shared by
# all clawker-managed containers on the host.
#
# Nothing generated is ever committed to the repo: .o files, bpf2go Go
# wrappers, and the extracted binaries are all gitignored.

EBPF_BINARY := controlplane/manager/assets/ebpf-manager
COREDNS_BINARY := controlplane/firewall/assets/coredns-clawker
CP_BINARY := controlplane/manager/assets/clawkercp
CLAWKERD_BINARY := clawkerd/embed/assets/clawkerd

# Proto inputs + generated outputs. Declared early so targets that use
# $(PROTO_GENERATED) further down in the file get a non-empty expansion
# (Make evaluates `:=` assignments and prerequisite lists at parse time).
# The regeneration rule itself lives further down, grouped with bpf-bindings
# and proto-tools. See that section for the full explanation.
PROTO_SOURCES := \
	buf.yaml \
	buf.gen.yaml \
	$(wildcard api/admin/v1/*.proto) \
	$(wildcard api/agent/v1/*.proto) \
	$(wildcard api/clawkerd/v1/*.proto)

PROTO_GENERATED := \
	api/admin/v1/admin.pb.go \
	api/admin/v1/admin_grpc.pb.go \
	api/agent/v1/agent.pb.go \
	api/agent/v1/agent_grpc.pb.go \
	api/clawkerd/v1/clawkerd.pb.go \
	api/clawkerd/v1/clawkerd_grpc.pb.go

# bpf2go-generated Go wrappers + compiled BPF bytecode extracted to the host
# tree so host-side `go test` / `go vet` / `gopls` can compile
# controlplane/firewall/ebpf/manager.go (which references clawkerObjects, clawkerRouteKey,
# etc. declared in the wrappers). Gitignored — never committed.
BPF_BINDINGS := \
	controlplane/firewall/ebpf/clawker_x86_bpfel.go \
	controlplane/firewall/ebpf/clawker_x86_bpfel.o \
	controlplane/firewall/ebpf/clawker_arm64_bpfel.go \
	controlplane/firewall/ebpf/clawker_arm64_bpfel.o

# Source inputs to the BPF bindings. An edit to these retriggers the
# bpf-bindings extraction (and transitively the binary builds that depend
# on it).
BPF_BINDING_DEPS := \
	Dockerfile.controlplane \
	go.mod \
	go.sum \
	controlplane/firewall/ebpf/bpf/clawker.c \
	controlplane/firewall/ebpf/bpf/common.h \
	controlplane/firewall/ebpf/gen.go

# Source dependencies for the ebpf-manager binary.
EBPF_BINARY_DEPS := \
	$(BPF_BINDING_DEPS) \
	controlplane/firewall/ebpf/manager.go \
	controlplane/firewall/ebpf/types.go \
	controlplane/firewall/ebpf/cmd/main.go

COREDNS_BINARY_DEPS := \
	$(BPF_BINDING_DEPS) \
	cmd/coredns-clawker/main.go \
	$(wildcard cmd/coredns-clawker/plugins/otel/*.go) \
	$(wildcard internal/dnsbpf/*.go) \
	controlplane/firewall/ebpf/types.go

# Source dependencies for the clawkercp (control plane) binary, built
# via `go build ./cmd/clawkercp`. The list mirrors every first-party
# package that build pulls from the controlplane/ and internal/controlplane
# trees (cross-check with `go list -deps ./cmd/clawkercp | grep
# schmitthub/clawker/...controlplane`) — internal/controlplane is the
# entrypoint (Main/run); controlplane/* are its subsystems. PROTO_GENERATED
# is listed explicitly so editing a `.proto` triggers the regeneration rule
# (above) before the binary rebuilds. Deeper transitive deps (internal/consts,
# logger, docker, ...) are intentionally not tracked: this is a first-party
# staleness heuristic, not a full import-graph mirror.
CP_BINARY_DEPS := \
	$(BPF_BINDING_DEPS) \
	$(PROTO_GENERATED) \
	$(wildcard cmd/clawkercp/*.go) \
	$(wildcard internal/controlplane/*.go) \
	$(wildcard controlplane/*.go) \
	$(wildcard controlplane/agent/*.go) \
	$(wildcard controlplane/auth/*.go) \
	$(wildcard controlplane/dockerevents/*.go) \
	$(wildcard controlplane/firewall/*.go) \
	$(wildcard controlplane/firewall/ebpf/*.go) \
	$(wildcard controlplane/firewall/ebpf/netlogger/*.go) \
	$(wildcard controlplane/infracerts/*.go) \
	$(wildcard controlplane/otel/*.go) \
	$(wildcard controlplane/otelcerts/*.go) \
	$(wildcard controlplane/pubsub/*.go) \
	$(wildcard controlplane/server/*.go) \
	$(wildcard controlplane/subprocess/*.go)

# `docker buildx build --output=type=local,dest=...` exports a stage's
# filesystem to a host directory. The `*-extract` stages in Dockerfile.controlplane
# are `FROM scratch` containers holding exactly the files we want exported,
# so the export lands them at the destination path with no extra layers.
BUILDX_BUILD := docker buildx build
BUILDX_TARGETARCH := $(shell $(GO) env GOARCH)

# =============================================================================
# BPF toolchain dependencies
# =============================================================================
#
# Single source of truth for the pinned apt versions that produce the BPF
# bytecode. Both CI (pinned `ubuntu-24.04` runner) and Dockerfile.controlplane
# (macOS dev convenience, ubuntu:24.04 base) install from this list —
# `sudo make bpf-deps` in CI, `COPY Makefile . && make bpf-deps` inside the
# dev container.
#
# Updating versions: bump the values below. Resolve fresh pins against the
# same ubuntu:24.04 digest used by Dockerfile.controlplane with:
#     docker run --rm ubuntu:24.04@sha256:<digest> bash -c \
#         'apt-get update >/dev/null && apt-cache policy clang llvm libbpf-dev linux-libc-dev'
#
# `llvm` provides the unversioned `/usr/bin/llvm-strip`, which bpf2go shells
# out to after compiling the .o to strip debug symbols. The `clang` meta
# package does not pull it in.
BPF_APT_DEPS := \
    clang=1:18.0-59~exp2 \
    llvm=1:18.0-59~exp2 \
    libbpf-dev=1:1.3.0-2build2 \
    linux-libc-dev=6.8.0-134.134

# Install the pinned BPF toolchain via apt. Requires Ubuntu 24.04 (Noble)
# and root — versions pinned above only resolve against Noble's apt repos.
# Callers are responsible for refreshing the apt index first (`apt-get
# update`); this target only installs. CI invokes via `sudo apt-get update
# && sudo make bpf-deps` on the pinned `ubuntu-24.04` runner; in
# Dockerfile.controlplane the build runs as root inside the matching
# ubuntu:24.04 base with its own preceding `apt-get update`. No-op on
# non-Noble hosts — call `make ebpf` instead, which routes through
# Dockerfile.controlplane on macOS.
bpf-deps:
	apt-get install -y --no-install-recommends $(BPF_APT_DEPS) ca-certificates
	rm -rf /var/lib/apt/lists/*

# bpf-bindings: extract bpf2go-generated Go wrappers + .o bytecode to
# controlplane/firewall/ebpf/. This is a prerequisite for any host-side Go tool (go build,
# go test, golangci-lint, staticcheck, gopls) touching the controlplane/firewall/ebpf
# package — manager.go references types declared in the generated wrappers.
# proto: regenerate Go code from .proto files via buf.
#
# The generated files (admin.pb.go, admin_grpc.pb.go, agent.pb.go,
# agent_grpc.pb.go) are committed to the repo — this matches the
# Kubernetes/containerd/gRPC-go convention and keeps normal `go build`
# invocations free of codegen setup. But to make proto edits painless,
# the generated files are declared as file targets whose source deps
# are the `.proto` files themselves: edit a `.proto`, the next `make`
# regenerates the matching `.pb.go` via Make's mtime check, and the
# downstream build picks up the fresh code. Same pattern as
# `BPF_BINDINGS` → bpf2go.
#
# Tool dependencies (buf, protoc-gen-go, protoc-gen-go-grpc) are pinned
# in Makefile variables below and installed on demand by the proto-tools
# target. Order-only dep on proto-tools ensures `go install` runs before
# `buf generate` if either binary is missing, without causing spurious
# regenerations just because proto-tools is phony.
BUF_VERSION := v1.47.2
PROTOC_GEN_GO_VERSION := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.6.1

# PROTO_SOURCES and PROTO_GENERATED are defined earlier (with EBPF_BINARY
# et al.) so any target above this line that references $(PROTO_GENERATED)
# expands to the full list instead of an empty string.

.PHONY: proto proto-tools
# `make proto` is a convenience alias for "regenerate all proto code right now"
# even when the `.pb.go` files are already up to date. Touches the .proto
# files first to force the generation rule to fire.
proto: proto-tools
	@touch $(filter %.proto,$(PROTO_SOURCES))
	@$(MAKE) --no-print-directory $(PROTO_GENERATED)

# File-target rule: Make regenerates PROTO_GENERATED whenever any source is
# newer (edited .proto, updated buf config). A single `buf generate` produces
# all six files. The grouped-target operator `&:` only works on GNU Make 4.3+
# (Apple's default `/usr/bin/make` is 3.81), so use the canonical-target
# pattern: one representative file gets the recipe, the rest depend on it.
# If any are deleted/stale, Make rebuilds via the chain and the recipe fires
# once. | proto-tools is order-only — runs first when missing but its phony
# nature doesn't trigger regeneration by itself.
PROTO_CANONICAL := api/admin/v1/admin.pb.go
$(PROTO_CANONICAL): $(PROTO_SOURCES) | proto-tools
	@echo "Regenerating Go code from .proto files via buf..."
	@PATH="$$(go env GOPATH)/bin:$$PATH" buf generate
$(filter-out $(PROTO_CANONICAL),$(PROTO_GENERATED)): $(PROTO_CANONICAL)

proto-tools:
	@echo "Installing pinned proto toolchain..."
	$(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

# Ubuntu 24.04 hosts (the pinned CI runner; the dev container) run bpf2go
# natively after `make bpf-deps` installs clang + libbpf-dev + linux-libc-dev.
# macOS hosts route through Dockerfile.controlplane because clang on macOS
# can't produce BPF object files — this is the only reason
# Dockerfile.controlplane exists at all.
HOST_OS := $(shell uname -s)

# `make ebpf` is the ergonomic alias for whichever bpf2go path the host
# supports. Same on-disk output (BPF_BINDINGS); differs only in how it gets
# produced.
.PHONY: bpf-bindings ebpf
bpf-bindings: $(BPF_BINDINGS)
ebpf: $(BPF_BINDINGS)

# Target gen.go specifically — recursing via `./...` also processes the
# `//go:generate moq` directive on EBPFManager in manager.go, which would
# require moq on $PATH. The mock is committed; bpf2go is the only directive
# we want to run here.
#
# Both Linux and macOS branches produce all four bindings in one invocation.
# Grouped-target `&:` requires GNU Make 4.3+ (Apple's default `/usr/bin/make`
# is 3.81), so use the canonical-target pattern: one representative file gets
# the recipe, the rest depend on it. Whichever sibling is stale forces the
# canonical to rebuild, the recipe fires once, all four land.
BPF_BINDINGS_CANONICAL := controlplane/firewall/ebpf/clawker_x86_bpfel.go
ifeq ($(HOST_OS),Linux)
$(BPF_BINDINGS_CANONICAL): $(BPF_BINDING_DEPS)
	@echo "Generating bpf2go bindings via native go generate (linux host)..."
	$(GO) generate ./controlplane/firewall/ebpf/gen.go
else
$(BPF_BINDINGS_CANONICAL): $(BPF_BINDING_DEPS)
	@echo "Extracting bpf2go bindings via Dockerfile.controlplane (non-linux host)..."
	@rm -rf controlplane/firewall/ebpf/.bpf-bindings-extract
	$(BUILDX_BUILD) \
		-f Dockerfile.controlplane \
		--target=bpf-bindings-extract \
		--output=type=local,dest=controlplane/firewall/ebpf/.bpf-bindings-extract \
		.
	@mv controlplane/firewall/ebpf/.bpf-bindings-extract/clawker_x86_bpfel.go  controlplane/firewall/ebpf/
	@mv controlplane/firewall/ebpf/.bpf-bindings-extract/clawker_x86_bpfel.o   controlplane/firewall/ebpf/
	@mv controlplane/firewall/ebpf/.bpf-bindings-extract/clawker_arm64_bpfel.go controlplane/firewall/ebpf/
	@mv controlplane/firewall/ebpf/.bpf-bindings-extract/clawker_arm64_bpfel.o  controlplane/firewall/ebpf/
	@rm -rf controlplane/firewall/ebpf/.bpf-bindings-extract
endif
$(filter-out $(BPF_BINDINGS_CANONICAL),$(BPF_BINDINGS)): $(BPF_BINDINGS_CANONICAL)

# Once $(BPF_BINDINGS) exist on the host tree, every embed binary is a plain
# CGO_ENABLED=0 Go cross-compile to linux/$(BUILDX_TARGETARCH). bpf2go's
# generated clawker_*_bpfel.go files embed the .o bytecode via `//go:embed`,
# so the binary build itself never needs clang or Docker.
ebpf-binary: $(EBPF_BINARY)
$(EBPF_BINARY): $(EBPF_BINARY_DEPS) $(BPF_BINDINGS)
	@echo "Building ebpf-manager for linux/$(BUILDX_TARGETARCH)..."
	@mkdir -p $(@D)
	@GOOS=linux GOARCH=$(BUILDX_TARGETARCH) CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $@ ./controlplane/firewall/ebpf/cmd

coredns-binary: $(COREDNS_BINARY)
$(COREDNS_BINARY): $(COREDNS_BINARY_DEPS) $(BPF_BINDINGS)
	@echo "Building coredns-clawker for linux/$(BUILDX_TARGETARCH)..."
	@mkdir -p $(@D)
	@GOOS=linux GOARCH=$(BUILDX_TARGETARCH) CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $@ ./cmd/coredns-clawker

# cp-binary builds the clawkercp containerized control plane daemon. The
# resulting binary is go:embed'd into the clawker CLI
# (controlplane/manager/embed_cp.go) and baked into the clawkercp
# image at runtime by controlplane/manager/bootstrap.go alongside
# ebpf-manager (break-glass).
#
# cp-binary depends on $(CLAWKERD_BINARY) because cmd/clawkercp transitively
# imports clawkerd/embed via internal/docker → internal/bundler. The Go
# build refuses to compile clawkerd/embed until its `//go:embed
# assets/clawkerd` target exists on disk. Make builds prereqs in declared
# order, but adding this as an explicit prerequisite of the file target
# also makes parallel `make -j` correct.
cp-binary: $(CP_BINARY)
$(CP_BINARY): $(CP_BINARY_DEPS) $(BPF_BINDINGS) $(CLAWKERD_BINARY)
	@echo "Building clawkercp for linux/$(BUILDX_TARGETARCH)..."
	@mkdir -p $(@D)
	@GOOS=linux GOARCH=$(BUILDX_TARGETARCH) CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $@ ./cmd/clawkercp

# clawkerd-binary builds the per-container agent daemon. Pure Go (no
# BPF), so the build is a plain CGO_ENABLED=0 cross-compile to
# linux/$(BUILDX_TARGETARCH) — no Docker buildx, no clang, no
# Dockerfile.controlplane stage. The artifact is go:embed'd into the
# clawker CLI via clawkerd/embed/embed.go and dropped into every
# per-project build context by internal/bundler.
#
# The build is `go build ./cmd/clawkerd`: the thin shell
# (cmd/clawkerd/clawkerd.go) imports the real entrypoint package
# internal/clawkerd (Main/run — the supervisor orchestration), which
# imports the daemon package clawkerd. All three carry first-party
# source for this binary, so all three are path-listed prerequisites
# below — editing the orchestration in internal/clawkerd must retrigger
# the rebuild, else the embedded binary goes stale against `go build`
# output. Deeper transitive deps (logger, grpc, moby) are intentionally
# not tracked: this is a first-party staleness heuristic, not a full
# import-graph mirror.
.PHONY: clawkerd-binary
clawkerd-binary: $(CLAWKERD_BINARY)
$(CLAWKERD_BINARY): $(PROTO_GENERATED) $(wildcard cmd/clawkerd/*.go) $(wildcard internal/clawkerd/*.go) $(wildcard clawkerd/*.go) $(wildcard internal/consts/*.go) $(wildcard api/agent/v1/*.go) $(wildcard api/clawkerd/v1/*.go)
	@echo "Building clawkerd for linux/$(BUILDX_TARGETARCH)..."
	@mkdir -p $(@D)
	@GOOS=linux GOARCH=$(BUILDX_TARGETARCH) CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $@ ./cmd/clawkerd

# ============================================================================
# Release pipeline support
# ============================================================================
#
# `make release-embeds` produces both linux embed sets ({amd64,arm64}) under
# embeds/ for goreleaser to consume via per-arch `hooks.pre`. Load-bearing
# invariants the rest of the file relies on:
#
#   - embeds/ lives OUTSIDE dist/ so `goreleaser release --clean` cannot
#     wipe staged binaries mid-release.
#   - All four embeds are plain CGO_ENABLED=0 Go cross-compiles. The BPF
#     bytecode is produced once by `make ebpf` (Linux: native bpf2go;
#     macOS: via Dockerfile.controlplane) and lands in the source tree as
#     clawker_*_bpfel.{go,o} where `//go:embed` pulls it into the binary.
#   - goreleaser runs with TWO build IDs (clawker-amd64, clawker-arm64),
#     not four (per-goos/arch). Splitting by arch lets a single staged embed
#     set serve both linux and darwin targets of that arch — embeds are
#     linux-only regardless of host CLI OS.
#   - `goreleaser release --parallelism 1` is REQUIRED. Both build IDs share
#     the same internal/.../assets/ paths; default parallelism would let
#     build B's pre-hook overwrite build A's staged embeds mid-compile,
#     silently producing archives whose embedded binaries don't match the
#     archive's advertised arch.

release-embeds: $(PROTO_GENERATED)
	@rm -rf $(RELEASE_EMBED_STAGE)/amd64 $(RELEASE_EMBED_STAGE)/arm64
	@echo "==> Building linux/amd64 embed set"
	@rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) $(CLAWKERD_BINARY)
	$(MAKE) BUILDX_TARGETARCH=amd64 ebpf-binary coredns-binary cp-binary clawkerd-binary
	@mkdir -p $(RELEASE_EMBED_STAGE)/amd64
	cp $(EBPF_BINARY)     $(RELEASE_EMBED_STAGE)/amd64/ebpf-manager
	cp $(COREDNS_BINARY)  $(RELEASE_EMBED_STAGE)/amd64/coredns-clawker
	cp $(CP_BINARY)       $(RELEASE_EMBED_STAGE)/amd64/clawkercp
	cp $(CLAWKERD_BINARY) $(RELEASE_EMBED_STAGE)/amd64/clawkerd
	@echo "==> Building linux/arm64 embed set"
	@rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) $(CLAWKERD_BINARY)
	$(MAKE) BUILDX_TARGETARCH=arm64 ebpf-binary coredns-binary cp-binary clawkerd-binary
	@mkdir -p $(RELEASE_EMBED_STAGE)/arm64
	cp $(EBPF_BINARY)     $(RELEASE_EMBED_STAGE)/arm64/ebpf-manager
	cp $(COREDNS_BINARY)  $(RELEASE_EMBED_STAGE)/arm64/coredns-clawker
	cp $(CP_BINARY)       $(RELEASE_EMBED_STAGE)/arm64/clawkercp
	cp $(CLAWKERD_BINARY) $(RELEASE_EMBED_STAGE)/arm64/clawkerd
	@$(MAKE) verify-release-embeds
	@echo "==> Embed sets staged under $(RELEASE_EMBED_STAGE)/ (verified)"

# verify-release-embeds asserts that each staged binary is a 64-bit
# little-endian ELF for the expected arch. Catches the silent-wrong-arch
# failure mode where Make variable propagation breaks (e.g.,
# BUILDX_TARGETARCH override stops taking effect) and both passes produce
# host-arch binaries — archives would still build cleanly but ship the
# wrong embeds. Validates four ELF header fields read from one 20-byte
# `dd` slurp per file:
#   - bytes 0-3: magic (7f 45 4c 46) — rules out non-ELF (e.g., Mach-O)
#   - byte 4:    EI_CLASS = 0x02 (ELFCLASS64)
#   - byte 5:    EI_DATA  = 0x01 (ELFDATA2LSB, little-endian)
#   - bytes 18-19: e_machine LE word — 0x003e = x86_64, 0x00b7 = AArch64
# OS/ABI (byte 7) is NOT checked: Go-built binaries set 0 (System V), not
# 3 (Linux), regardless of GOOS. Magic + class + endianness + e_machine is
# sufficient to prove "64-bit ELF for the right linux arch", which is what
# the Linux container runtime cares about.
verify-release-embeds:
	@for arch in amd64 arm64; do \
		case $$arch in amd64) want=3e00 ;; arm64) want=b700 ;; esac; \
		for bin in ebpf-manager coredns-clawker clawkercp clawkerd; do \
			f=$(RELEASE_EMBED_STAGE)/$$arch/$$bin; \
			test -f $$f || { echo "ERROR: missing $$f" >&2; exit 1; }; \
			hdr=$$(dd if=$$f bs=1 count=20 status=none 2>/dev/null | od -An -tx1 | tr -d ' \n'); \
			magic=$$(printf '%s' "$$hdr" | cut -c1-8); \
			class=$$(printf '%s' "$$hdr" | cut -c9-10); \
			data=$$(printf '%s'  "$$hdr" | cut -c11-12); \
			machine=$$(printf '%s' "$$hdr" | cut -c37-40); \
			if [ "$$magic" != "7f454c46" ]; then \
				echo "ERROR: $$f is not an ELF file (magic=0x$$magic, expected 0x7f454c46)" >&2; exit 1; \
			fi; \
			if [ "$$class" != "02" ]; then \
				echo "ERROR: $$f is not 64-bit ELF (EI_CLASS=0x$$class, expected 0x02)" >&2; exit 1; \
			fi; \
			if [ "$$data" != "01" ]; then \
				echo "ERROR: $$f is not little-endian ELF (EI_DATA=0x$$data, expected 0x01)" >&2; exit 1; \
			fi; \
			if [ "$$machine" != "$$want" ]; then \
				echo "ERROR: $$f has ELF e_machine=0x$$machine (expected 0x$$want for linux/$$arch)" >&2; exit 1; \
			fi; \
		done; \
	done

# stage-embeds-<arch> places the staged linux/<arch> embed binaries at the
# per-package go:embed source paths so the next `go build` of ./cmd/clawker
# picks them up. Called from goreleaser's per-build hooks.pre. Plain (non-`@`)
# cp so any failure (missing source, permissions) shows the offending file
# in the goreleaser log, not just a bare `cp: cannot stat`.
#
# rm -f all destination assets first so a partial failure (e.g., mid-cp
# permission denied) cannot leave a half-staged set where some assets are
# the previous arch's bytes. Either every asset is the requested arch, or
# the build fails before `go build` runs.
stage-embeds-amd64:
	rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) $(CLAWKERD_BINARY)
	cp $(RELEASE_EMBED_STAGE)/amd64/ebpf-manager     $(EBPF_BINARY)
	cp $(RELEASE_EMBED_STAGE)/amd64/coredns-clawker  $(COREDNS_BINARY)
	cp $(RELEASE_EMBED_STAGE)/amd64/clawkercp       $(CP_BINARY)
	cp $(RELEASE_EMBED_STAGE)/amd64/clawkerd         $(CLAWKERD_BINARY)

stage-embeds-arm64:
	rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) $(CLAWKERD_BINARY)
	cp $(RELEASE_EMBED_STAGE)/arm64/ebpf-manager     $(EBPF_BINARY)
	cp $(RELEASE_EMBED_STAGE)/arm64/coredns-clawker  $(COREDNS_BINARY)
	cp $(RELEASE_EMBED_STAGE)/arm64/clawkercp       $(CP_BINARY)
	cp $(RELEASE_EMBED_STAGE)/arm64/clawkerd         $(CLAWKERD_BINARY)

# Run Clawker tests with coverage
clawker-test-coverage: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running Clawker tests with coverage..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD) -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Run short tests (skip internals tests)
clawker-test-short: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running short Clawker tests..."
	$(TEST_CMD) -short ./...

# Run linter
clawker-lint: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
		echo "(tip: install with: brew install golangci-lint)"; \
	fi

# Run staticcheck
clawker-staticcheck: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running staticcheck..."
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
	else \
		echo "staticcheck not installed, skipping..."; \
		echo "(tip: install with: go install honnef.co/go/tools/cmd/staticcheck@latest)"; \
	fi

# Format code
clawker-fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

# Tidy dependencies
clawker-tidy:
	@echo "Tidying dependencies..."
	$(GO) mod tidy

# Install Clawker to GOPATH/bin
clawker-install: clawker
	@echo "Installing $(BINARY_NAME)..."
	cp $(BIN_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)

# Install Clawker to /usr/local/bin (requires sudo)
clawker-install-global: clawker
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

# Clean Clawker build artifacts
# bin/ and dist/ are cleaned by contents, never removed: they are
# tmpfs-masked inside running clawker bind containers, and deleting the
# host dir detaches the mask (kernel drops submounts on the invalidated
# dentry), leaking container writes to the host until a recreate.
clawker-clean:
	@echo "Cleaning Clawker build artifacts..."
	rm -rf $(BIN_DIR)/* $(DIST_DIR)/* $(RELEASE_EMBED_STAGE)
	rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) $(CLAWKERD_BINARY) coverage.out coverage.html
	rm -f $(BPF_BINDINGS)

# ============================================================================
# Test Targets
# ============================================================================

# Package list for unit tests (excludes integration test directories)
UNIT_PKGS = $$($(GO) list ./... | grep -v '/test/whail' | grep -v '/test/e2e')

# Unit tests only (fast, no Docker)
# Excludes test/e2e, test/whail which require Docker
# Depends on the embedded control plane binaries. controlplane/manager
# uses go:embed on assets/clawkercp + assets/ebpf-manager, and
# controlplane/firewall uses go:embed on assets/coredns-clawker —
# tests that compile those packages will fail without the binaries on disk.
test: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running unit tests..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	@PKGS="$(UNIT_PKGS)"; if [ -z "$$PKGS" ]; then echo "ERROR: no packages found" >&2; exit 1; fi; \
	$(TEST_CMD) $$PKGS

# Alias for unit tests (matches CI naming convention)
test-unit: test

# CI-mode unit tests: race detector, no caching, coverage
# Called by .github/workflows/test.yml
test-ci: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running unit tests (CI mode: race, no cache, coverage)..."
	@PKGS="$(UNIT_PKGS)"; if [ -z "$$PKGS" ]; then echo "ERROR: no packages found" >&2; exit 1; fi; \
	$(GO) test -race -count=1 -coverprofile=coverage.out $$PKGS

# E2E integration tests (requires Docker)
test-e2e: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running E2E integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 10m ./test/e2e/...

# Whail BuildKit integration tests (requires Docker + BuildKit)
test-whail: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running whail integration tests (requires Docker + BuildKit)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 5m ./test/whail/...

# Targeted suite: clawkerd daemon + Connect handshake + identity
# binding. Fast feedback loop while iterating on Branch 4 work
# (clawkerd, agent handler, identity interceptor, agentslots,
# agentregistry, auth/agent_*, container start agent-bootstrap).
# Excludes test/e2e and test/whail so this stays safe to run inside
# a clawker container (e2e tears down the host CP).
test-clawkerd: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running clawkerd-focused unit tests..."
	$(TEST_CMD) \
		./clawkerd/... \
		./internal/clawkerd/... \
		./internal/auth/... \
		./internal/cmd/container/shared/... \
		./internal/cmd/controlplane/... \
		./controlplane/agent/...

# All test suites
test-all: test test-e2e test-whail

# Unit tests with coverage
test-coverage: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Running unit tests with coverage..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD) -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Remove test Docker resources (containers, volumes, networks, images)
test-clean:
	@echo "Cleaning test resources..."
	@docker rm -f $$(docker ps -aq --filter "label=dev.clawker.test=true") 2>/dev/null || true
	@docker volume rm $$(docker volume ls -q --filter "label=dev.clawker.test=true") 2>/dev/null || true
	@docker network rm $$(docker network ls -q --filter "label=dev.clawker.test=true") 2>/dev/null || true
	@docker rmi -f $$(docker images -q --filter "label=dev.clawker.test=true") 2>/dev/null || true
	@echo "Test cleanup complete!"

# Preview the newest CHANGELOG.md entry rendered exactly as the post-upgrade
# "what's new" teaser shows it (glamour markdown). Use to eyeball a release
# section — alerts, bullets, code spans — before shipping. Depends on the
# embed binaries because internal/clawker links the full cobra tree, which
# imports the go:embed-bearing controlplane/manager, controlplane/firewall
# and clawkerd/embed packages.
changelog-preview: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@CLAWKER_PREVIEW_CHANGELOG=1 COLORTERM=truecolor $(GO) test ./internal/clawker/ -run TestPreviewLatestChangelogEntry -v 2>&1

# ============================================================================
# License Targets
# ============================================================================

# Generate NOTICE file with third-party license attributions.
# Depends on the embedded control plane binaries + bpf2go bindings because
# gen-notice.sh runs `go-licenses report ./...` which loads every package
# in the module — controlplane/manager and controlplane/firewall
# need go:embed targets, and controlplane/firewall/ebpf needs the
# bpf2go-generated Go wrappers to compile.
licenses: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Generating NOTICE file..."
	bash scripts/gen-notice.sh

# Check NOTICE file is up to date (used by CI)
licenses-check: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Checking NOTICE freshness..."
	@bash scripts/gen-notice.sh
	@if ! git diff --quiet NOTICE; then \
		echo "" >&2; \
		echo "ERROR: NOTICE is out of date. Run 'make licenses' and commit." >&2; \
		echo "" >&2; \
		git diff NOTICE; \
		exit 1; \
	fi
	@echo "NOTICE is up to date."

# ============================================================================
# Docs Targets
# ============================================================================

# Generate CLI reference + config reference docs
# Depends on the embedded control plane binaries because cmd/gen-docs links
# the full cobra tree, which imports controlplane/manager and
# controlplane/firewall (both carry go:embed assets).
docs: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Generating CLI reference + config reference docs + config JSON schemas..."
	$(GO) run ./cmd/gen-docs --doc-path docs --markdown --website --schemas

# Check all generated docs are up to date (used by CI)
docs-check: ebpf-binary coredns-binary cp-binary clawkerd-binary $(PROTO_GENERATED)
	@echo "Checking generated docs freshness..."
	@$(GO) run ./cmd/gen-docs --doc-path docs --markdown --website --schemas
	@if ! git diff --quiet docs/cli-reference/ docs/configuration.mdx docs/schemas/; then \
		echo "" >&2; \
		echo "ERROR: Generated docs are out of date. Run 'make docs' and commit." >&2; \
		echo "" >&2; \
		git diff --stat docs/cli-reference/ docs/configuration.mdx docs/schemas/; \
		exit 1; \
	fi
	@echo "Generated docs are up to date."

# ============================================================================
# Pre-commit Targets
# ============================================================================

# Install pre-commit hooks (run once after clone)
pre-commit-install:
	@bash scripts/install-hooks.sh

# Run all pre-commit hooks against all files
pre-commit:
	@pre-commit run --all-files

# Print current storage golden values for manual review.
# Interactive confirmation prevents accidental execution in CI.
# After reviewing output, hand-edit the golden values in storage_test.go.
storage-golden:
	@printf '\033[33mThis will print new golden values for TestStore_WalkUpGolden.\033[0m\n'
	@printf 'You must hand-edit storage_test.go with the printed values.\n'
	@printf 'Continue? [y/N] ' && read ans && [ "$$ans" = "y" ] || (echo "Aborted." && exit 1)
	STORAGE_GOLDEN_BLESS=1 go test ./internal/storage/... -run TestStore_WalkUpGolden -v -count=1

# ============================================================================
# Development Environment
# ============================================================================

LOCALENV_ROOT := .clawkerlocal

# Parent XDG dirs (created by make localenv — bare skeleton).
LOCALENV_XDG_CONFIG := $(LOCALENV_ROOT)/.config
LOCALENV_XDG_DATA   := $(LOCALENV_ROOT)/.local/share
LOCALENV_XDG_STATE  := $(LOCALENV_ROOT)/.local/state
LOCALENV_XDG_CACHE  := $(LOCALENV_ROOT)/.cache

# App-level dirs (created by the CLI on first use, NOT by make localenv).
LOCALENV_CONFIG := $(LOCALENV_XDG_CONFIG)/clawker
LOCALENV_DATA   := $(LOCALENV_XDG_DATA)/clawker
LOCALENV_STATE  := $(LOCALENV_XDG_STATE)/clawker
LOCALENV_CACHE  := $(LOCALENV_XDG_CACHE)/clawker

# (Re)create the local development environment directory tree.
# Creates bare XDG parent dirs only — the CLI creates app-level
# subdirs (e.g. .config/clawker/) on first use.
# Updates .env with CLAWKER_*_DIR vars (picked up by dotenv/direnv on cd).
# Also prints export commands for manual eval:
#   eval "$(make localenv)"
localenv:
	@rm -rf $(LOCALENV_ROOT)
	@mkdir -p $(LOCALENV_XDG_CONFIG) $(LOCALENV_XDG_DATA) $(LOCALENV_XDG_STATE) $(LOCALENV_XDG_CACHE)
	@bash scripts/localenv-dotenv.sh \
		"CLAWKER_CONFIG_DIR=$(CURDIR)/$(LOCALENV_CONFIG)" \
		"CLAWKER_DATA_DIR=$(CURDIR)/$(LOCALENV_DATA)" \
		"CLAWKER_STATE_DIR=$(CURDIR)/$(LOCALENV_STATE)" \
		"CLAWKER_CACHE_DIR=$(CURDIR)/$(LOCALENV_CACHE)"
	@echo "export CLAWKER_CONFIG_DIR=$(CURDIR)/$(LOCALENV_CONFIG)"
	@echo "export CLAWKER_DATA_DIR=$(CURDIR)/$(LOCALENV_DATA)"
	@echo "export CLAWKER_STATE_DIR=$(CURDIR)/$(LOCALENV_STATE)"
	@echo "export CLAWKER_CACHE_DIR=$(CURDIR)/$(LOCALENV_CACHE)"

# Full rebuild + nuke firewall stack containers/images for a clean restart.
# Usage: make restart
restart: clawker-clean clawker
	@echo "Stopping firewall stack containers..."
	@docker rm -f clawker-controlplane clawker-envoy clawker-coredns 2>/dev/null || true
	@# CP image tag is content-derived (clawker-controlplane:bin-<sha16>);
	@# sweep every tag under the repo so old bin-<sha> tags from prior
	@# builds and any legacy :latest don't linger after a restart. The
	@# explicit empty-check avoids BSD xargs (macOS) erroring on the
	@# GNU-only `-r` flag when there is no input.
	@tags="$$(docker images clawker-controlplane --format '{{.Repository}}:{{.Tag}}' 2>/dev/null)"; \
		[ -n "$$tags" ] && echo "$$tags" | xargs docker rmi -f 2>/dev/null || true
	@docker rmi clawker-coredns:latest 2>/dev/null || true
	@./bin/clawker controlplane up
	@echo "Ready. Start with: ./bin/clawker run ..."

# ============================================================================
# Release Targets
# ============================================================================

# Create and push an annotated tag to trigger the release workflow.
# Usage: make release VERSION=v2026.7.0 MESSAGE="description of release"
release:
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=v2026.7.0 MESSAGE=\"...\""; exit 1; fi
	@if [ -z "$(MESSAGE)" ]; then echo "MESSAGE is required"; exit 1; fi
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9._-]+)?$$'; then echo "Invalid semver: $(VERSION)"; exit 1; fi
	@if [ -n "$$(git status --porcelain)" ]; then echo "Working tree dirty — commit or stash first"; exit 1; fi
	@if [ "$$(git branch --show-current)" != "main" ]; then echo "Not on main branch"; exit 1; fi
	git tag -a $(VERSION) -m "$(MESSAGE)"
	git push origin $(VERSION)
	@echo ""
	@echo "Tagged and pushed $(VERSION) — watch: gh run watch"
