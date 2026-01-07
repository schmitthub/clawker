ARG BASE_IMAGE=claude-container:base
FROM ${BASE_IMAGE}

USER root 
# Install Rust build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
  build-essential \
  curl \
  ca-certificates \
  git \
  pkg-config \
  libssl-dev \
  && apt-get clean && rm -rf /var/lib/apt/lists/*


USER node
# Install Rust via rustup
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable

# Set Rust environment
ENV PATH="/home/node/.cargo/bin:${PATH}"
ENV RUSTUP_HOME=/home/node/.rustup
ENV CARGO_HOME=/home/node/.cargo

# Install common Rust tools
RUN . /home/node/.cargo/env && \
    rustup component add rust-analyzer clippy rustfmt && \
    cargo install cargo-watch && \
    rm -rf /home/node/.cargo/registry /home/node/.cargo/git

# Default command
CMD ["/bin/zsh", "-c", "claude"]
