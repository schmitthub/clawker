# Serena Language Server Initialization Failure

## Problem

Serena fails with: "The language server manager is not initialized, indicating a problem during project activation."

## Root Cause

`.serena/project.yml` configures 4 languages: `go`, `markdown`, `bash`, `yaml`.

Serena starts all language servers in parallel. If **any** fails, it stops all of them and sets the manager to `None`.

- `go` → `gopls` — installed at `/home/claude/go/bin/gopls`, works fine
- `markdown` → `marksman` — binary auto-downloaded to `~/.serena/language_servers/static/Marksman/marksman`, works fine
- `bash` → `bash-language-server` — requires `npm install bash-language-server@5.6.0` — **npm/node NOT installed in container**
- `yaml` → `yaml-language-server` — requires `npm install yaml-language-server@1.19.2` — **npm/node NOT installed in container**

The bash and yaml LSP failures cause Serena to abort gopls too, breaking all semantic tooling.

## Fix Options

### Option A: Remove bash/yaml from Serena languages (minimal, recommended)

Edit `.serena/project.yml`:
```yaml
languages:
- go
- markdown
```

Pros: No new dependencies, gopls works immediately.
Cons: Lose bash/yaml symbol navigation (minor loss for a Go project).

### Option B: Install Node.js in clawker.yaml

Add to `build.packages`:
```yaml
- nodejs
- npm
```

And add to `security.firewall.add_domains`:
```
- registry.npmjs.org
```

Pros: All 4 language servers work.
Cons: Adds ~100MB+ to container image for marginal benefit on a Go project.

### Option C: Combination — trim languages AND document the Node.js requirement

Apply Option A now. Document in clawker.yaml comments that bash/yaml Serena languages require Node.js if someone wants to re-enable them.

## Resolution

**Option B implemented** on branch `a/container-fixes`: nodejs and npm added to `clawker.yaml` build.packages. `registry.npmjs.org` was already in `init-firewall.sh` hardcoded defaults — no firewall config change needed.

## Original Recommendation (superseded)

**Option A** was originally recommended but This is a Go project — gopls and marksman cover the important files. Bash and YAML files in this repo are simple configs/scripts that don't benefit meaningfully from LSP symbol analysis.

## Key Learnings

1. **Serena's all-or-nothing LSP init**: If any configured language server fails, the entire language server manager is set to `None`. This is in `serena/ls_manager.py` `LanguageServerManager.from_languages()`.
2. **npm-dependent LSP servers**: `bash-language-server` and `yaml-language-server` are npm packages that Serena auto-installs via `npm install --prefix`. This silently requires Node.js/npm in the environment.
3. **Zombie gopls processes**: When Serena's LSP init fails and stops all servers, the gopls process can become orphaned (ppid=1) with broken pipe connections. These zombies persist for the container lifetime.
4. **No visible error logs**: Serena's MCP server communicates via stdio, so LSP init errors aren't written to any accessible log file in the container. The only clue is the tool error message. Enable `trace_lsp_communication: true` in `serena_config.yml` for debugging.
5. **Firewall considerations**: Even with Node.js installed, `registry.npmjs.org` must be in the firewall allowlist for npm install to work. The marksman binary download from `github.com` also needs network access but was pre-cached.
