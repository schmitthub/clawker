.PHONY: help build-base build-nodejs build-python build-go build-rust build-all \
        push-base push-nodejs push-python push-go push-rust push-all \
        test-base test-nodejs test-python test-go test-rust clean

# Variables
IMAGE_NAME ?= claude-container
VERSION ?= latest
PLATFORMS ?= linux/amd64,linux/arm64
DOCKERFILE_PATH ?= ./claude-container/Dockerfile

# Check if DOCKER_USERNAME is set
ifndef DOCKER_USERNAME
$(error DOCKER_USERNAME is not set. Please set it with: export DOCKER_USERNAME=your-dockerhub-username)
endif

# Image tags
BASE_TAG = $(DOCKER_USERNAME)/$(IMAGE_NAME):base
NODEJS_TAG = $(DOCKER_USERNAME)/$(IMAGE_NAME):node
PYTHON_TAG = $(DOCKER_USERNAME)/$(IMAGE_NAME):python
GO_TAG = $(DOCKER_USERNAME)/$(IMAGE_NAME):go
RUST_TAG = $(DOCKER_USERNAME)/$(IMAGE_NAME):rust

help:
	@echo "Claude Container Makefile"
	@echo ""
	@echo "Prerequisites:"
	@echo "  export DOCKER_USERNAME=your-dockerhub-username"
	@echo ""
	@echo "Available targets:"
	@echo "  build-base      Build base image with Claude Code"
	@echo "  build-node      Build Node.js image"
	@echo "  build-python    Build Python image"
	@echo "  build-go        Build Go image"
	@echo "  build-rust      Build Rust image"
	@echo "  build-all       Build all images"
	@echo ""
	@echo "  push-base       Push base image to DockerHub"
	@echo "  push-node     Push Node.js image to DockerHub"
	@echo "  push-python     Push Python image to DockerHub"
	@echo "  push-go         Push Go image to DockerHub"
	@echo "  push-rust       Push Rust image to DockerHub"
	@echo "  push-all        Push all images to DockerHub"
	@echo ""
	@echo "  test-base       Run base container interactively"
	@echo "  test-node     Run Node.js container interactively"
	@echo "  test-python     Run Python container interactively"
	@echo "  test-go         Run Go container interactively"
	@echo "  test-rust       Run Rust container interactively"
	@echo ""
	@echo "  clean           Remove all locally built images"

# Build targets
build-base:
	@echo "Building base image..."
	docker build -t $(BASE_TAG) \
		--target base \
		-f $(DOCKERFILE_PATH) . 
	docker tag $(BASE_TAG) $(IMAGE_NAME):base

build-node: build-base
	@echo "Building Node.js image..."
	docker build -t $(NODEJS_TAG) \
		--target node \
		-f $(DOCKERFILE_PATH) .

build-python: build-base
	@echo "Building Python image..."
	docker build -t $(PYTHON_TAG) \
		--target python \
		-f $(DOCKERFILE_PATH) .

build-go: build-base
	@echo "Building Go image..."
	docker build -t $(GO_TAG) \
		--target go \
		-f $(DOCKERFILE_PATH) .

build-rust: build-base
	@echo "Building Rust image..."
	docker build -t $(RUST_TAG) \
		--target rust \
		-f $(DOCKERFILE_PATH) .

build-all: build-base build-node build-python build-go build-rust
	@echo "All images built successfully!"

# Push targets
push-base:
	@echo "Pushing base image..."
	docker push $(BASE_TAG)

push-nodejs:
	@echo "Pushing Node.js image..."
	docker push $(NODEJS_TAG)

push-python:
	@echo "Pushing Python image..."
	docker push $(PYTHON_TAG)

push-go:
	@echo "Pushing Go image..."
	docker push $(GO_TAG)

push-rust:
	@echo "Pushing Rust image..."
	docker push $(RUST_TAG)

push-all: push-base push-nodejs push-python push-go push-rust
	@echo "All images pushed successfully!"

# Test targets - exec command to test containers
test-base:
	docker run --rm -it -v $(PWD):/workspace $(BASE_TAG) /bin/zsh

test-node:
	docker run --rm -it -v $(PWD):/workspace $(NODEJS_TAG) /bin/zsh

test-python:
	docker run --rm -it -v $(PWD):/workspace $(PYTHON_TAG) /bin/zsh

test-go:
	docker run --rm -it -v $(PWD):/workspace $(GO_TAG) /bin/zsh

test-rust:
	docker run --rm -it -v $(PWD):/workspace $(RUST_TAG) /bin/zsh

# Clean target
clean:
	@echo "Removing locally built images..."
	-docker rmi $(BASE_TAG) $(IMAGE_NAME):base
	-docker rmi $(NODEJS_TAG)
	-docker rmi $(PYTHON_TAG)
	-docker rmi $(GO_TAG)
	-docker rmi $(RUST_TAG)
	@echo "Cleanup complete!"
