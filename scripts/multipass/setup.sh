#!/usr/bin/env bash
set -euo pipefail

echo "==> Updating apt"
sudo apt update
sudo apt upgrade -y

echo "==> Installing base tools"
sudo apt install -y \
  ca-certificates \
  curl \
  gnupg \
  git \
  build-essential \
  jq \
  unzip

echo "==> Installing Docker (official repo)"
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
  sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
  https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt update
sudo apt install -y \
  docker-ce \
  docker-ce-cli \
  containerd.io \
  docker-buildx-plugin \
  docker-compose-plugin

echo "==> Adding $USER to docker group"
sudo usermod -aG docker "$USER"

echo "==> Installing nvm + Node LTS"
export NVM_DIR="$HOME/.nvm"
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
# shellcheck disable=SC1091
. "$NVM_DIR/nvm.sh"
nvm install --lts
nvm alias default lts/*
nvm use default

echo "==> Installing Claude Code"
npm install -g @anthropic-ai/claude-code

echo "==> Adding ~/.local/bin to PATH in ~/.bashrc"
mkdir -p "$HOME/.local/bin"
if ! grep -q 'HOME/.local/bin' "$HOME/.bashrc"; then
  echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME/.bashrc"
fi

echo "==> Verifying installs"
git --version
docker --version
node --version
npm --version
claude --version || true

cat <<'EOF'

==> Setup complete.

NOTE: Docker group membership requires a new shell to take effect.
Run one of:
  - exit and re-shell:  exit ; multipass shell clawker
  - or activate now:    newgrp docker

The PATH change in ~/.bashrc will also apply in new shells.

Then test: docker run hello-world

Authenticate Claude Code by running: claude
EOF
