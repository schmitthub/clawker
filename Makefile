.PHONY: help \
        clawker clawker-generate clawker-test clawker-test-internals clawker-lint clawker-staticcheck clawker-install clawker-clean \
        ebpf-binary coredns-binary cp-binary \
        test test-unit test-ci test-commands test-whail test-internals test-agents test-acceptance test-all test-coverage test-clean test-e2e \
        licenses licenses-check \
        docs docs-check \
        pre-commit pre-commit-install \
        localenv \
        restart \
        release

# Go Clawker variables
BINARY_NAME := clawker
CLAWKER_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GO ?= go
GOFLAGS := -trimpath
LDFLAGS := -s -w \
	-X 'github.com/schmitthub/clawker/internal/build.Version=$(CLAWKER_VERSION)' \
	-X 'github.com/schmitthub/clawker/internal/build.Date=$(shell date +%Y-%m-%d)'
BIN_DIR := bin
DIST_DIR := dist

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
	@echo "  clawker-generate        Build the standalone clawker-generate binary"
	@echo "  clawker-test            Run Clawker tests (alias for 'test')"
	@echo "  clawker-test-internals  Run Clawker internal integration tests"
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
	@echo "  release             Tag and push a release (VERSION=v0.7.6 MESSAGE=\"...\" required)"
	@echo ""
	@echo "Examples:"
	@echo "  make clawker"
	@echo "  make clawker-test"
	@echo "  make release VERSION=v0.7.6 MESSAGE=\"my release\""

# ============================================================================
# Clawker Build Targets
# ============================================================================

# Build the Clawker binary (includes embedded clawker-cp control plane,
# ebpf-manager break-glass CLI, and custom CoreDNS with dnsbpf plugin).
# This is the main developer entry point for rebuilding the CLI and
# everything it go:embeds. Editing a `.proto` retriggers codegen; editing
# a `.c` retriggers bpf2go; editing host-side Go triggers only the Go
# build. Collapsed from the previous `clawker → clawker-build` indirection,
# which added a hop with no second consumer.
clawker: ebpf-binary coredns-binary cp-binary $(PROTO_GENERATED)
	@echo "Building $(BINARY_NAME) $(CLAWKER_VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/clawker

# =============================================================================
# Embedded firewall stack binaries (reproducible Docker builds)
# =============================================================================
#
# The clawker CLI go:embed's three Linux binaries: clawker-cp (CP daemon),
# ebpf-manager (break-glass, with compiled BPF bytecode baked in via bpf2go),
# and coredns-clawker (with the dnsbpf plugin baked in). At clawker-run time
# internal/controlplane/bootstrap.go builds the clawker-cp image (bundling
# clawker-cp + ebpf-manager) and internal/controlplane/firewall/stack.go
# (corednsImageTag, ensureCorednsImage) builds the clawker-coredns image.
# They are NOT sidecars — one control plane + firewall stack is shared by
# all clawker-managed containers on the host.
#
# Both targets build through `docker buildx build` against a single pinned
# multi-stage Dockerfile.controlplane whose `bpf-builder` stage is shared between
# the ebpf-manager and coredns-clawker compile paths (coredns-clawker imports
# internal/controlplane/firewall/ebpf and so needs the bpf2go-generated wrappers at compile time).
# Every input is pinned — base image digest, apt package versions, Go
# toolchain digest, bpf2go version — so a fresh `make` on any host produces
# byte-identical output. Nothing generated is ever committed to the repo:
# .o files, bpf2go Go wrappers, and the extracted binaries are all gitignored.
#
# See internal/controlplane/firewall/ebpf/REPRODUCIBILITY.md for the full provenance chain and the
# pin update procedure.

EBPF_BINARY := internal/controlplane/cpboot/assets/ebpf-manager
COREDNS_BINARY := internal/controlplane/firewall/assets/coredns-clawker
CP_BINARY := internal/controlplane/cpboot/assets/clawker-cp

