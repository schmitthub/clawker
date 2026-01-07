ARG BASE_IMAGE=claude-container:base
FROM ${BASE_IMAGE}

# Install Rust build dependencies
RUN apk add --no-cache \
    gcc \
    musl-dev \
    && rm -rf /var/cache/apk/*

# Install Rust via rustup
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable

# Set Rust environment
ENV PATH="/root/.cargo/bin:${PATH}"
ENV RUSTUP_HOME=/root/.rustup
ENV CARGO_HOME=/root/.cargo

# Install common Rust tools
RUN . /root/.cargo/env && \
    rustup component add rust-analyzer clippy rustfmt && \
    cargo install cargo-watch && \
    rm -rf /root/.cargo/registry /root/.cargo/git

# Default command
CMD ["/bin/bash"]
