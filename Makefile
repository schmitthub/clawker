.PHONY: help update apply-templates build build-version build-all \
        list-versions list-variants clean \
        cli cli-build cli-generate cli-test cli-test-integration cli-lint cli-install cli-clean \
        test test-integration test-e2e test-coverage test-clean golden-update \
        generate-mocks

# Variables
IMAGE_NAME ?= clawker
DOCKERFILES_DIR ?= ./dockerfiles
DOCKER_USERNAME ?= $(shell echo $$DOCKER_USERNAME)

# Default versions to update (stable, latest)
VERSIONS ?= stable latest

# Go CLI variables
BINARY_NAME := clawker
CLI_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
CLI_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
GO ?= go
GOFLAGS := -trimpath
LDFLAGS := -s -w \
	-X 'github.com/schmitthub/clawker/internal/clawker.Version=$(CLI_VERSION)' \
	-X 'github.com/schmitthub/clawker/internal/clawker.Commit=$(CLI_COMMIT)'
BIN_DIR := bin
DIST_DIR := dist

help:
	@echo "Clawker - Claude Code Docker Images & CLI"
	@echo ""
	@echo "Test targets:"
	@echo "  test                Unit tests only (fast, no Docker)"
	@echo "  test-integration    Unit + integration tests (requires Docker)"
	@echo "  test-e2e            All tests including E2E (requires Docker)"
	@echo "  test-coverage       Unit tests with coverage"
	@echo "  test-clean          Remove test Docker resources"
	@echo "  golden-update       Regenerate golden files"
	@echo ""
	@echo "CLI targets:"
	@echo "  cli                 Build the clawker CLI binary"
	@echo "  cli-generate        Build the standalone clawker-generate binary"
	@echo "  cli-test            Run CLI tests (alias for 'test')"
	@echo "  cli-test-integration Run CLI integration tests (alias for 'test-integration')"
	@echo "  cli-lint            Run linter on CLI code"
	@echo "  cli-install         Install CLI to GOPATH/bin"
	@echo "  cli-clean           Remove CLI build artifacts"
	@echo ""
	@echo "Docker image update targets:"
	@echo "  update              Fetch version info and generate Dockerfiles (default: stable latest)"
	@echo "  update VERSIONS='2.1.1 2.1.2'  Update specific versions"
	@echo "  apply-templates     Re-generate Dockerfiles from template (uses versions.json)"
	@echo ""
	@echo "Docker image build targets:"
	@echo "  build VERSION=x.x.x VARIANT=variant  Build a specific version/variant"
	@echo "  build-version VERSION=x.x.x          Build all variants for a version"
	@echo "  build-all                            Build all versions and variants"
	@echo ""
	@echo "Info targets:"
	@echo "  list-versions       List available versions in versions.json"
	@echo "  list-variants       List variants for a VERSION"
	@echo ""
	@echo "Code generation targets:"
	@echo "  generate-mocks      Regenerate mock files for testing"
	@echo ""
	@echo "Other targets:"
	@echo "  clean               Remove generated Dockerfiles"
	@echo ""
	@echo "Examples:"
	@echo "  make cli"
	@echo "  make cli-test"
	@echo "  make update"
	@echo "  make update VERSIONS='2.1.2'"
	@echo "  make build VERSION=2.1.2 VARIANT=alpine3.23"
	@echo "  make build-version VERSION=2.1.2"
	@echo "  make build-all"

# Update versions.json and generate Dockerfiles
update:
	@echo "Updating versions: $(VERSIONS)"
	./versions.sh $(VERSIONS)
	./apply-templates.sh

# Re-apply templates without fetching new version info
apply-templates:
	@echo "Generating Dockerfiles from template..."
	./apply-templates.sh

# Build a specific version/variant
build:
ifndef VERSION
	$(error VERSION is required. Usage: make build VERSION=x.x.x VARIANT=variant)
endif
ifndef VARIANT
	$(error VARIANT is required. Usage: make build VERSION=x.x.x VARIANT=variant)
endif
	@if [ ! -f "$(DOCKERFILES_DIR)/$(VERSION)/$(VARIANT)/Dockerfile" ]; then \
		echo "Error: Dockerfile not found at $(DOCKERFILES_DIR)/$(VERSION)/$(VARIANT)/Dockerfile"; \
		echo "Run 'make list-variants VERSION=$(VERSION)' to see available variants"; \
		exit 1; \
	fi
	@echo "Building $(IMAGE_NAME):$(VERSION)-$(VARIANT)..."
	docker build -t $(IMAGE_NAME):$(VERSION)-$(VARIANT) \
		-f $(DOCKERFILES_DIR)/$(VERSION)/$(VARIANT)/Dockerfile .

# Build all variants for a specific version
build-version:
ifndef VERSION
	$(error VERSION is required. Usage: make build-version VERSION=x.x.x)
endif
	@if [ ! -d "$(DOCKERFILES_DIR)/$(VERSION)" ]; then \
		echo "Error: Version $(VERSION) not found in $(DOCKERFILES_DIR)"; \
		echo "Run 'make list-versions' to see available versions"; \
		exit 1; \
	fi
	@echo "Building all variants for version $(VERSION)..."
	@for variant in $$(ls $(DOCKERFILES_DIR)/$(VERSION)); do \
		echo "Building $(IMAGE_NAME):$(VERSION)-$$variant..."; \
		docker build -t $(IMAGE_NAME):$(VERSION)-$$variant \
			-f $(DOCKERFILES_DIR)/$(VERSION)/$$variant/Dockerfile . || exit 1; \
	done
	@echo "All variants for $(VERSION) built successfully!"

