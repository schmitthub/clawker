# SSH Known Hosts Fix

## Problem
SSH git operations inside containers failed with "Host key verification failed" because `~/.ssh/known_hosts` doesn't exist. While SSH agent forwarding works (`SSH_AUTH_SOCK` is set), the container user has no `~/.ssh` directory.

## Solution
Added SSH known hosts setup to `pkg/build/templates/entrypoint.sh` that runs when `SSH_AUTH_SOCK` is set (SSH forwarding is enabled). Pre-populates `~/.ssh/known_hosts` with official public keys from:
- GitHub
- GitLab
- Bitbucket

This approach matches VS Code Dev Containers behavior.

## Implementation Location
- `pkg/build/templates/entrypoint.sh` - Lines 51-68

## Key Sources for Host Keys
- GitHub: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints
- GitLab: https://docs.gitlab.com/ee/user/gitlab_com/#ssh-host-keys-fingerprints
- Bitbucket: https://support.atlassian.com/bitbucket-cloud/docs/configure-ssh-and-two-step-verification/

## Status
Implemented and tested.
