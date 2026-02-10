> ## Documentation Index
> Fetch the complete documentation index at: https://code.claude.com/docs/llms.txt
> Use this file to discover all available pages before exploring further.

# Authentication

> Learn how to configure user authentication and credential management for Claude Code in your organization.

## Authentication methods

Setting up Claude Code requires access to Anthropic models. For teams, you can set up Claude Code access in one of these ways:

* [Claude for Teams or Enterprise](#claude-for-teams-or-enterprise) (recommended)
* [Claude Console](#claude-console-authentication)
* [Amazon Bedrock](/en/amazon-bedrock)
* [Google Vertex AI](/en/google-vertex-ai)
* [Microsoft Foundry](/en/microsoft-foundry)

### Claude for Teams or Enterprise

[Claude for Teams](https://claude.com/pricing#team-&-enterprise) and [Claude for Enterprise](https://anthropic.com/contact-sales) provide the best experience for organizations using Claude Code. Team members get access to both Claude Code and Claude on the web with centralized billing and team management.

* **Claude for Teams**: self-service plan with collaboration features, admin tools, and billing management. Best for smaller teams.
* **Claude for Enterprise**: adds SSO, domain capture, role-based permissions, compliance API, and managed policy settings for organization-wide Claude Code configurations. Best for larger organizations with security and compliance requirements.

<Steps>
  <Step title="Subscribe">
    Subscribe to [Claude for Teams](https://claude.com/pricing#team-&-enterprise) or contact sales for [Claude for Enterprise](https://anthropic.com/contact-sales).
  </Step>

  <Step title="Invite team members">
    Invite team members from the admin dashboard.
  </Step>

  <Step title="Install and log in">
    Team members install Claude Code and log in with their Claude.ai accounts.
  </Step>
</Steps>

### Claude Console authentication

For organizations that prefer API-based billing, you can set up access through the Claude Console.

<Steps>
  <Step title="Create or use a Console account">
    Use your existing Claude Console account or create a new one.
  </Step>

  <Step title="Add users">
    You can add users through either method:

    * Bulk invite users from within the Console (Console -> Settings -> Members -> Invite)
    * [Set up SSO](https://support.claude.com/en/articles/13132885-setting-up-single-sign-on-sso)
  </Step>

  <Step title="Assign roles">
    When inviting users, assign one of:

    * **Claude Code** role: users can only create Claude Code API keys
    * **Developer** role: users can create any kind of API key
  </Step>

  <Step title="Users complete setup">
    Each invited user needs to:

    * Accept the Console invite
    * [Check system requirements](/en/setup#system-requirements)
    * [Install Claude Code](/en/setup#installation)
    * Log in with Console account credentials
  </Step>
</Steps>

### Cloud provider authentication

For teams using Amazon Bedrock, Google Vertex AI, or Microsoft Azure:

<Steps>
  <Step title="Follow provider setup">
    Follow the [Bedrock docs](/en/amazon-bedrock), [Vertex docs](/en/google-vertex-ai), or [Microsoft Foundry docs](/en/microsoft-foundry).
  </Step>

  <Step title="Distribute configuration">
    Distribute the environment variables and instructions for generating cloud credentials to your users. Read more about how to [manage configuration here](/en/settings).
  </Step>

  <Step title="Install Claude Code">
    Users can [install Claude Code](/en/setup#installation).
  </Step>
</Steps>

## Credential management

Claude Code securely manages your authentication credentials:

* **Storage location**: on macOS, API keys, OAuth tokens, and other credentials are stored in the encrypted macOS Keychain.
* **Supported authentication types**: Claude.ai credentials, Claude API credentials, Azure Auth, Bedrock Auth, and Vertex Auth.
* **Custom credential scripts**: the [`apiKeyHelper`](/en/settings#available-settings) setting can be configured to run a shell script that returns an API key.
* **Refresh intervals**: by default, `apiKeyHelper` is called after 5 minutes or on HTTP 401 response. Set `CLAUDE_CODE_API_KEY_HELPER_TTL_MS` environment variable for custom refresh intervals.

## Clawker Global Volume Integration

Clawker uses a `clawker-globals` Docker volume to persist Claude Code credentials across all projects and agents.

### How it works
1. `workspace.EnsureGlobalsVolume()` creates the `clawker-globals` volume (labels: `com.clawker.managed=true`, `com.clawker.purpose=globals`)
2. Volume mounts at `/home/claude/.clawker-globals/` in containers
3. Entrypoint (`entrypoint.sh`) symlinks `~/.claude/.credentials.json` → `~/.clawker-globals/.credentials.json`
4. Claude Code writes to the symlink target, persisting credentials to the global volume
5. Migration: existing per-agent credentials are copied to global volume before symlinking

### Key files
- `internal/docker/names.go` — `GlobalVolumeName(purpose)` → `clawker-<purpose>`
- `internal/docker/labels.go` — `GlobalVolumeLabels(purpose)` — managed + purpose only
- `internal/workspace/strategy.go` — `EnsureGlobalsVolume()`, `GetGlobalsVolumeMount()`
- `internal/workspace/setup.go` — wired into `SetupMounts()`
- `internal/bundler/assets/entrypoint.sh` — symlink creation + migration

## Keyring Package (`internal/keyring`)

The `keyring` package wraps `zalando/go-keyring` with timeouts and provides a
service-credential registry pattern for fetching, parsing, and validating OS
keychain secrets.

### File Layout
- `keyring.go` — Raw ops (Set/Get/Delete), ErrNotFound, TimeoutError, MockInit
- `service.go` — Generic pipeline: `ServiceDef[T]`, `getCredential[T]`, sentinels, helpers
- `claude_code.go` — `ClaudeCodeCredentials` types + `GetClaudeCodeCredentials()`
- `claude_code_test.go` — Table-driven tests (5 cases, all using MockInit)
- `CLAUDE.md` — Package docs

### Adding a New Service
Create `<service>.go` with struct + `ServiceDef[T]` var + public function.
No registration map — just one file per service.

### Error Types
- `ErrNotFound` — no keyring entry
- `ErrEmptyCredential` — entry exists but blank
- `ErrInvalidSchema` — data doesn't match struct
- `ErrTokenExpired` — credential past expiry
- `*TimeoutError` — keyring op timed out

**Status**: No production callers yet. Ready for integration.

## See also

* [Permissions](/en/permissions): configure what Claude Code can access and do
* [Settings](/en/settings): complete configuration reference
* [Security](/en/security): security safeguards and best practices
