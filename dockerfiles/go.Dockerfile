ARG BASE_IMAGE=claude-container:base
FROM ${BASE_IMAGE}

USER root 
# Install Go
RUN apt-get update && apt-get install -y --no-install-recommends \
    golang \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

USER node

# Set Go environment variables
ENV GOPATH=/home/node/go
ENV PATH=$PATH:/usr/local/go/bin:$GOPATH/bin

# Default command
CMD ["/bin/zsh", "-c", "claude"]
