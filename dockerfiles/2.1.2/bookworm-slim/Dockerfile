#
# NOTE: THIS DOCKERFILE IS GENERATED VIA "apply-templates.sh"
#
# PLEASE DO NOT EDIT IT DIRECTLY.
#

FROM buildpack-deps:bookworm-slim-scm AS build

ENV CLAUDE_CODE_VERSION=2.1.2

# TODO figure out which of these are already installed in buildpack-deps
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

ARG USERNAME=claude

# Setup claude user
RUN groupadd --gid 1001 ${USERNAME} \
  && useradd --uid 1001 --gid ${USERNAME} --shell /bin/zsh --create-home ${USERNAME}

# Create workspace and config directories and set permissions
RUN mkdir -p /home/${USERNAME}/.claude /workspace && \
  chown -R ${USERNAME}:${USERNAME} /workspace /home/${USERNAME}/.claude

# Persist bash history.
RUN SNIPPET="export PROMPT_COMMAND='history -a' && export HISTFILE=/commandhistory/.bash_history" \
  && mkdir /commandhistory \
  && touch /commandhistory/.bash_history \
  && chown -R $USERNAME /commandhistory

WORKDIR /workspace

ARG GIT_DELTA_VERSION=0.18.2
RUN ARCH=$(dpkg --print-architecture) && \
  wget "https://github.com/dandavison/delta/releases/download/${GIT_DELTA_VERSION}/git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && \
  sudo dpkg -i "git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb" && \
  rm "git-delta_${GIT_DELTA_VERSION}_${ARCH}.deb"

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
  -a "source /usr/share/doc/fzf/examples/key-bindings.zsh" \
  -a "source /usr/share/doc/fzf/examples/completion.zsh" \
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
