.PHONY: help \
        clawker clawker-build clawker-generate clawker-test clawker-test-internals clawker-lint clawker-staticcheck clawker-install clawker-clean \
        test test-unit test-ci test-commands test-whail test-internals test-agents test-acceptance test-all test-coverage test-clean \
        licenses licenses-check \
        docs docs-check \
        bpf-builder-image bpf-regenerate bpf-verify \
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
	@echo "Development targets:"
	@echo "  localenv            (Re)create .clawkerlocal/ with XDG dirs and export env vars"
	@echo "  restart             Full rebuild + nuke firewall containers/image for clean restart"
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

# Build the Clawker binary (includes embedded eBPF manager + custom CoreDNS)
clawker: clawker-build

clawker-build: ebpf-binary coredns-binary
	@echo "Building $(BINARY_NAME) $(CLAWKER_VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/clawker

# Cross-compile the eBPF manager binary for Linux (embedded in clawker via go:embed).
# Architecture matches the host — Docker Desktop runs the matching Linux arch.
EBPF_BINARY := internal/firewall/assets/ebpf-manager
ebpf-binary: $(EBPF_BINARY)
$(EBPF_BINARY): internal/ebpf/cmd/main.go internal/ebpf/manager.go internal/ebpf/types.go
	@echo "Building ebpf-manager for linux/$(shell $(GO) env GOARCH)..."
	@mkdir -p internal/firewall/assets
	GOOS=linux CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/ebpf/cmd

