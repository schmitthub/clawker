FROM debian:bookworm AS base

# Install basic development tools and iptables/ipset
RUN apt-get update && apt-get install -y --no-install-recommends \
  nodejs \
  npm \
  less \
  git \
  procps \
  sudo \
  fzf \
  zsh \
  man-db \
  unzip \
  gnupg2 \
  iptables \
  ipset \
  iproute2 \
  dnsutils \
  aggregate \
  jq \
  nano \
  vim \
  wget \
  curl \
  gh \
  locales \
  locales-all \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

RUN locale-gen en_US.UTF-8

# Setup claude user
RUN groupadd --gid 1001 claude \
  && useradd --uid 1001 --gid claude --shell /bin/bash --create-home claude

# Ensure default claude user has access to /usr/local/share
RUN mkdir -p /usr/local/share/npm-global && \
  chown -R claude:claude /usr/local/share

ARG USERNAME=claude

# Persist bash history.
RUN SNIPPET="export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  && mkdir /commandhistory \
  && touch /commandhistory/.bash_history \
  && chown -R $USERNAME /commandhistory


# Create workspace and config directories and set permissions
RUN mkdir -p /home/claude/.claude /workspace && \
  chown -R claude:claude /workspace /home/claude/.claude

WORKDIR /workspace

ARG GIT_DELTA_VERSION=0.18.2
RUN ARCH=$(dpkg --print-architecture) && \
  wget "https://github.com/dandavison/delta/releases/download/${GIT_DELTA_VERSION}/git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && \
  sudo dpkg -i "git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && \
  rm "git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb"

# Set up non-root user
USER claude

# Install global packages
ENV NPM_CONFIG_PREFIX=/usr/local/share/npm-global
ENV PATH=$PATH:/usr/local/share/npm-global/bin

# Set environment variables for shell, editor
ENV SHELL=/bin/zsh
ENV EDITOR=nano
ENV VISUAL=nano

# Use zsh-in-docker to configure plugins and theme
ARG ZSH_IN_DOCKER_VERSION=1.2.0
ARG ZSH_THEME=default
RUN sh -c "$(wget -O- https://github.com/deluan/zsh-in-docker/releases/download/v${ZSH_IN_DOCKER_VERSION}/zsh-in-docker.sh)" -- \
  -t ${ZSH_THEME} \
  -p git \
  -p fzf \
  -a "source /usr/share/doc/fzf/examples/key-bindings.zsh" \
  -a "source /usr/share/doc/fzf/examples/completion.zsh" \
  -a "export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  -x

# Install Claude Code and ast-grep globally via npm
ARG CLAUDE_CODE_VERSION=latest
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} @ast-grep/cli && npm cache clean --force

# Native install might be better and avoids node dependency
# curl -fsSL https://claude.ai/install.sh | bash -s 1.0.58

# Copy and set up firewall script
COPY ./claude-container/init-firewall.sh /usr/local/bin/
USER root
RUN chmod +x /usr/local/bin/init-firewall.sh && \
  echo "${USERNAME} ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh" > /etc/sudoers.d/${USERNAME}-firewall && \
  chmod 0440 /etc/sudoers.d/${USERNAME}-firewall

COPY ./claude-container/docker-entrypoint.sh /usr/local/bin/
USER root
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
USER ${USERNAME}

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["claude"]

FROM base AS go

USER root
# Install Go
RUN apt-get update && apt-get install -y --no-install-recommends \
    golang \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

USER claude

# Set Go environment variables
ENV GOPATH=/home/claude/go
ENV PATH=$PATH:/usr/local/go/bin:$GOPATH/bin

FROM base AS node

# Install additional Node.js package managers
RUN npm install -g yarn pnpm && npm cache clean --force

# Set development environment
ENV NODE_ENV=development

FROM base AS python

USER root
# Install Python and build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
  python3 \
  python3-pip \
  python3-venv \
  build-essential \
  libssl-dev \
  libffi-dev \
  libxml2-dev \
  libxslt1-dev \
  zlib1g-dev \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Poetry
RUN pip3 install --no-cache-dir poetry --break-system-packages

# Install uv (modern Python package manager)
RUN pip3 install --no-cache-dir uv --break-system-packages

# Set Python unbuffered mode
ENV PYTHONUNBUFFERED=1

# Create symlink for python command
RUN ln -sf /usr/bin/python3 /usr/bin/python

USER claude

FROM base AS rust

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


USER claude
# Install Rust via rustup
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable

# Set Rust environment
ENV PATH="/home/claude/.cargo/bin:${PATH}"
ENV RUSTUP_HOME=/home/claude/.rustup
ENV CARGO_HOME=/home/claude/.cargo

# Install common Rust tools
RUN . /home/claude/.cargo/env && \
    rustup component add rust-analyzer clippy rustfmt && \
    cargo install cargo-watch && \
    rm -rf /home/claude/.cargo/registry /home/claude/.cargo/git
