# Session Status: User vs Project Init Feature

## Branch: `a/user-vs-project-init`

## STATUS: ALL IMPLEMENTATION COMPLETE ✅

All code changes are done. Tests pass. Ready for PR.

## What Was Implemented

### 1. Separated init commands:
- `clawker init` - User-level setup (creates `~/.local/clawker/settings.yaml`)
- `clawker project init` - Project-level setup (creates `clawker.yaml`)

### 2. Base image build feature in `clawker init`:
- Prompts to build initial base image
- Flavor selection (bookworm, trixie, alpine3.22, alpine3.23)
- Fetches latest Claude Code version from npm
- Builds `clawker-default:latest`
- Saves to settings

### 3. Default image resolution & validation:
- Changed resolution order: explicit → project → default
- Added validation for default images (prompts to rebuild if missing)
- Extracted shared build logic to `pkg/cmdutil/image_build.go`

### 4. IOStreams and Prompter:
- Created `pkg/cmdutil/iostreams.go` for TTY detection
- Enhanced `pkg/cmdutil/prompts.go` with interactive prompts
- Updated Factory with IOStreams support

## Key Files Modified
- `pkg/cmd/init/init.go` - User init with base image build
- `pkg/cmd/project/init/init.go` - Project init
- `pkg/cmdutil/image_build.go` - Shared build logic
- `pkg/cmdutil/resolve.go` - Image resolution with validation
- `pkg/cmd/container/run/run.go` - Uses ResolveAndValidateImage
- `pkg/cmd/container/create/create.go` - Uses ResolveAndValidateImage
- `.claude/docs/CLI-VERBS.md` - Documentation
- `README.md` - User docs updated

## Tests
All tests pass: `go test ./...`

## Next Steps
1. Final review of changes
2. Create PR to main branch
3. Consider manual testing of full flow