# Release Automation Implementation Guide

## Related Plan File

**Location:** `/Users/andrew/.claude/plans/floating-swinging-key.md`

This plan file contains the executive summary, research findings, and high-level overview. **Do not read it now** - just remember it exists if you need additional context about the research or rationale behind the implementation choices.

---

## Worker Kickoff Prompt

**Copy this prompt to start a new Claude Code session for implementation:**

```
I am implementing the clawker release automation pipeline. Read the Serena memory `release_automation_implementation` for full context and task breakdown.

My assigned task is: [TASK_NUMBER] - [TASK_NAME]

Follow the instructions in the memory exactly. When you complete this specific task successfully, output:
<promise>DONE</promise>

If you encounter blockers, document them clearly and do NOT output the DONE promise.
```

---

## How to Interpret This Memory

1. **Tasks are sequential** - Complete Task 1 before Task 2, etc.
2. **Each task should be completed in a separate session** - Stop after each task
3. **Verification is mandatory** - Each task has acceptance criteria that must pass
4. **Output `<promise>DONE</promise>` only when ALL acceptance criteria pass**
5. **Document blockers** - If you cannot complete a task, explain why clearly

---

## Project Context

**Repository:** github.com/schmitthub/clawker  
**Language:** Go  
**Build System:** Makefile + GoReleaser (to be added)  
**Current Version Variables:**
- `internal/clawker/cmd.go:10` - `Version = "dev"`
- `internal/clawker/cmd.go:11` - `Commit = "none"`

**Goal:** When main branch is tagged with semver (e.g., `v0.1.0-alpha`), automatically:
1. Build clawker for linux/darwin × amd64/arm64
2. Create GitHub Release with checksums and auto-generated notes

---

## Task 1: Create GoReleaser Configuration

**Assigned Worker Session:** 1  
**Estimated Complexity:** Low

### Instructions

1. Create file `.goreleaser.yaml` in project root with this exact content:

```yaml
version: 2

project_name: clawker

before:
  hooks:
    - go mod tidy
    - go generate ./...

builds:
  - id: clawker
    main: ./cmd/clawker
    binary: clawker
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X github.com/schmitthub/clawker/internal/clawker.Version={{.Version}}
      - -X github.com/schmitthub/clawker/internal/clawker.Commit={{.ShortCommit}}

archives:
  - id: default
    formats:
      - tar.gz
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files:
      - LICENSE
      - README.md

checksum:
  name_template: "checksums.txt"
  algorithm: sha256

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^ci:"
      - "^chore:"
      - Merge pull request
      - Merge branch

release:
  github:
    owner: schmitthub
    name: clawker
  prerelease: auto
  name_template: "{{.ProjectName}} v{{.Version}}"
```

2. Validate the configuration:
```bash
# Install goreleaser if not present
go install github.com/goreleaser/goreleaser/v2@latest

# Validate config
goreleaser check
```

3. Test locally with snapshot mode:
```bash
goreleaser release --snapshot --clean
```

### Acceptance Criteria

- [ ] `.goreleaser.yaml` exists in project root
- [ ] `goreleaser check` outputs "config is valid"
- [ ] `goreleaser release --snapshot --clean` completes without errors
- [ ] `dist/` directory contains 4 archives:
  - `clawker_*_linux_amd64.tar.gz`
  - `clawker_*_linux_arm64.tar.gz`
  - `clawker_*_darwin_amd64.tar.gz`
  - `clawker_*_darwin_arm64.tar.gz`
- [ ] `dist/checksums.txt` exists

**When ALL criteria pass, output:** `<promise>DONE</promise>`

---

## Task 2: Create GitHub Actions Release Workflow

**Assigned Worker Session:** 2  
**Estimated Complexity:** Low  
**Depends On:** Task 1

### Instructions

1. Create directory `.github/workflows/` if it doesn't exist

2. Create file `.github/workflows/release.yml` with this exact content:

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  release:
    name: Release
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Run tests
        run: go test ./...

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

