# Security Policy

## Supported Versions

Clawker is currently in **alpha**. Security fixes are applied to the latest version only.

| Version | Supported |
|---------|-----------|
| Latest (main branch) | Yes |
| Older releases | No |

## Reporting a Vulnerability

If you discover a security vulnerability in Clawker, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please use one of the following methods:

1. **GitHub Security Advisories** (preferred): Use [GitHub's private vulnerability reporting](https://github.com/schmitthub/clawker/security/advisories/new) to submit a report directly.

2. **Email**: Contact the maintainer at the email address listed in the Git commit history.

### What to Include

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### What to Expect

- **Acknowledgment** within 48 hours
- **Assessment** within 1 week
- **Fix or mitigation plan** communicated within 2 weeks

For accepted vulnerabilities, we will:
- Develop and test a fix
- Release a patched version
- Credit you in the release notes (unless you prefer anonymity)

For declined reports, we will explain our reasoning.

## Security Model

Clawker's security model is documented in the project's design docs. Key points:

- **Containers are the security boundary** — Clawker protects the host from what happens inside containers
- **Network firewall** is enabled by default (outbound blocked except allowlisted domains)
- **Docker socket access** is disabled by default
- **Git credentials** are forwarded (never copied) via the host proxy
- **Label-based isolation** ensures Clawker never touches non-Clawker Docker resources

## Scope

The following are considered security issues:

- Container escape or host filesystem access beyond configured bind mounts
- Credential leakage (API keys, OAuth tokens, git credentials)
- Firewall bypass allowing unauthorized network access
- Operations on non-Clawker Docker resources (label isolation failure)
- Privilege escalation within the container beyond intended permissions

The following are **not** in scope:

- Vulnerabilities in Docker itself (report to Docker)
- Vulnerabilities in Claude Code (report to Anthropic)
- Issues requiring physical access to the host machine
- Social engineering attacks
