FROM node:20

ARG TZ
ENV TZ="$TZ"

ARG CLAUDE_CODE_VERSION=latest

# Install basic development tools and iptables/ipset
RUN apt-get update && apt-get install -y --no-install-recommends \
  less \
  git \
  procps \
  sudo \
  fzf \
  zsh \
  man-db \
  unzip \
  gnupg2 \
  gh \
  iptables \
  ipset \
  iproute2 \
  dnsutils \
  aggregate \
  jq \
  nano \
  vim \
  gawk \
  tar \
  locales \
  locales-all \
  ca-certificates \
  curl \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

# install Go
ENV PATH=/usr/local/go/bin:$PATH
ENV GOLANG_VERSION=1.25.5
RUN ARCH=$(dpkg --print-architecture) && \
  curl -fsSL "https://golang.org/dl/go1.25.5.linux-${ARCH}.tar.gz" | tar -C /usr/local -xzf - \
  && echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile.d/go.sh

# Install Docker CLI (not daemon - we use host's Docker via socket mount)
RUN install -m 0755 -d /etc/apt/keyrings && \
  curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc && \
  chmod a+r /etc/apt/keyrings/docker.asc && \
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list && \
  apt-get update && \
  apt-get install -y --no-install-recommends docker-ce-cli docker-buildx-plugin docker-compose-plugin && \
  apt-get clean && rm -rf /var/lib/apt/lists/*

# Create docker group with GID 1001 to match typical host docker group
# and add node user to it for socket access
RUN groupadd -g 1001 docker && usermod -aG docker node

RUN locale-gen en_US.UTF-8

# Ensure default node user has access to /usr/local/share
RUN mkdir -p /usr/local/share/npm-global && \
  chown -R node:node /usr/local/share

ARG USERNAME=node

# Persist bash history.
RUN SNIPPET="export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  && mkdir /commandhistory \
  && touch /commandhistory/.bash_history \
  && chown -R $USERNAME /commandhistory

# Set `DEVCONTAINER` environment variable to help with orientation
ENV DEVCONTAINER=true

# Create workspace and config directories and set permissions
RUN mkdir -p /workspace /home/node/.claude /home/node/.local/bin && \
  chown -R node:node /workspace /home/node/

WORKDIR /workspace

ARG GIT_DELTA_VERSION=0.18.2
RUN ARCH=$(dpkg --print-architecture) && \
  wget "https://github.com/dandavison/delta/releases/download/${GIT_DELTA_VERSION}/git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && \
  sudo dpkg -i "git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && \
  rm "git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb"

# Set up non-root user
USER node

# Install global packages
ENV NPM_CONFIG_PREFIX=/usr/local/share/npm-global
ENV PATH=/home/node/.local/bin:$PATH:/usr/local/share/npm-global/bin
ENV PATH=$PATH:/usr/local/go/bin:/home/node/go/bin

# Set the default shell to zsh rather than sh
ENV SHELL=/bin/zsh

# Set the default editor and visual
ENV EDITOR=nano
ENV VISUAL=nano

# Default powerline10k theme
ARG ZSH_IN_DOCKER_VERSION=1.2.0
RUN sh -c "$(wget -O- https://github.com/deluan/zsh-in-docker/releases/download/v${ZSH_IN_DOCKER_VERSION}/zsh-in-docker.sh)" -- \
  -p git \
  -p fzf \
  -p dotenv \
  -p zsh-autosuggestions \
  -a "source /usr/share/doc/fzf/examples/key-bindings.zsh" \
  -a "source /usr/share/doc/fzf/examples/completion.zsh" \
  -a "export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  -x

RUN curl -LsSf https://astral.sh/uv/install.sh | sh
RUN /usr/local/go/bin/go install golang.org/x/tools/gopls@latest

# Install Claude
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} @ast-grep/cli
COPY --chown=node:node statusline.sh /home/node/.claude-init/statusline.sh
RUN chmod +x /home/node/.claude-init/statusline.sh
COPY --chown=node:node claude-settings.json /home/node/.claude-init/settings.json
RUN claude mcp add -s local serena -- uvx --from git+https://github.com/oraios/serena serena start-mcp-server --context claude-code --project "$(pwd)" --enable-web-dashboard false

# Currently you can't run docker mcp client connect inside a container due to a check docker does to see if docker desktop is running. see: https://github.com/docker/mcp-gateway/issues/112
# Install Docker MCP 
# ARG DOCKER_MCP_VERSION=0.35.0
# RUN ARCH=$(dpkg --print-architecture) && \
#   mkdir -p /home/node/.docker/cli-plugins && \
#   curl -fsSL "https://github.com/docker/mcp-gateway/releases/download/v${DOCKER_MCP_VERSION}/docker-mcp-linux-${ARCH}.tar.gz" | tar -C ~/.docker/cli-plugins -xzf - && \
#   chmod +x /home/node/.docker/cli-plugins/docker-mcp
# RUN docker mcp client connect claude-code 

# Initialize Claude config from staged defaults
RUN INIT_DIR="$HOME/.claude-init" && \
  CONFIG_DIR="$HOME/.claude" && \
  if [ -d "$INIT_DIR" ]; then \
    # Copy statusline.sh if missing
    [ ! -f "$CONFIG_DIR/statusline.sh" ] && cp "$INIT_DIR/statusline.sh" "$CONFIG_DIR/statusline.sh"; \
    # Initialize or merge settings.json
    if [ ! -f "$CONFIG_DIR/settings.json" ]; then \
      cp "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json"; \
    else \
      # Merge: init defaults first, user settings override
      jq -s '.[0] * .[1]' "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json" > "$CONFIG_DIR/settings.json.tmp" 2>/dev/null \
        && mv "$CONFIG_DIR/settings.json.tmp" "$CONFIG_DIR/settings.json" \
        || true; \
    fi; \
  fi

# Copy and set up firewall script
COPY init-firewall.sh /usr/local/bin/
USER root
RUN chmod +x /usr/local/bin/init-firewall.sh && \
  echo "node ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh" > /etc/sudoers.d/node-firewall && \
  chmod 0440 /etc/sudoers.d/node-firewall
USER node
