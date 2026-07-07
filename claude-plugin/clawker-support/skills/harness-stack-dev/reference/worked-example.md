# Worked Example: a Minimal Custom Harness

A complete, fictional bundle for "pilot" — an imaginary coding-agent CLI
distributed as a GitHub release binary, configured via `PILOT_HOME`, with an
inference API at `api.pilot.example`. Adapt the pieces; every line follows
the validated vocabulary (`harness-manifest.md`, `template-blocks.md`).

## Layout

```
~/.config/clawker/harnesses/pilot/
├── harness.yaml
├── Dockerfile.harness.tmpl
└── assets/
    └── pilot-config.json
```

## harness.yaml

```yaml
# Pilot harness bundle — fictional example.

# Feeds {{.HarnessVersion}}: latest release tag of acme/pilot with the
# leading "v" stripped (v1.4.2 → 1.4.2).
version:
  resolver: github-release
  package: acme/pilot
  tag_prefix: v

# Pilot keeps all state under $PILOT_HOME. One persisted dir → the named
# volume clawker.<project>.<agent>-config mounted at ~/.pilot.
volumes:
  - name: config
    path: .pilot

# First-boot default config, applied into the volume by the generic init
# step. copy-if-missing: a user's existing config always wins.
seeds:
  - file: assets/pilot-config.json
    dest: .pilot/config.json
    apply: copy-if-missing

# Create-time copy of the user's GLOBAL pilot instructions from the host —
# state outside the workspace only (repo files arrive via the mount).
# Missing source skips silently.
staging:
  copy:
    - src: ${PILOT_HOME:-~/.pilot}/instructions.md
      dest: .pilot/instructions.md

# Runtime floor only. Install-time hosts (github.com release download)
# deliberately absent — docker build runs on the host daemon's network.
egress:
  - dst: api.pilot.example    # inference (observed live)
  - dst: auth.pilot.example   # in-container OAuth token exchange
```

## Dockerfile.harness.tmpl

```dockerfile
{{/*
  Pilot harness — fills the master template's block slots.
*/}}

{{define "block_3" -}}
# Tell pilot where its config lives — must match the config volume mount.
ENV PILOT_HOME=/home/${USERNAME}/.pilot
{{- end}}

{{define "block_4" -}}
# Build-only ARG, declared directly above its only consumer (BuildKit
# invalidates at the declaration line — adjacency scopes a version roll to
# this layer). Override: clawker build --build-arg PILOT_VERSION=<v>
ARG PILOT_VERSION={{.HarnessVersion}}
# Binary lands in ~/.local/bin — already on the image ENV PATH, so the CMD
# spawn (direct exec, no login shell) can find it.
RUN mkdir -p /home/${USERNAME}/.local/bin && \
    curl -fsSL "https://github.com/acme/pilot/releases/download/v${PILOT_VERSION}/pilot-linux-$(uname -m)" \
      -o /home/${USERNAME}/.local/bin/pilot && \
    chmod +x /home/${USERNAME}/.local/bin/pilot && \
    pilot --version
{{- end}}

{{define "block_6" -}}
CMD ["pilot"]
{{- end}}
```

(A real bundle should verify the download — a sha256 check against a
published checksum file, as the shipped stacks do.)

## assets/pilot-config.json

```json
{
  "telemetry": false
}
```

## Registration, build, run

```yaml
# In: <config-dir>/settings.yaml (user settings)
harnesses:
  pilot:
    path: /home/me/.config/clawker/harnesses/pilot
```

```bash
clawker build -t pilot
clawker run @:pilot
```

First run: pilot prompts its own login inside the container (browser OAuth
proxies to the host); the token persists in the `config` volume.

## Common adaptations

| Need | Change |
|---|---|
| npm-distributed CLI | `version: {resolver: npm, package: "@acme/pilot"}`; declare `stacks: [node]`; install in block_4 via the vendor installer or `npm install -g @acme/pilot@${PILOT_VERSION}` (nvm-owned npm prefix puts the binary on a shell-init path — verify the entry point lands in `~/.local/bin` or symlink it there; see the PATH gotcha) |
| No upstream versioning | `version: {resolver: none}` and ignore `{{.HarnessVersion}}` or treat it as a floating tag |
| Root-scope install | Put the install in block_1 or block_2 (root, /bin/sh) targeting `/usr/local/bin` |
| Installer writes into the volume path | Redirect the install-time home off the volume path (codex pattern in `template-blocks.md`) |
| Backend host also serves UGC | Path-scope the egress rule — allowlist or denylist mode per `security-egress.md` |
| Bespoke language runtime | Embed `stacks/pilot-runtime/` in the bundle (prefixed name; flat namespace) and declare it in the manifest |
