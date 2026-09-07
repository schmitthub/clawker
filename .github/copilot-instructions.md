# Copilot code review instructions

You review pull requests for this repository. `AGENTS.md` at the repository
root and the rule files in `.claude/rules/` are the coding standard. This file
tells you what to look for first and how to write each comment.

Read the head branch of the pull request. Compare every added or changed line
against the priorities below, in order.

## Priority 1: lint suppression

Flag every added or changed lint suppression. One comment per occurrence.

Markers to find in the diff:

- `//nolint` and `// nolint` (with or without a linter list)
- `#nosec`
- `//lint:ignore`
- Changes to `exclusions`, `exclude`, or `nolintlint` settings in `.golangci.yml`

The repository rule (`AGENTS.md`, section "Error Handling") is: do not add a
lint suppression without explicit maintainer approval. Correct the code so the
linter passes. An explanation text after the marker is not sufficient on its
own. In each comment:

1. Quote the marker and the linter it silences.
2. State that the rule requires maintainer approval.
3. Propose the code change that removes the need for the suppression.

## Priority 2: control plane servers and listeners

Applies to new or changed calls to `grpc.NewServer`, `net.Listen`,
`net.ListenConfig`, `http.Server`, and `ListenAndServe` in these paths:

- `controlplane/**`
- `internal/controlplane/**`
- `cmd/clawkercp/**`
- `clawkerd/**`

The canonical authorization code is `controlplane/auth`:

- `auth.NewAuthInterceptor` builds the interceptor from a Hydra introspector
  and a method-to-scope map.
- `AuthInterceptor.UnaryInterceptor`, `AuthInterceptor.StreamInterceptor`, and
  `AuthInterceptor.GRPCServerOptions` attach it to a gRPC server.
- `controlplane/server/grpc_stack.go` shows the required chain order:
  recovery interceptor, then ready-gate interceptor, then auth interceptor.

Report each of these as a blocking finding:

- **No auth.** A gRPC server on the admin or agent surface without the
  `controlplane/auth` interceptor chain. A hand-written token check, a
  header check, or a copy of the interceptor logic is not acceptable; the
  server must use `controlplane/auth`.
- **Reused auth material.** A listener that loads certificate, key, or CA
  files that belong to another lane, or that shares a `tls.Config` with
  another lane. Each listener has its own certificate lane in its own
  subpackage (`controlplane/infracerts`, `controlplane/otelcerts`,
  `controlplane/sdscerts` are the pattern). See "Why not otelcerts" in
  `controlplane/sdscerts/AGENTS.md`.
- **mTLS without a pin.** A listener that uses client certificates instead of
  bearer tokens must set `ClientAuth: tls.RequireAndVerifyClientCert` and pin
  the peer CN or SAN in `VerifyPeerCertificate`. `clawkerd/listener.go` is the
  reference. The pull request description must say why bearer authorization
  does not apply to that listener.
- **Crash path.** `panic`, `log.Fatal`, or `os.Exit` on the control plane
  boot or serve path, or a serve goroutine without a `recover`. See
  "CP crashing is a SECURITY incident" in `AGENTS.md`. A degraded subsystem
  must emit a structured log line with `event=<subsystem>_unavailable`.

## Priority 3: `CLAUDE.md` files

Every `CLAUDE.md` in this repository is a symbolic link to the file
`AGENTS.md` in the same directory. Git submodules are exempt.

For each added or changed `CLAUDE.md` in the diff, check the file mode and
the blob content. A symbolic link has mode `120000`, and its blob content is
the link target string. Report each of these as a blocking finding:

- A regular file named `CLAUDE.md` (mode `100644` or `100755`).
- A link whose target is an absolute path.
- A link whose target is not exactly `AGENTS.md` (for example
  `../AGENTS.md`, `docs/AGENTS.md`, or `./AGENTS.md`).
- A link whose sibling `AGENTS.md` does not exist in that directory.

The fix is always the same: move the content to the sibling `AGENTS.md` and
replace `CLAUDE.md` with a relative link to it.

## Priority 4: standing rules from `AGENTS.md`

Flag these when they appear in the diff. Name the `AGENTS.md` section in the
comment.

- An error assigned to `_` or otherwise not handled.
- A meaningful string literal that is not a named constant.
- A struct literal that omits required fields (the `exhaustruct` linter).
- A dependency, container image, GitHub Action, or hook without an exact
  version and digest or commit SHA.
- A `context.Context` stored in a struct field.
- An import of `github.com/moby/moby/client` outside `pkg/whail`, of
  `pkg/whail` outside `internal/docker`, of `golang.org/x/term` outside
  `internal/term`, of `lipgloss` outside `internal/iostreams`, or of
  `bubbletea` outside `internal/tui`.

## Do not report

- Formatting that `gofmt`, `golines`, or import ordering already enforce.
- Word choice in code comments.
- A finding that a CI check on the same pull request already reports.

## Comment style

- Write in plain, direct English. One sentence, one idea.
- One finding per comment. Anchor the comment to the line.
- Name the rule file or `AGENTS.md` section the finding comes from.
- Give the fix, not only the problem.

## Rule files by path

Read the matching rule file before you review a change in that path. This
table mirrors the `paths:` front matter in each rule file.

| Path | Rule file |
| --- | --- |
| `**/*.go` | `.claude/rules/testing.md` |
| `internal/**`, `cmd/**` | `.claude/rules/dependency-placement.md` |
| `internal/cmd/container/**` | `.claude/rules/container-commands.md` |
| `internal/docker/**`, `pkg/whail/**` | `.claude/rules/docker-client.md` |
| `controlplane/firewall/envoy_*.go` | `.claude/rules/envoy.md` |
| `internal/git/**` | `.claude/rules/git.md` |
| `internal/hostproxy/**` | `.claude/rules/hostproxy.md` |
| `internal/iostreams/**`, `internal/cmd/**` | `.claude/rules/iostreams.md` |
| `docs/**` | `.claude/rules/mintlify-docs.md` |
| `internal/monitor/**`, `cmd/coredns-clawker/plugins/otel/**`, `controlplane/firewall/ebpf/netlogger/**`, `internal/consts/monitoring.go` | `.claude/rules/monitoring.md` |
| `internal/storage/**`, `internal/config/schema*`, `internal/config/defaults*` | `.claude/rules/storage-schema.md` |
| `internal/state/**`, `internal/config/**`, `internal/project/**`, `internal/storage/**`, `internal/storeui/**`, `controlplane/firewall/**` | `.claude/rules/store-backed-package.md` |
| `internal/storeui/**`, `internal/config/storeui/**`, `internal/tui/fieldbrowser*`, `internal/tui/listeditor*`, `internal/tui/textareaeditor*` | `.claude/rules/storeui.md` |
| `internal/tui/**` | `.claude/rules/tui.md` |

`.claude/rules/code-style.md` and `.claude/rules/firewall-uat.md` apply to
every path.
