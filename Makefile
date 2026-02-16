.PHONY: help \
        clawker clawker-build clawker-generate clawker-test clawker-test-internals clawker-lint clawker-staticcheck clawker-install clawker-clean \
        fawker \
        test test-unit test-ci test-commands test-whail test-internals test-agents test-acceptance test-controlplane test-all test-coverage test-clean golden-update \
        proto \
        licenses licenses-check \
        docs docs-check \
        pre-commit pre-commit-install

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
	@echo "  golden-update       Regenerate golden files"
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
	@echo "Examples:"
	@echo "  make clawker"
	@echo "  make clawker-test"

# ============================================================================
# Clawker Build Targets
# ============================================================================

# Build the Clawker binary
clawker: clawker-build

clawker-build:
	@echo "Building $(BINARY_NAME) $(CLAWKER_VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/clawker

# Build the fawker demo Clawker (faked deps, no Docker required)
fawker:
	@echo "Building fawker..."
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/fawker ./cmd/fawker

# Build the standalone generate binary
clawker-generate:
	@echo "Building clawker-generate $(CLAWKER_VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/clawker-generate ./cmd/clawker-generate

# Build Clawker for multiple platforms
clawker-build-all: clawker-build-linux clawker-build-darwin clawker-build-windows

clawker-build-linux:
	@echo "Building Clawker for Linux..."
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/clawker
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/clawker

clawker-build-darwin:
	@echo "Building Clawker for macOS..."
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/clawker
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/clawker

clawker-build-windows:
	@echo "Building Clawker for Windows..."
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/clawker

# Run Clawker tests
clawker-test:
	@echo "Running Clawker tests..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) ./...

# Run Clawker internals tests
clawker-test-internals:
	@echo "Running Clawker internal integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 10m ./test/internals/...

# Run Clawker tests with coverage
clawker-test-coverage:
	@echo "Running Clawker tests with coverage..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD) -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Run short tests (skip internals tests)
clawker-test-short:
	@echo "Running short Clawker tests..."
	$(TEST_CMD) -short ./...

# Run linter
clawker-lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
		echo "(tip: install with: brew install golangci-lint)"; \
	fi

# Run staticcheck
clawker-staticcheck:
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
	rm -f coverage.out coverage.html

# ============================================================================
# Test Targets
# ============================================================================

# Package list for unit tests (excludes integration test directories)
UNIT_PKGS = $$($(GO) list ./... | grep -v '/test/cli' | grep -v '/test/commands' | grep -v '/test/whail' | grep -v '/test/internals' | grep -v '/test/agents' | grep -v '/test/controlplane')

# Unit tests only (fast, no Docker)
# Excludes test/cli, test/internals, test/agents which require Docker
test:
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
test-ci:
	@echo "Running unit tests (CI mode: race, no cache, coverage)..."
	@PKGS="$(UNIT_PKGS)"; if [ -z "$$PKGS" ]; then echo "ERROR: no packages found" >&2; exit 1; fi; \
	$(GO) test -race -count=1 -coverprofile=coverage.out $$PKGS

# Internal integration tests (requires Docker)
test-internals:
	@echo "Running internal integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 10m ./test/internals/...

# Clawker workflow tests via testscript (requires Docker)
test-acceptance:
	@echo "Running Clawker acceptance tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 15m ./test/cli/...

# Command integration tests (requires Docker)
test-commands:
	@echo "Running command integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 10m ./test/commands/...

# Whail BuildKit integration tests (requires Docker + BuildKit)
test-whail:
	@echo "Running whail integration tests (requires Docker + BuildKit)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 5m ./test/whail/...

# Agent E2E tests (requires Docker)
test-agents:
	@echo "Running agent E2E tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 15m ./test/agents/...

# Control plane integration tests (requires Docker)
test-controlplane:
	@echo "Running control plane integration tests (requires Docker)..."
ifndef GOTESTSUM
	@echo "(tip: install gotestsum for prettier output: go install gotest.tools/gotestsum@latest)"
endif
	$(TEST_CMD_VERBOSE) -timeout 5m ./test/controlplane/...

# All test suites
test-all: test test-commands test-whail test-internals test-acceptance test-agents test-controlplane

# Unit tests with coverage
test-coverage:
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

# Regenerate golden files
golden-update:
	@echo "Regenerating golden files..."
	GOLDEN_UPDATE=1 $(TEST_CMD) ./...

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

# Generate CLI reference docs
docs:
	@echo "Generating CLI reference docs..."
	$(GO) run ./cmd/gen-docs --doc-path docs --markdown

# Check CLI docs are up to date (used by CI)
docs-check:
	@echo "Checking CLI docs freshness..."
	@$(GO) run ./cmd/gen-docs --doc-path docs --markdown
	@if ! git diff --quiet docs/cli-reference/; then \
		echo "" >&2; \
		echo "ERROR: CLI docs are out of date. Run 'make docs' and commit." >&2; \
		echo "" >&2; \
		git diff --stat docs/cli-reference/; \
		exit 1; \
	fi
	@echo "CLI docs are up to date."

# ============================================================================
# Pre-commit Targets
# ============================================================================

# Install pre-commit hooks (run once after clone)
pre-commit-install:
	@bash scripts/install-hooks.sh

# Run all pre-commit hooks against all files
pre-commit:
	@pre-commit run --all-files

# ============================================================================
# Protobuf Targets
# ============================================================================

# Generate protobuf Go code from .proto files
proto:
	@echo "Generating protobuf code..."
	@if ! command -v buf >/dev/null 2>&1; then \
		echo "buf not installed. Install: https://buf.build/docs/installation" >&2; exit 1; \
	fi
	@if ! command -v protoc-gen-go >/dev/null 2>&1; then \
		echo "protoc-gen-go not installed. Run: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest" >&2; exit 1; \
	fi
	@if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then \
		echo "protoc-gen-go-grpc not installed. Run: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest" >&2; exit 1; \
	fi
	buf generate
	@echo "Protobuf generation complete."
