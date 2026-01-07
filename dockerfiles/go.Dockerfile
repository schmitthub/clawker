ARG BASE_IMAGE=claude-container:base
FROM ${BASE_IMAGE}

# Install Go
RUN apt-get update && apt-get install -y --no-install-recommends \
    golang \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Set Go environment variables
ENV GOPATH=/go
ENV PATH=$PATH:/go/bin

# Create Go workspace
RUN mkdir -p /go/src /go/bin /go/pkg

# Install common Go tools
RUN go install golang.org/x/tools/gopls@latest && \
    go install github.com/go-delve/delve/cmd/dlv@latest && \
    go clean -cache -modcache

# Default command
CMD ["/bin/zsh", "-c", "claude"]
