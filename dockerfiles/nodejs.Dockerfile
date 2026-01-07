ARG BASE_IMAGE=claude-container:base
FROM ${BASE_IMAGE}

# Install additional Node.js package managers
RUN npm install -g yarn pnpm && npm cache clean --force

# Set development environment
ENV NODE_ENV=development

# Use claude as the default command, but allow easy override
CMD ["/bin/zsh", "-c", "claude"]
