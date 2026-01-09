#
# NOTE: THIS DOCKERFILE IS GENERATED VIA "apply-templates.sh"
#
# PLEASE DO NOT EDIT IT DIRECTLY.
#

FROM alpine:3.23 AS build

ARG TZ
ENV TZ="$TZ"

ENV CLAUDE_CODE_VERSION=2.1.2

# Alpine pkgs
RUN apk add --no-cache \
  bash \
  less \
  git \
  procps \
  sudo \
  fzf \
  zsh \
  man-db \
  unzip \
  gnupg \
  iptables \
  ipset \
  iproute2 \
  bind-tools \
  jq \
  nano \
  vim \
  wget \
  curl \
  github-cli \
  musl-locales \
  musl-locales-lang \
  && rm -rf /var/cache/apk/*


# Install Docker CLI (not daemon - we use host's Docker via socket mount)
RUN apk add --no-cache \
  docker-cli \
  docker-cli-buildx \
  docker-cli-compose

# Alpine uses musl-locales, no locale-gen needed

ARG USERNAME=claude

# Setup claude user
RUN addgroup -g 1001 ${USERNAME} \
  && adduser -D -u 1001 -G ${USERNAME} -s /bin/zsh ${USERNAME}

# Create docker group and add user for socket access
RUN addgroup -g 999 docker 2>/dev/null || addgroup docker 2>/dev/null || true && adduser ${USERNAME} docker

# Create workspace and config directories and set permissions
RUN mkdir -p /home/${USERNAME}/.claude /workspace && \
  chown -R ${USERNAME}:${USERNAME} /workspace /home/${USERNAME}/.claude

# Persist bash history.
RUN SNIPPET="export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  && mkdir /commandhistory \
  && touch /commandhistory/.bash_history \
  && chown -R $USERNAME /commandhistory

WORKDIR /workspace

# Install git-delta
RUN apk add --no-cache delta

# Install hadolint (Dockerfile linter)
ARG HADOLINT_VERSION=2.12.0
RUN ARCH=$(uname -m) && \
  if [ "$ARCH" = "x86_64" ]; then ARCH="x86_64"; \
  elif [ "$ARCH" = "aarch64" ]; then ARCH="arm64"; fi && \
  wget "https://github.com/hadolint/hadolint/releases/download/v${HADOLINT_VERSION}/hadolint-Linux-${ARCH}" -O /usr/local/bin/hadolint && \
  chmod +x /usr/local/bin/hadolint

# Set up non-root user
USER ${USERNAME}

# Set environment config
ENV SHELL=/bin/zsh
ENV EDITOR=nano
ENV VISUAL=nano
ENV PATH=/home/${USERNAME}/.local/bin:$PATH

# Use zsh-in-docker to configure plugins and theme
ARG ZSH_IN_DOCKER_VERSION=1.2.0
ARG ZSH_THEME=default
RUN sh -c "$(wget -O- https://github.com/deluan/zsh-in-docker/releases/download/v${ZSH_IN_DOCKER_VERSION}/zsh-in-docker.sh)" -- \
  -t ${ZSH_THEME} \
  -p git \
  -p fzf \
  -a "source /usr/share/fzf/key-bindings.zsh" \
  -a "source /usr/share/fzf/completion.zsh" \
  -a "export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  -x

ARG TEMPLATE_DIR=./build/templates

# Copy and set up firewall script
COPY ${TEMPLATE_DIR}/docker-init-firewall.sh /usr/local/bin/init-firewall.sh
USER root
RUN chmod +x /usr/local/bin/init-firewall.sh && \
  echo "${USERNAME} ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh" > /etc/sudoers.d/${USERNAME}-firewall && \
  chmod 0440 /etc/sudoers.d/${USERNAME}-firewall
USER ${USERNAME}

# Install Claude Code
RUN curl -fsSL "https://claude.ai/install.sh" | bash -s ${CLAUDE_CODE_VERSION}

# Copy and set up entrypoint script
COPY ${TEMPLATE_DIR}/docker-entrypoint.sh /usr/local/bin/
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["claude"]