3. Verify the workflow syntax:
```bash
# Check YAML syntax
cat .github/workflows/release.yml | python3 -c "import yaml, sys; yaml.safe_load(sys.stdin); print('YAML valid')"
```

### Acceptance Criteria

- [ ] `.github/workflows/release.yml` exists
- [ ] YAML syntax is valid
- [ ] Workflow triggers on `push: tags: ["v*"]`
- [ ] Workflow has `permissions: contents: write`
- [ ] Workflow uses `actions/checkout@v4` with `fetch-depth: 0`
- [ ] Workflow uses `actions/setup-go@v5` with `go-version-file: go.mod`
- [ ] Workflow runs tests before release
- [ ] Workflow uses `goreleaser/goreleaser-action@v6`

**When ALL criteria pass, output:** `<promise>DONE</promise>`

---

## Task 3: Test Release Flow with Test Tag

**Assigned Worker Session:** 3  
**Estimated Complexity:** Medium  
**Depends On:** Tasks 1, 2

### Instructions

1. Ensure all changes from Tasks 1-2 are committed and pushed to main:
```bash
git status
git add .goreleaser.yaml .github/workflows/release.yml
git commit -m "ci: add GoReleaser configuration and release workflow"
git push origin main
```

2. Create and push a test tag:
```bash
git tag -a v0.0.1-test -m "Test release automation"
git push origin v0.0.1-test
```

3. Monitor the GitHub Actions workflow:
   - Go to: https://github.com/schmitthub/clawker/actions
   - Watch the "Release" workflow run
   - Wait for completion

4. Verify the release was created:
   - Go to: https://github.com/schmitthub/clawker/releases
   - Find "clawker v0.0.1-test"
   - Verify it's marked as "Pre-release"

5. Cleanup test release and tag:
```bash
# Delete remote tag
git push origin --delete v0.0.1-test

# Delete local tag
git tag -d v0.0.1-test
```

6. Delete the test release from GitHub Releases page (manual step)

### Acceptance Criteria

- [ ] GitHub Actions workflow triggered on tag push
- [ ] Workflow completed successfully (green checkmark)
- [ ] Release created at github.com/schmitthub/clawker/releases
- [ ] Release marked as "Pre-release" (due to `-test` suffix)
- [ ] Release contains 4 binary archives
- [ ] Release contains checksums.txt
- [ ] Release has auto-generated changelog
- [ ] Test tag and release cleaned up

**When ALL criteria pass, output:** `<promise>DONE</promise>`

---

## Task 4: Update Documentation

**Assigned Worker Session:** 4  
**Estimated Complexity:** Low  
**Depends On:** Task 3

### Instructions

1. Update `README.md` to add installation from releases section. Add after "Quick Start" section:

```markdown
## Installation

### From Releases (Recommended)

Download the latest release for your platform from [GitHub Releases](https://github.com/schmitthub/clawker/releases).

```bash
# macOS (Apple Silicon)
curl -LO https://github.com/schmitthub/clawker/releases/latest/download/clawker_*_darwin_arm64.tar.gz
tar -xzf clawker_*_darwin_arm64.tar.gz
sudo mv clawker /usr/local/bin/

# macOS (Intel)
curl -LO https://github.com/schmitthub/clawker/releases/latest/download/clawker_*_darwin_amd64.tar.gz
tar -xzf clawker_*_darwin_amd64.tar.gz
sudo mv clawker /usr/local/bin/

# Linux (x86_64)
curl -LO https://github.com/schmitthub/clawker/releases/latest/download/clawker_*_linux_amd64.tar.gz
tar -xzf clawker_*_linux_amd64.tar.gz
sudo mv clawker /usr/local/bin/

# Linux (ARM64)
curl -LO https://github.com/schmitthub/clawker/releases/latest/download/clawker_*_linux_arm64.tar.gz
tar -xzf clawker_*_linux_arm64.tar.gz
sudo mv clawker /usr/local/bin/
```

### From Source

```bash
git clone https://github.com/schmitthub/clawker.git
cd clawker && go build -o ./bin/clawker ./cmd/clawker
export PATH="$PWD/bin:$PATH"
```
```

