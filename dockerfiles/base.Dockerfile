FROM alpine:latest

# Install Node.js and common development tools
RUN apk add --no-cache \
    nodejs \
    npm \
    git \
    bash \
    curl \
    wget \
    ca-certificates \
    openssh-client \
    && rm -rf /var/cache/apk/*

# Install Claude Code globally via npm
RUN npm install -g @anthropic/claude-code && npm cache clean --force

# Create workspace and Claude config directories
RUN mkdir -p /workspace /root/.claude

# Set working directory
WORKDIR /workspace

# Set bash as default shell
ENV SHELL=/bin/bash

# Default command
CMD ["/bin/bash"]
