# Claucker Makefile
# Build automation for the Claucker CLI

.PHONY: all build clean test lint install help

# Build variables
BINARY_NAME := claucker
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go variables
GO ?= go
GOFLAGS := -trimpath
LDFLAGS := -s -w \
	-X 'github.com/claucker/claucker/cmd.Version=$(VERSION)' \
	-X 'github.com/claucker/claucker/cmd.Commit=$(COMMIT)'

# Output directories
BIN_DIR := bin
DIST_DIR := dist

# Default target
all: build

## Build targets

# Build the binary
build:
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) .

# Build for multiple platforms
build-all: build-linux build-darwin build-windows

build-linux:
	@echo "Building for Linux..."
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .

build-darwin:
	@echo "Building for macOS..."
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 .

build-windows:
	@echo "Building for Windows..."
	@mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe .

## Development targets

# Run tests
test:
	@echo "Running tests..."
	$(GO) test -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Run short tests (skip integration tests)
test-short:
	@echo "Running short tests..."
	$(GO) test -short -v ./...

# Run linter
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
	fi

# Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	$(GO) mod tidy

# Verify dependencies
verify:
	@echo "Verifying dependencies..."
	$(GO) mod verify

## Install/Uninstall

# Install to GOPATH/bin
install: build
	@echo "Installing $(BINARY_NAME)..."
	cp $(BIN_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)

# Install to /usr/local/bin (requires sudo)
install-global: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

# Uninstall from GOPATH/bin
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	rm -f $(GOPATH)/bin/$(BINARY_NAME)

## Cleanup

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(BIN_DIR) $(DIST_DIR)
	rm -f coverage.out coverage.html

# Deep clean (including Go cache)
clean-all: clean
	$(GO) clean -cache -testcache

## Development helpers

# Run the CLI (for development)
run: build
	./$(BIN_DIR)/$(BINARY_NAME) $(ARGS)

# Watch for changes and rebuild (requires entr)
watch:
	@if command -v entr >/dev/null 2>&1; then \
		find . -name '*.go' | entr -r make build; \
	else \
		echo "entr not installed. Install with: brew install entr (macOS) or apt install entr (Linux)"; \
	fi

## Help

help:
	@echo "Claucker Build System"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build targets:"
	@echo "  build          Build the binary"
	@echo "  build-all      Build for all platforms"
	@echo "  build-linux    Build for Linux (amd64, arm64)"
	@echo "  build-darwin   Build for macOS (amd64, arm64)"
	@echo "  build-windows  Build for Windows (amd64)"
	@echo ""
	@echo "Development targets:"
	@echo "  test           Run all tests"
	@echo "  test-short     Run tests (skip integration)"
	@echo "  test-coverage  Run tests with coverage report"
	@echo "  lint           Run linter"
	@echo "  fmt            Format code"
	@echo "  tidy           Tidy dependencies"
	@echo "  verify         Verify dependencies"
	@echo ""
	@echo "Install targets:"
	@echo "  install        Install to GOPATH/bin"
	@echo "  install-global Install to /usr/local/bin"
	@echo "  uninstall      Uninstall from GOPATH/bin"
	@echo ""
	@echo "Cleanup targets:"
	@echo "  clean          Remove build artifacts"
	@echo "  clean-all      Remove all artifacts and Go cache"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION        Version string (default: git describe)"
	@echo "  ARGS           Arguments to pass to 'make run'"