# Build all versions and variants
build-all:
	@echo "Building all versions and variants..."
	@for version in $$(ls $(DOCKERFILES_DIR)); do \
		for variant in $$(ls $(DOCKERFILES_DIR)/$$version); do \
			echo "Building $(IMAGE_NAME):$$version-$$variant..."; \
			docker build -t $(IMAGE_NAME):$$version-$$variant \
				-f $(DOCKERFILES_DIR)/$$version/$$variant/Dockerfile . || exit 1; \
		done; \
	done
	@echo "All images built successfully!"

# List available versions
list-versions:
	@echo "Available versions:"
	@jq -r 'keys[]' versions.json 2>/dev/null || ls $(DOCKERFILES_DIR) 2>/dev/null || echo "No versions found. Run 'make update' first."

# List variants for a version
list-variants:
ifndef VERSION
	$(error VERSION is required. Usage: make list-variants VERSION=x.x.x)
endif
	@echo "Variants for version $(VERSION):"
	@jq -r '.["$(VERSION)"].variants | keys[]' versions.json 2>/dev/null || \
		ls $(DOCKERFILES_DIR)/$(VERSION) 2>/dev/null || \
		echo "Version $(VERSION) not found."

# Clean generated Dockerfiles
clean:
	@echo "Removing generated Dockerfiles..."
	rm -rf $(DOCKERFILES_DIR)/*
	@echo "Cleanup complete!"

# ============================================================================
# CLI Build Targets
# ============================================================================

# Build the CLI binary
cli: cli-build

cli-build:
	@echo "Building $(BINARY_NAME) $(CLI_VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/clawker

# Build the standalone generate binary
cli-generate:
	@echo "Building clawker-generate $(CLI_VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/clawker-generate ./cmd/clawker-generate

# Build CLI for multiple platforms
cli-build-all: cli-build-linux cli-build-darwin cli-build-windows

cli-build-linux:
	@echo "Building CLI for Linux..."
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/clawker
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/clawker

cli-build-darwin:
	@echo "Building CLI for macOS..."
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/clawker
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/clawker

cli-build-windows:
	@echo "Building CLI for Windows..."
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/clawker

# Run CLI tests
cli-test:
	@echo "Running CLI tests..."
	$(GO) test -v ./...

# Run CLI integration tests
cli-test-integration:
	@echo "Running CLI integration tests (requires Docker)..."
	$(GO) test ./pkg/cmd/... -tags=integration -v -timeout 10m

# Run CLI tests with coverage
cli-test-coverage:
	@echo "Running CLI tests with coverage..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Run short tests (skip integration tests)
cli-test-short:
	@echo "Running short CLI tests..."
	$(GO) test -short -v ./...

# Run linter
cli-lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
	fi

# Format code
cli-fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

# Tidy dependencies
cli-tidy:
	@echo "Tidying dependencies..."
	$(GO) mod tidy

# Install CLI to GOPATH/bin
cli-install: cli-build
	@echo "Installing $(BINARY_NAME)..."
	cp $(BIN_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)

# Install CLI to /usr/local/bin (requires sudo)
cli-install-global: cli-build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

# Clean CLI build artifacts
cli-clean:
	@echo "Cleaning CLI build artifacts..."
	rm -rf $(BIN_DIR) $(DIST_DIR)
	rm -f coverage.out coverage.html

# ============================================================================
# Test Targets
# ============================================================================

# Unit tests only (fast, no Docker)
test:
	@echo "Running unit tests..."
	$(GO) test -short ./...

# Unit + integration tests (requires Docker)
test-integration:
	@echo "Running unit + integration tests..."
	$(GO) test -tags=integration ./...

# All tests including E2E
test-e2e:
	@echo "Running all tests including E2E..."
	$(GO) test -tags=integration,e2e -timeout 15m ./...

# Unit tests with coverage
test-coverage:
	@echo "Running unit tests with coverage..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Remove test Docker resources
test-clean:
	@echo "Cleaning test resources..."
	@docker rm -f $$(docker ps -aq --filter "label=com.clawker.test=true") 2>/dev/null || true
	@docker volume rm $$(docker volume ls -q --filter "label=com.clawker.test=true") 2>/dev/null || true
	@docker network rm $$(docker network ls -q --filter "label=com.clawker.test=true") 2>/dev/null || true
	@echo "Test cleanup complete!"

# Regenerate golden files
golden-update:
	@echo "Regenerating golden files..."
	GOLDEN_UPDATE=1 $(GO) test ./...

# ============================================================================
# Code Generation Targets
# ============================================================================

# Generate mock files for testing
# NOTE: Post-processing is required because mockgen copies the Docker SDK's
# unnamed variadic parameters (using `_`) which is invalid Go syntax.
generate-mocks:
	@echo "Generating mocks..."
	@mkdir -p internal/docker/mocks
	mockgen -destination internal/docker/mocks/mock_client.go -package mock github.com/moby/moby/client APIClient
	@echo "Post-processing mocks (fixing unnamed variadic params)..."
	sed -i '' 's/_ \.\.\.client\./opts ...client./g; s/_ \.\.\.any/opts ...any/g; s/range _ {/range opts {/g; s/}, _\.\.\.)/}, opts...)/g' internal/docker/mocks/mock_client.go
	@echo "Mocks generated successfully!"
