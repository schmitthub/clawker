---
name: code-review
description: Review a pull request in this repository. Use this when asked to review a pull request, a diff, or a branch.
---

Follow these steps in order. `.github/copilot-instructions.md` defines each
priority in full and the comment style; this file is the procedure.

## 1. Lint suppression

Search every added or changed line in the diff for `//nolint`, `// nolint`,
`#nosec`, and `//lint:ignore`. Search `.golangci.yml` changes for
`exclusions`, `exclude`, and `nolintlint`. List every hit as `file:line`.
Report each hit as its own comment (priority 1).

## 2. Control plane servers and listeners

Search the diff under `controlplane/`, `internal/controlplane/`,
`cmd/clawkercp/`, and `clawkerd/` for `grpc.NewServer`, `net.Listen`,
`net.ListenConfig`, `http.Server`, and `ListenAndServe`. For each hit:

1. Confirm the gRPC server attaches the `controlplane/auth` interceptor in
   the chain order used by `controlplane/server/grpc_stack.go`.
2. Confirm the certificate, key, and CA files come from a lane that belongs
   to this listener only. Compare against `controlplane/infracerts`,
   `controlplane/otelcerts`, and `controlplane/sdscerts`.
3. If the listener uses client certificates instead of bearer tokens, confirm
   `tls.RequireAndVerifyClientCert` and a CN or SAN pin in
   `VerifyPeerCertificate`, and confirm the pull request description gives
   the reason.
4. Confirm there is no `panic`, `log.Fatal`, or `os.Exit`, and that each
   serve goroutine has a `recover`.

Report each failed check as a blocking comment (priority 2).

## 3. `CLAUDE.md` files

For each `CLAUDE.md` in the diff, read the file mode and the blob content.
Pass only when the mode is `120000` and the content is exactly `AGENTS.md`,
and a file named `AGENTS.md` exists in the same directory. Git submodules are
exempt. Report each failure as a blocking comment (priority 3).

## 4. Version pins

For each added `FROM` line, `uses:` line, `rev:` line, or image constant in
Go source, confirm it carries a `@sha256:` digest or a full commit SHA.
Report each miss (priority 4).

## 5. Standing rules

Apply the remaining priority 4 rules and the rule files that
`.github/copilot-instructions.md` names.

## 6. Report

Write each comment as `.github/copilot-instructions.md` describes: one
finding per comment, the rule source named, the fix given.
