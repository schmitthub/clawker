#!/bin/bash
set -e

# Emit ready signal - called before exec to indicate container is ready
emit_ready() {
    mkdir -p /var/run/clawker
    echo "ts=$(date +%s) pid=$$" > /var/run/clawker/ready
    echo "[clawker] ready ts=$(date +%s) agent=${CLAWKER_AGENT:-default}"
}

# Emit error signal and exit
emit_error() {
    local component="$1"
    local msg="$2"
    echo "[clawker] error component=$component msg=$msg" >&2
    exit 1
}

# Initialize firewall if script exists and we have capabilities
if [ -x /usr/local/bin/init-firewall.sh ] && [ -f /proc/net/ip_tables_names ]; then
    # Write firewall config to file since sudo strips environment variables
    mkdir -p /tmp/clawker
    echo "${CLAWKER_FIREWALL_IP_RANGE_SOURCES:-}" > /tmp/clawker/firewall-ip-range-sources
    echo "${CLAWKER_FIREWALL_DOMAINS:-}" > /tmp/clawker/firewall-domains
    if ! firewall_output=$(sudo /usr/local/bin/init-firewall.sh 2>&1); then
        # Sanitize output for JSON safety (remove newlines, escape quotes)
        sanitized_output=$(echo "$firewall_output" | tr '\n' ' ' | sed 's/"/\\"/g' | head -c 200)
        emit_error "firewall" "initialization failed: $sanitized_output"
    fi
fi

# Initialize config volume with image defaults if missing
INIT_DIR="$HOME/.claude-init"
CONFIG_DIR="$HOME/.claude"

if [ -d "$INIT_DIR" ]; then
    # Copy statusline.sh if missing
    [ ! -f "$CONFIG_DIR/statusline.sh" ] && cp "$INIT_DIR/statusline.sh" "$CONFIG_DIR/statusline.sh"

    # Initialize or merge settings.json
    if [ ! -f "$CONFIG_DIR/settings.json" ]; then
        cp "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json"
    else
        # Merge: init defaults first, user settings override
        jq -s '.[0] * .[1]' "$INIT_DIR/settings.json" "$CONFIG_DIR/settings.json" > "$CONFIG_DIR/settings.json.tmp" 2>/dev/null \
            && mv "$CONFIG_DIR/settings.json.tmp" "$CONFIG_DIR/settings.json" \
            || true
    fi
fi

# Link credentials to global volume for persistence across agents.
# The global credentials volume mounts at ~/.clawker-globals/.
# A symlink ensures writes by Claude Code persist immediately.
CREDS_STAGING="$HOME/.clawker-globals"
CREDS_FILE=".credentials.json"
if [ -d "$CREDS_STAGING" ]; then
    migration_ok=true
    # Migrate: if credentials exist locally but not on global volume, copy them
    if [ -f "$CONFIG_DIR/$CREDS_FILE" ] && [ ! -L "$CONFIG_DIR/$CREDS_FILE" ] && [ ! -f "$CREDS_STAGING/$CREDS_FILE" ]; then
        if ! cp "$CONFIG_DIR/$CREDS_FILE" "$CREDS_STAGING/$CREDS_FILE"; then
            echo "[clawker] warning: failed to migrate credentials to global volume, keeping local copy" >&2
            migration_ok=false
        fi
    fi
    # Replace local file with symlink only if global volume has (or will have) credentials
    if [ "$migration_ok" = true ] || [ -f "$CREDS_STAGING/$CREDS_FILE" ]; then
        rm -f "$CONFIG_DIR/$CREDS_FILE"
        if ! ln -s "$CREDS_STAGING/$CREDS_FILE" "$CONFIG_DIR/$CREDS_FILE"; then
            emit_error "credentials" "failed to create credentials symlink to global volume"
        fi
    fi
    # Enforce restrictive permissions on credentials
    if [ -f "$CREDS_STAGING/$CREDS_FILE" ]; then
        chmod 600 "$CREDS_STAGING/$CREDS_FILE"
    fi
fi