# Cross-compile the custom CoreDNS binary for Linux (embedded in clawker via go:embed).
# Includes the dnsbpf plugin for real-time BPF dns_cache population.
COREDNS_BINARY := internal/firewall/assets/coredns-clawker
coredns-binary: $(COREDNS_BINARY)
$(COREDNS_BINARY): cmd/coredns-clawker/main.go $(wildcard internal/dnsbpf/*.go) internal/ebpf/types.go
	@echo "Building coredns-clawker for linux/$(shell $(GO) env GOARCH)..."
	@mkdir -p internal/firewall/assets
	GOOS=linux CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker

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
	@echo "  ebpf-manager linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/ebpf/cmd
	@echo "  coredns-clawker linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/clawker
	@echo "  ebpf-manager linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/ebpf/cmd
	@echo "  coredns-clawker linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/clawker

clawker-build-darwin:
	@echo "Building Clawker for macOS..."
	@mkdir -p $(DIST_DIR)
	@echo "  ebpf-manager linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/ebpf/cmd
	@echo "  coredns-clawker linux/amd64"; GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/clawker
	@echo "  ebpf-manager linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(EBPF_BINARY) ./internal/ebpf/cmd
	@echo "  coredns-clawker linux/arm64"; GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags="-s -w" -trimpath -o $(COREDNS_BINARY) ./cmd/coredns-clawker
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/clawker

# Run Clawker tests
clawker-test: ebpf-binary coredns-binary
	@echo "Running Clawker tests..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) ./...

# Run Clawker internals tests
clawker-test-internals: ebpf-binary coredns-binary
	@echo "Running Clawker internal integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 10m ./test/internals/...

# Run Clawker tests with coverage
clawker-test-coverage: ebpf-binary coredns-binary
	@echo "Running Clawker tests with coverage..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD) -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Run short tests (skip internals tests)
clawker-test-short: ebpf-binary coredns-binary
	@echo "Running short Clawker tests..."
	$(TEST_CMD) -short ./...

# Run linter
clawker-lint: ebpf-binary coredns-binary
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
		echo "(tip: install with: brew install golangci-lint)"; \
	fi

# Run staticcheck
clawker-staticcheck: ebpf-binary coredns-binary
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
clawker-install: clawker-build
	@echo "Installing $(BINARY_NAME)..."
	cp $(BIN_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)

# Install Clawker to /usr/local/bin (requires sudo)
clawker-install-global: clawker-build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

# Clean Clawker build artifacts
clawker-clean:
	@echo "Cleaning Clawker build artifacts..."
	rm -rf $(BIN_DIR) $(DIST_DIR)
	rm -f $(EBPF_BINARY) $(COREDNS_BINARY) coverage.out coverage.html

# ============================================================================
# Test Targets
# ============================================================================

# Package list for unit tests (excludes integration test directories)
UNIT_PKGS = $$($(GO) list ./... | grep -v '/test/whail' | grep -v '/test/e2e')

# Unit tests only (fast, no Docker)
# Excludes test/e2e, test/whail which require Docker
# Depends on the embedded firewall binaries because internal/firewall uses
# go:embed on assets/ebpf-manager and assets/coredns-clawker — tests that
# compile the firewall package will fail without them.
test: ebpf-binary coredns-binary
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
test-ci: ebpf-binary coredns-binary
	@echo "Running unit tests (CI mode: race, no cache, coverage)..."
	@PKGS="$(UNIT_PKGS)"; if [ -z "$$PKGS" ]; then echo "ERROR: no packages found" >&2; exit 1; fi; \
	$(GO) test -race -count=1 -coverprofile=coverage.out $$PKGS

# E2E integration tests (requires Docker)
test-e2e: ebpf-binary coredns-binary
	@echo "Running E2E integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 10m ./test/e2e/...

# Whail BuildKit integration tests (requires Docker + BuildKit)
test-whail: ebpf-binary coredns-binary
	@echo "Running whail integration tests (requires Docker + BuildKit)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 5m ./test/whail/...

# All test suites
test-all: test test-e2e test-whail

# Unit tests with coverage
test-coverage: ebpf-binary coredns-binary
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
# BPF Reproducible Bytecode Targets
# ============================================================================
#
# `make bpf-regenerate` and `make bpf-verify` run BPF bytecode generation
# inside a fully pinned Docker build environment (Dockerfile.bpf-builder) so
# the output is byte-reproducible from pinned inputs: base image digest,
# apt package versions, Go toolchain digest, and bpf2go version.
#
# The committed internal/ebpf/clawker_*_bpfel.{go,o} files are authoritative
# for `go build`. `bpf-verify` is the reproducibility gate — it regenerates
# in the pinned image and diffs against the committed bytecode. CI runs it
# on every PR so committed .o files are never trust-on-first-use.
#
# See internal/ebpf/REPRODUCIBILITY.md for the provenance chain and the
# procedure for updating pinned inputs.

BPF_BUILDER_IMAGE := clawker-bpf-builder:latest
BPF_GENERATED := internal/ebpf/clawker_x86_bpfel.go internal/ebpf/clawker_x86_bpfel.o \
                 internal/ebpf/clawker_arm64_bpfel.go internal/ebpf/clawker_arm64_bpfel.o

# Build the pinned BPF builder image. Re-runs whenever Dockerfile.bpf-builder
# changes. The image tag is local-only; we re-derive it from pinned sources
# each time rather than pushing to a registry.
bpf-builder-image:
	@echo "Building pinned BPF builder image..."
	@if grep -q 'PIN_ME_BEFORE_MERGE' Dockerfile.bpf-builder; then \
		echo "" >&2; \
		echo "ERROR: Dockerfile.bpf-builder contains a PIN_ME_BEFORE_MERGE placeholder." >&2; \
		echo "       Follow the pin-refresh recipe in internal/ebpf/REPRODUCIBILITY.md" >&2; \
		echo "       to fill in the base image digest before running bpf-regenerate." >&2; \
		echo "" >&2; \
		exit 1; \
	fi
	docker build -f Dockerfile.bpf-builder -t $(BPF_BUILDER_IMAGE) .

# Regenerate the BPF Go bindings + .o bytecode from clawker.c / common.h
# inside the pinned builder image. Writes directly to the host tree so the
# next `go build` picks up the fresh output.
bpf-regenerate: bpf-builder-image
	@echo "Regenerating BPF bytecode in pinned builder image..."
	docker run --rm \
		-v $(CURDIR):/src \
		-u $(shell id -u):$(shell id -g) \
		$(BPF_BUILDER_IMAGE)
	@echo "Done. Committed bytecode under internal/ebpf/ has been rewritten."
	@echo "If this differed from HEAD, commit the change."

# Reproducibility gate: regenerate in the pinned image and fail if the output
# differs from the committed bytecode. This is what CI runs on every PR to
# guarantee the committed .o files match the committed .c sources under the
# pinned recipe.
bpf-verify: bpf-builder-image
	@echo "Verifying BPF bytecode reproducibility..."
	@for f in $(BPF_GENERATED); do \
		if [ ! -f "$$f" ]; then \
			echo "ERROR: committed BPF artifact missing: $$f" >&2; \
			exit 1; \
		fi; \
	done
	@tmpdir=$$(mktemp -d); \
	trap "rm -rf $$tmpdir" EXIT; \
	for f in $(BPF_GENERATED); do \
		cp "$$f" "$$tmpdir/$$(basename $$f).orig"; \
	done; \
	docker run --rm \
		-v $(CURDIR):/src \
		-u $(shell id -u):$(shell id -g) \
		$(BPF_BUILDER_IMAGE); \
	fail=0; \
	for f in $(BPF_GENERATED); do \
		if ! cmp -s "$$f" "$$tmpdir/$$(basename $$f).orig"; then \
			echo "" >&2; \
			echo "ERROR: $$f drifted from committed bytecode." >&2; \
			echo "       Run 'make bpf-regenerate' and commit the result." >&2; \
			fail=1; \
		fi; \
	done; \
	if [ $$fail -ne 0 ]; then \
		echo "" >&2; \
		echo "BPF bytecode reproducibility check FAILED." >&2; \
		exit 1; \
	fi
	@echo "BPF bytecode is reproducible from committed sources + pinned recipe."

# ============================================================================
# License Targets
# ============================================================================

# Generate NOTICE file with third-party license attributions
licenses:
	@echo "Generating NOTICE file..."
	bash scripts/gen-notice.sh

# Check NOTICE file is up to date (used by CI)
licenses-check:
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
# Depends on the embedded firewall binaries because cmd/gen-docs links
# the full cobra tree, which imports internal/firewall (go:embed assets).
docs: ebpf-binary coredns-binary
	@echo "Generating CLI reference + config reference docs..."
	$(GO) run ./cmd/gen-docs --doc-path docs --markdown --website

# Check all generated docs are up to date (used by CI)
docs-check: ebpf-binary coredns-binary
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

# Full rebuild + nuke firewall containers/image for a clean restart.
# Usage: make restart
restart: clawker-clean clawker
	@echo "Stopping firewall containers..."
	@docker rm -f clawker-ebpf clawker-envoy clawker-coredns 2>/dev/null || true
	@docker rmi clawker-ebpf:latest clawker-coredns:latest 2>/dev/null || true
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