# Proto inputs + generated outputs. Declared early so targets that use
# $(PROTO_GENERATED) further down in the file get a non-empty expansion
# (Make evaluates `:=` assignments and prerequisite lists at parse time).
# The regeneration rule itself lives further down, grouped with bpf-bindings
# and proto-tools. See that section for the full explanation.
PROTO_SOURCES := \
	buf.yaml \
	buf.gen.yaml \
	$(wildcard internal/clawkerd/protocol/v1/*.proto)

PROTO_GENERATED := \
	internal/clawkerd/protocol/v1/agent.pb.go \
	internal/clawkerd/protocol/v1/agent_grpc.pb.go \
	internal/clawkerd/protocol/v1/controlplane.pb.go \
	internal/clawkerd/protocol/v1/controlplane_grpc.pb.go

# bpf2go-generated Go wrappers + compiled BPF bytecode extracted to the host
# tree so host-side `go test` / `go vet` / `gopls` can compile
# internal/controlplane/firewall/ebpf/manager.go (which references clawkerObjects, clawkerRouteKey,
# etc. declared in the wrappers). Gitignored — never committed.
BPF_BINDINGS := \
	internal/controlplane/firewall/ebpf/clawker_x86_bpfel.go \
	internal/controlplane/firewall/ebpf/clawker_x86_bpfel.o \
	internal/controlplane/firewall/ebpf/clawker_arm64_bpfel.go \
	internal/controlplane/firewall/ebpf/clawker_arm64_bpfel.o

# Source inputs to the BPF bindings. An edit to these retriggers the
# bpf-bindings extraction (and transitively the binary builds that depend
# on it).
BPF_BINDING_DEPS := \
	Dockerfile.controlplane \
	go.mod \
	go.sum \
	internal/controlplane/firewall/ebpf/bpf/clawker.c \
	internal/controlplane/firewall/ebpf/bpf/common.h \
	internal/controlplane/firewall/ebpf/gen.go

# Source dependencies for the ebpf-manager binary.
EBPF_BINARY_DEPS := \
	$(BPF_BINDING_DEPS) \
	internal/controlplane/firewall/ebpf/manager.go \
	internal/controlplane/firewall/ebpf/types.go \
	internal/controlplane/firewall/ebpf/cmd/main.go

COREDNS_BINARY_DEPS := \
	$(BPF_BINDING_DEPS) \
	cmd/coredns-clawker/main.go \
	$(wildcard internal/dnsbpf/*.go) \
	internal/controlplane/firewall/ebpf/types.go

# Source dependencies for the clawker-cp (control plane) binary. It
# imports both internal/controlplane and internal/controlplane/firewall/ebpf, plus
# the generated proto types in internal/clawkerd/protocol. PROTO_GENERATED
# is listed explicitly so that editing a `.proto` triggers the regeneration
# rule (above) before the binary is rebuilt.
CP_BINARY_DEPS := \
	$(BPF_BINDING_DEPS) \
	$(PROTO_GENERATED) \
	$(wildcard cmd/clawker-cp/*.go) \
	$(wildcard internal/controlplane/*.go) \
	internal/controlplane/firewall/ebpf/manager.go \
	internal/controlplane/firewall/ebpf/types.go

# `docker buildx build --output=type=local,dest=...` exports a stage's
# filesystem to a host directory. The `*-extract` stages in Dockerfile.controlplane
# are `FROM scratch` containers holding exactly the files we want exported,
# so the export lands them at the destination path with no extra layers.
BUILDX_BUILD := docker buildx build
BUILDX_TARGETARCH := $(shell $(GO) env GOARCH)

# bpf-bindings: extract bpf2go-generated Go wrappers + .o bytecode to
# internal/controlplane/firewall/ebpf/. This is a prerequisite for any host-side Go tool (go build,
# go test, golangci-lint, staticcheck, gopls) touching the internal/controlplane/firewall/ebpf
# package — manager.go references types declared in the generated wrappers.
# proto: regenerate Go code from .proto files via buf.
#
# The generated files (agent.pb.go, agent_grpc.pb.go, controlplane.pb.go,
# controlplane_grpc.pb.go) are committed to the repo — this matches the
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
PROTOC_GEN_GO_VERSION := v1.36.5
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1

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
# newer (edited .proto, updated buf config). Group target (&:) means a single
# `buf generate` invocation produces all four files. | proto-tools is an
# order-only prerequisite: it runs before regeneration (installing tools if
# needed) but its phony nature doesn't trigger regeneration by itself.
$(PROTO_GENERATED) &: $(PROTO_SOURCES) | proto-tools
	@echo "Regenerating Go code from .proto files via buf..."
	@PATH="$$(go env GOPATH)/bin:$$PATH" buf generate

proto-tools:
	@echo "Installing pinned proto toolchain..."
	$(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

.PHONY: bpf-bindings
bpf-bindings: $(BPF_BINDINGS)
$(BPF_BINDINGS) &: $(BPF_BINDING_DEPS)
	@echo "Extracting bpf2go bindings to internal/controlplane/firewall/ebpf/ via pinned Dockerfile.controlplane..."
	@rm -rf internal/controlplane/firewall/ebpf/.bpf-bindings-extract
	$(BUILDX_BUILD) \
		-f Dockerfile.controlplane \
		--target=bpf-bindings-extract \
		--output=type=local,dest=internal/controlplane/firewall/ebpf/.bpf-bindings-extract \
		.
	@mv internal/controlplane/firewall/ebpf/.bpf-bindings-extract/clawker_x86_bpfel.go  internal/controlplane/firewall/ebpf/
	@mv internal/controlplane/firewall/ebpf/.bpf-bindings-extract/clawker_x86_bpfel.o   internal/controlplane/firewall/ebpf/
	@mv internal/controlplane/firewall/ebpf/.bpf-bindings-extract/clawker_arm64_bpfel.go internal/controlplane/firewall/ebpf/
	@mv internal/controlplane/firewall/ebpf/.bpf-bindings-extract/clawker_arm64_bpfel.o  internal/controlplane/firewall/ebpf/
	@rm -rf internal/controlplane/firewall/ebpf/.bpf-bindings-extract

ebpf-binary: $(EBPF_BINARY)
$(EBPF_BINARY): $(EBPF_BINARY_DEPS) $(BPF_BINDINGS)
	@echo "Building ebpf-manager for linux/$(BUILDX_TARGETARCH) via pinned Dockerfile.controlplane..."
	@mkdir -p $(@D)
	@rm -rf $(@D)/.ebpf-extract
	$(BUILDX_BUILD) \
		-f Dockerfile.controlplane \
		--target=ebpf-manager-extract \
		--build-arg TARGETOS=linux \
		--build-arg TARGETARCH=$(BUILDX_TARGETARCH) \
		--output=type=local,dest=$(@D)/.ebpf-extract \
		.
	@mv $(@D)/.ebpf-extract/ebpf-manager $@
	@rm -rf $(@D)/.ebpf-extract

coredns-binary: $(COREDNS_BINARY)
$(COREDNS_BINARY): $(COREDNS_BINARY_DEPS) $(BPF_BINDINGS)
	@echo "Building coredns-clawker for linux/$(BUILDX_TARGETARCH) via pinned Dockerfile.controlplane..."
	@mkdir -p $(@D)
	@rm -rf $(@D)/.coredns-extract
	$(BUILDX_BUILD) \
		-f Dockerfile.controlplane \
		--target=coredns-extract \
		--build-arg TARGETOS=linux \
		--build-arg TARGETARCH=$(BUILDX_TARGETARCH) \
		--output=type=local,dest=$(@D)/.coredns-extract \
		.
	@mv $(@D)/.coredns-extract/coredns-clawker $@
	@rm -rf $(@D)/.coredns-extract

# cp-binary builds the clawker-cp containerized control plane daemon via
# the same pinned multi-stage Dockerfile.controlplane. The resulting binary is
# go:embed'd into the clawker CLI (internal/controlplane/cpboot/embed_cp.go)
# and baked into the clawker-cp image at runtime by
# internal/controlplane/cpboot/bootstrap.go (cpImageDockerfile) alongside
# ebpf-manager (break-glass).
cp-binary: $(CP_BINARY)
$(CP_BINARY): $(CP_BINARY_DEPS) $(BPF_BINDINGS)
	@echo "Building clawker-cp for linux/$(BUILDX_TARGETARCH) via pinned Dockerfile.controlplane..."
	@mkdir -p $(@D)
	@rm -rf $(@D)/.cp-extract
	$(BUILDX_BUILD) \
		-f Dockerfile.controlplane \
		--target=clawker-cp-extract \
		--build-arg TARGETOS=linux \
		--build-arg TARGETARCH=$(BUILDX_TARGETARCH) \
		--output=type=local,dest=$(@D)/.cp-extract \
		.
	@mv $(@D)/.cp-extract/clawker-cp $@
	@rm -rf $(@D)/.cp-extract

# Build the standalone generate binary
clawker-generate:
	@echo "Building clawker-generate $(CLAWKER_VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/clawker-generate ./cmd/clawker-generate

# Build Clawker for all supported platforms (linux, darwin — windows is not
# currently supported).
#
# clawker-build-linux and clawker-build-darwin each overwrite the shared
# $(EBPF_BINARY)/$(COREDNS_BINARY) asset paths during their recipe (first
# amd64, then arm64). Under `make -j clawker-build-all` the platform targets
# would run concurrently and stomp each other's asset files, silently
# embedding the wrong-arch binaries into the cross-compiled clawker output.
# Invoke each platform as a sub-make so -j does not parallelize them.
clawker-build-all:
	@echo "Building Clawker for all platforms..."
	$(MAKE) clawker-build-linux
	$(MAKE) clawker-build-darwin

clawker-build-linux:
	@echo "Building Clawker for Linux..."
	@mkdir -p $(DIST_DIR)
	@echo "  ebpf-manager linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/controlplane/firewall/ebpf/cmd
	@echo "  coredns-clawker linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker
	@echo "  clawker-cp linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(CP_BINARY) ./cmd/clawker-cp
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/clawker
	@echo "  ebpf-manager linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/controlplane/firewall/ebpf/cmd
	@echo "  coredns-clawker linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker
	@echo "  clawker-cp linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(CP_BINARY) ./cmd/clawker-cp
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/clawker

clawker-build-darwin:
	@echo "Building Clawker for macOS..."
	@mkdir -p $(DIST_DIR)
	@echo "  ebpf-manager linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/controlplane/firewall/ebpf/cmd
	@echo "  coredns-clawker linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker
	@echo "  clawker-cp linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(CP_BINARY) ./cmd/clawker-cp
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/clawker
	@echo "  ebpf-manager linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/controlplane/firewall/ebpf/cmd
	@echo "  coredns-clawker linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker
	@echo "  clawker-cp linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(CP_BINARY) ./cmd/clawker-cp
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/clawker

# Run Clawker tests
clawker-test: ebpf-binary coredns-binary cp-binary
	@echo "Running Clawker tests..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) ./...

# Run Clawker internals tests
clawker-test-internals: ebpf-binary coredns-binary cp-binary
	@echo "Running Clawker internal integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 10m ./test/internals/...

# Run Clawker tests with coverage
clawker-test-coverage: ebpf-binary coredns-binary cp-binary
	@echo "Running Clawker tests with coverage..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD) -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Run short tests (skip internals tests)
clawker-test-short: ebpf-binary coredns-binary cp-binary
	@echo "Running short Clawker tests..."
	$(TEST_CMD) -short ./...

# Run linter
clawker-lint: ebpf-binary coredns-binary cp-binary
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
		echo "(tip: install with: brew install golangci-lint)"; \
	fi

# Run staticcheck
clawker-staticcheck: ebpf-binary coredns-binary cp-binary
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
clawker-clean:
	@echo "Cleaning Clawker build artifacts..."
	rm -rf $(BIN_DIR) $(DIST_DIR)
	rm -f $(EBPF_BINARY) $(COREDNS_BINARY) $(CP_BINARY) coverage.out coverage.html

# ============================================================================
# Test Targets
# ============================================================================

# Package list for unit tests (excludes integration test directories)
UNIT_PKGS = $$($(GO) list ./... | grep -v '/test/whail' | grep -v '/test/e2e')

# Unit tests only (fast, no Docker)
# Excludes test/e2e, test/whail which require Docker
# Depends on the embedded control plane binaries. internal/controlplane/cpboot
# uses go:embed on assets/clawker-cp + assets/ebpf-manager, and
# internal/controlplane/firewall uses go:embed on assets/coredns-clawker —
# tests that compile those packages will fail without the binaries on disk.
test: ebpf-binary coredns-binary cp-binary
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
test-ci: ebpf-binary coredns-binary cp-binary
	@echo "Running unit tests (CI mode: race, no cache, coverage)..."
	@PKGS="$(UNIT_PKGS)"; if [ -z "$$PKGS" ]; then echo "ERROR: no packages found" >&2; exit 1; fi; \
	$(GO) test -race -count=1 -coverprofile=coverage.out $$PKGS

# E2E integration tests (requires Docker)
test-e2e: ebpf-binary coredns-binary cp-binary
	@echo "Running E2E integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 10m ./test/e2e/...

# Whail BuildKit integration tests (requires Docker + BuildKit)
test-whail: ebpf-binary coredns-binary cp-binary
	@echo "Running whail integration tests (requires Docker + BuildKit)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 5m ./test/whail/...

# All test suites
test-all: test test-e2e test-whail

# Unit tests with coverage
test-coverage: ebpf-binary coredns-binary cp-binary
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

# ============================================================================
# License Targets
# ============================================================================

# Generate NOTICE file with third-party license attributions.
# Depends on the embedded control plane binaries + bpf2go bindings because
# gen-notice.sh runs `go-licenses report ./...` which loads every package
# in the module — internal/controlplane/cpboot and internal/controlplane/firewall
# need go:embed targets, and internal/controlplane/firewall/ebpf needs the
# bpf2go-generated Go wrappers to compile.
licenses: ebpf-binary coredns-binary cp-binary
	@echo "Generating NOTICE file..."
	bash scripts/gen-notice.sh

# Check NOTICE file is up to date (used by CI)
licenses-check: ebpf-binary coredns-binary cp-binary
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
# the full cobra tree, which imports internal/controlplane/cpboot and
# internal/controlplane/firewall (both carry go:embed assets).
docs: ebpf-binary coredns-binary cp-binary
	@echo "Generating CLI reference + config reference docs..."
	$(GO) run ./cmd/gen-docs --doc-path docs --markdown --website

# Check all generated docs are up to date (used by CI)
docs-check: ebpf-binary coredns-binary cp-binary
	@echo "Checking generated docs freshness..."
	@$(GO) run ./cmd/gen-docs --doc-path docs --markdown --website
	@if ! git diff --quiet docs/cli-reference/ docs/configuration.mdx; then \
		echo "" >&2; \
		echo "ERROR: Generated docs are out of date. Run 'make docs' and commit." >&2; \
		echo "" >&2; \
		git diff --stat docs/cli-reference/ docs/configuration.mdx; \
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
	@docker rmi clawker-controlplane:latest clawker-coredns:latest 2>/dev/null || true
	@echo "Ready. Start with: ./bin/clawker run ..."

# ============================================================================
# Release Targets
# ============================================================================

# Create and push an annotated tag to trigger the release workflow.
# Usage: make release VERSION=v0.7.6 MESSAGE="description of release"
release:
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=v0.7.6 MESSAGE=\"...\""; exit 1; fi
	@if [ -z "$(MESSAGE)" ]; then echo "MESSAGE is required"; exit 1; fi
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9._-]+)?$$'; then echo "Invalid semver: $(VERSION)"; exit 1; fi
	@if [ -n "$$(git status --porcelain)" ]; then echo "Working tree dirty — commit or stash first"; exit 1; fi
	@if [ "$$(git branch --show-current)" != "main" ]; then echo "Not on main branch"; exit 1; fi
	git tag -a $(VERSION) -m "$(MESSAGE)"
	git push origin $(VERSION)
	@echo ""
	@echo "Tagged and pushed $(VERSION) — watch: gh run watch"