# Setup git configuration from host
# Uses git config commands to selectively copy settings, avoiding credential.helper
HOST_GITCONFIG="/tmp/host-gitconfig"
if [ -f "$HOST_GITCONFIG" ]; then
    # Copy host gitconfig, filtering out the entire [credential] section
    # The awk script skips lines from [credential] until the next section header
    if ! awk '
        /^\[credential/ { in_cred=1; next }
        /^\[/ { in_cred=0 }
        !in_cred { print }
    ' "$HOST_GITCONFIG" > "$HOME/.gitconfig.tmp" 2>/dev/null; then
        echo "Warning: Failed to filter host gitconfig" >&2
        cp "$HOST_GITCONFIG" "$HOME/.gitconfig" 2>/dev/null || true
    elif [ -s "$HOME/.gitconfig.tmp" ]; then
        mv "$HOME/.gitconfig.tmp" "$HOME/.gitconfig"
    else
        rm -f "$HOME/.gitconfig.tmp"
    fi
fi

# Configure git credential helper if HTTPS forwarding is enabled
if [ -n "$CLAWKER_HOST_PROXY" ] && [ "$CLAWKER_GIT_HTTPS" = "true" ]; then
    if ! git config --global credential.helper clawker 2>&1; then
        echo "Warning: Failed to configure git credential helper" >&2
    fi
fi

# Setup SSH known hosts for common git hosting services
ssh_setup_known_hosts() {
    mkdir -p "$HOME/.ssh"
    chmod 700 "$HOME/.ssh"
    cat >> "$HOME/.ssh/known_hosts" << 'KNOWN_HOSTS'
github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=
github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=
gitlab.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf
gitlab.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBFSMqzJeV9rUzU4kWitGjeR4PWSa29SPqJ1fVkhtj3Hw9xjLVXVYrU9QlYWrOLXBpQ6KWjbjTDTdDkoohFzgbEY=
gitlab.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCsj2bNKTBSpIYDEGk9KxsGh3mySTRgMtXL583qmBpzeQ+jqCMRgBqB98u3z++J1sKlXHWfM9dyhSevkMwSbhoR8XIq/U0tCNyokEi/ueaBMCvbcTHhO7FcwzY92WK4Ik8Y0iQ7F3awE8ntZELLwOvLYjzo3yl7hGRM9aLhHaVCF8bCG7cJTbplCCVSLKcQzQasPAOmPTmCC/NfZqrT0cIQ2rWM8Q1xI/z3THw1h19WSSqLBgNmz8M+Z7oqlABp7UMlP8W5K5RUCTASg9K7hNg7Jy3gmr3G6V+/FFHDB5PASg8q2g9ByCVWDqt1r8I5NxpqhUJ47RCrKE01zEIyc9z
bitbucket.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO
bitbucket.org ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBPIQmuzMBuKdWeF4+a2sjSSpBK0iqitSQ+5BM9KhpexuGt20JpTVM7u5BDZngncgrqDMbWdxMWWOGtZ9UgbqgZE=
bitbucket.org ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDQeJzhupRu0u0cdegZIa8e86EG2qOCsIsD1Xw0xSeiPDlCr7kq97NLmMbpKTX6Esc30NuoqEEHCuc7yWtwp8dI76EEEB1VqY9QJq6vk+aySyboD5QF61I/1WeTwu+deCbgKMGbUijeXhtfbxSxm6JwGrXrhBdofTsbKRUsrN1WoNgUa8uqN1Vx6WAJw1JHPhglEGGHea6QICwJOAr/6mrui/oB7pkaWKHj3z7d1IC4KWLtY47elvjbaTlkN04Kc/5LFEirorGYVbt15kAUlqGM65pk6ZBxtaO3+30LVlORZkxOh+LKL/BvbZ/iRNhItLqNyieoQj/uj/4PXhq0r2tVoBqXJCmLk7k+zpcaoprJBFQDa5A7SjqPQK0pCwBvhOT0hHpF0sWH4AIQHvYAWVTD0tBFPF1yENBxnVJpfL0L2qgGxLbQCWgOG0/1ygM+Gf9n0AIksE1h/uoLERBHQXE30XuP4pHV3n+7kO5+nw5VVFIsMfrQ3oT89Si/NvvmM=
KNOWN_HOSTS
    chmod 600 "$HOME/.ssh/known_hosts"
}

# Setup SSH known hosts unconditionally — socketbridge handles SSH/GPG agent forwarding
# via docker exec, but known_hosts are still needed for SSH operations
ssh_setup_known_hosts

# If first argument starts with "-" or isn't a command, prepend "claude"
if [ "${1#-}" != "${1}" ] || [ -z "$(command -v "${1}" 2>/dev/null)" ]; then
    set -- claude "$@"
fi

# Signal readiness before handing off to the main process
emit_ready

exec "$@"
