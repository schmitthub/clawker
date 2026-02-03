# Fix: Container Terminal Colors Limited to 8 Colors

## Status: Investigation Complete — Ready for PR

## Problem

Claude Code terminal colors are degraded inside clawker containers. Only 8 colors are available instead of 256 (or truecolor). This affects syntax highlighting, TUI rendering, prompt themes, and the overall developer experience.

## Root Cause Analysis

Three issues combine to create the problem:

### 1. `TERM=xterm` hardcoded by zsh-in-docker (Primary Cause)

The `zsh-in-docker.sh` script (v1.2.0) writes `export TERM=xterm` into `~/.zshrc`. The `xterm` terminfo entry only declares **8 colors** (`colors#8`), while `xterm-256color` declares **256 colors** (`colors#0x100`). The `xterm-256color` terminfo entry **does exist** on the container filesystem at `/lib/terminfo/x/xterm-256color`, but is never used.

**Location:** `Dockerfile.tmpl:270-287` — the `zsh-in-docker` invocation has no option to override this.

### 2. No `TERM` or `COLORTERM` set in Dockerfile or entrypoint

Neither the Dockerfile template (`internal/bundler/assets/Dockerfile.tmpl`) nor the entrypoint script (`internal/bundler/assets/entrypoint.sh`) set `TERM` or `COLORTERM`. The Dockerfile only sets `ENV SHELL`, `ENV PATH`, and `ENV BROWSER`.

The `credentials.DefaultPassthrough()` list includes both `TERM` and `COLORTERM` for host-to-container passthrough, but:
- Docker's `TERM` passthrough typically provides `xterm` unless the host explicitly sends something better
- Even if the host sends `TERM=xterm-256color`, the `.zshrc` line from zsh-in-docker **overrides it** with `export TERM=xterm`
- `COLORTERM` is rarely set by the host Docker client

### 3. Locale not activated at runtime

While `locale-gen en_US.UTF-8` runs during build and `.zshrc` sets `LANG=en_US.UTF-8`, at runtime the actual locale reports `POSIX` for all categories. The `LANG` env var from `.zshrc` only takes effect in interactive zsh sessions, not in the entrypoint or non-zsh processes (like Claude Code's node process).

## Evidence

```
# Inside container:
$ echo $TERM
xterm

$ tput colors
8

$ infocmp xterm | grep colors
colors#8

$ infocmp xterm-256color | grep colors
colors#0x100    # 256 colors — available but unused!

$ locale
LANG=           # empty at runtime outside zsh
LC_ALL=         # empty

$ grep TERM ~/.zshrc
export TERM=xterm   # hardcoded by zsh-in-docker
```

## Recommendations

### Fix 1: Set `TERM=xterm-256color` in Dockerfile ENV (High Priority)

Add to `Dockerfile.tmpl` alongside the existing ENV block (around line 254):

```dockerfile
ENV TERM=xterm-256color
```

This sets the default for all processes in the container. It will be overridden by any explicit `-e TERM=...` at container creation, preserving user control. It takes effect before `.zshrc` runs but `.zshrc` will still override it — so Fix 2 is also needed.

### Fix 2: Override zsh-in-docker's TERM in .zshrc (High Priority)

Add `-a "export TERM=xterm-256color"` to the zsh-in-docker invocation in `Dockerfile.tmpl`, or better yet, add a subsequent line in the Dockerfile that patches `.zshrc`:

```dockerfile
RUN sed -i 's/^export TERM=xterm$/export TERM=xterm-256color/' ~/.zshrc
```

This is more robust than relying on `-a` ordering since zsh-in-docker places its `export TERM=xterm` early in `.zshrc`.

### Fix 3: Set `COLORTERM=truecolor` in Dockerfile ENV (Medium Priority)

```dockerfile
ENV COLORTERM=truecolor
```

Modern terminals (iTerm2, Windows Terminal, Ghostty, Alacritty, kitty) all support 24-bit truecolor. `COLORTERM=truecolor` tells applications they can use full RGB colors. This is standard practice in container images targeting developer tooling. Claude Code and many CLI tools check this variable.

### Fix 4: Set `LANG=en_US.UTF-8` in Dockerfile ENV (Medium Priority)

```dockerfile
ENV LANG=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8
```

Currently `LANG` is only set inside `.zshrc`, meaning non-zsh processes (node, python, etc.) run with `POSIX` locale. This can cause Unicode rendering issues in TUI applications.

### Fix 5: Pass `TERM` and `COLORTERM` from host at container creation (Low Priority — Enhancement)

The `credentials.DefaultPassthrough()` already lists `TERM` and `COLORTERM`. Verify that `RuntimeEnv()` or the container creation path actually applies these. Currently `RuntimeEnv()` in `internal/docker/env.go` only handles editor, firewall, agent env, and instruction env — it does **not** call `DefaultPassthrough()` or `SetFromHostAll()`. The passthrough list appears unused in the container creation flow.

If host passthrough is wired up, it should take precedence over the Dockerfile ENV defaults but the `.zshrc` override from zsh-in-docker would still win in interactive shells (hence Fix 2 remains essential).

## Files to Modify

| File | Change |
|------|--------|
| `internal/bundler/assets/Dockerfile.tmpl` | Add `ENV TERM=xterm-256color`, `ENV COLORTERM=truecolor`, `ENV LANG=en_US.UTF-8` |
| `internal/bundler/assets/Dockerfile.tmpl` | Add `sed` to fix zsh-in-docker's TERM override in `.zshrc` |
| `internal/bundler/hash_test.go` | Update content hash golden values (Dockerfile content changes) |

## Impact

- All new container builds will have correct 256-color (and truecolor) support
- Existing containers need rebuild (`clawker build --force`)
- No runtime behavior changes for users who explicitly set `TERM` via `-e` flag
- Content hash will change (expected — Dockerfile structural change)