2. Update `CLAUDE.md` to add release process documentation. Add to "Build Commands" section:

```markdown
## Release Process

Releases are automated via GitHub Actions when tags are pushed:

```bash
# Create a release
git tag -a v0.1.0 -m "Release description"
git push origin v0.1.0

# Create a prerelease (alpha, beta, rc)
git tag -a v0.2.0-alpha -m "Alpha release"
git push origin v0.2.0-alpha

# Test locally before tagging
goreleaser release --snapshot --clean
```

**Tag format:** `v<MAJOR>.<MINOR>.<PATCH>[-<PRERELEASE>]`
- `v0.1.0` - Full release
- `v0.1.0-alpha` - Prerelease (auto-detected)
- `v0.1.0-beta.1` - Prerelease with build number
- `v0.1.0-rc.1` - Release candidate

**Artifacts generated:**
- `clawker_VERSION_linux_amd64.tar.gz`
- `clawker_VERSION_linux_arm64.tar.gz`
- `clawker_VERSION_darwin_amd64.tar.gz`
- `clawker_VERSION_darwin_arm64.tar.gz`
- `checksums.txt`
```

3. Commit documentation updates:
```bash
git add README.md CLAUDE.md
git commit -m "docs: add release installation and process documentation"
git push origin main
```

### Acceptance Criteria

- [ ] README.md contains installation instructions from releases
- [ ] README.md contains installation instructions from source
- [ ] CLAUDE.md contains release process documentation
- [ ] CLAUDE.md documents tag format and prerelease detection
- [ ] Documentation changes committed and pushed

**When ALL criteria pass, output:** `<promise>DONE</promise>`

---

## Task 5: Create First Official Release

**Assigned Worker Session:** 5  
**Estimated Complexity:** Low  
**Depends On:** Task 4

### Instructions

1. Verify all previous tasks are complete and main branch is up to date:
```bash
git pull origin main
git log --oneline -5
```

2. Review recent commits for changelog:
```bash
git log --oneline $(git describe --tags --abbrev=0 2>/dev/null || echo HEAD~20)..HEAD
```

3. Create the first official alpha release:
```bash
git tag -a v0.1.0-alpha -m "First alpha release

Features:
- Docker container isolation for Claude Code
- Multi-agent support with named containers
- Host proxy for OAuth and git credential forwarding
- Bind and snapshot workspace modes
- Optional monitoring stack (Prometheus + Grafana)
- Network firewall for security"

git push origin v0.1.0-alpha
```

4. Wait for GitHub Actions to complete and verify release

### Acceptance Criteria

- [ ] Tag v0.1.0-alpha created and pushed
- [ ] GitHub Actions workflow completed successfully
- [ ] Release page shows v0.1.0-alpha as Pre-release
- [ ] All 4 platform binaries available for download
- [ ] Checksums.txt included in release
- [ ] Release notes auto-generated

**When ALL criteria pass, output:** `<promise>DONE</promise>`

---

## Summary of Files to Create/Modify

| Task | File | Action |
|------|------|--------|
| 1 | `.goreleaser.yaml` | Create |
| 2 | `.github/workflows/release.yml` | Create |
| 4 | `README.md` | Modify (add installation section) |
| 4 | `CLAUDE.md` | Modify (add release process) |

---

## Troubleshooting

### GoReleaser check fails
- Ensure GoReleaser v2 is installed: `go install github.com/goreleaser/goreleaser/v2@latest`
- Check YAML indentation (use spaces, not tabs)

### GitHub Actions workflow doesn't trigger
- Ensure tag starts with `v` (e.g., `v0.1.0`)
- Ensure tag is pushed: `git push origin <tag>`
- Check workflow file is in `.github/workflows/`

### Release not created
- Check workflow permissions: `permissions: contents: write`
- Verify GITHUB_TOKEN is available (automatic in Actions)
- Check GoReleaser logs in Actions for errors

### Prerelease not detected
- Ensure tag contains `-` before suffix (e.g., `v0.1.0-alpha`)
- GoReleaser auto-detects: `-alpha`, `-beta`, `-rc`, `-dev`, `-test`
