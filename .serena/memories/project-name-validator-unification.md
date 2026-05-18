# Project-Name Validator Unification — DELIVERED on `fix/invalid-name-len`

**Status:** delivered as part of the slugify simplification on `fix/invalid-name-len`. This memory documents the final design that landed; the original plan (length caps, dot ban as load-bearing, four divergent validators) is stale and the body below replaces it.

## What actually shipped

The cert SAN refactor that moved `AgentFullName` from x509 CN (64-byte cap) into a `urn:clawker:agent:` URI SAN already structurally fixed the original "long project name breaks `clawker run`" bug. Everything below cleans up validator drift, dead code, and unmotivated constraints layered around that fix.

### 1. New `cmdutil.ProjectSlugify` (single normalization point)

`internal/cmdutil/slugify.go`. Never errors. Steps:

- lowercase
- whitespace runs (incl. tab/newline) → single `-`
- control chars (`\x00-\x1F` not whitespace, plus `\x7F`) stripped so the result is safe for x509 URI SAN encoding
- leading/trailing `-` trimmed (Docker rejects names that don't start with an alnum)
- everything else (dots, underscores, unicode, etc.) passes through unchanged

The function is the only normalization layer. Downstream consumers (Docker, x509, gRPC `IdentityInterceptor`) enforce their own constraints and produce their own errors. No double-validation.

### 2. Auth validation gutted (`internal/auth/identity.go`)

- Deleted: `shortNameRE`, `shortNameMax`, `validateShortName`, `agentFullNamePrefix`, `MaxShortNameLen`.
- `NewProjectSlug(s)` returns `ProjectSlug{s: s}, nil` unconditionally. Empty input is the legitimate "projectless / unscoped agent" signal documented at `internal/consts/consts.go:39-40`.
- `NewAgentName(s)` keeps the empty-reject (`fmt.Errorf("agent name required")`) since `BuildAgentSAN` already errors on empty agent and this matches that contract; no charset/length check.
- `ProjectSlug` / `AgentName` are now compile-time discipline types only. The runtime invariant is "non-empty for AgentName" and nothing else.

### 3. Dead code removed (`internal/docker/names.go`)

- Deleted `ParseContainerName` — only its own unit test called it; no production caller. The "ban dots in project names" rule existed purely to keep that dead function's naive `strings.Split(name, ".")` parser happy. Production recovers `(project, agent)` from Docker labels (`consts.LabelProject`, `consts.LabelAgent`), never by name splitting.
- Deleted the 128-byte cap in `ValidateResourceName`. Verified against moby/moby and deepwiki: Docker has NO engine-level length cap on container, volume, or network names. The 63-char DNS-label limit only matters for container-name service discovery, which clawker does not use. The old comment claiming a "Docker 128-byte limit" was wrong.
- `ValidateResourceName` still checks Docker's `RestrictedNameChars` (`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`) and empty — useful pre-flight for callers composing names without going through a Docker create.

### 4. `clawker.yaml::project.name` override

- New `Project.Name string` field on `internal/config/schema.go` (`yaml:"name,omitempty"`).
- `internal/project/manager.go::CurrentProject` reads it after `ResolvePath` returns and replaces `record.Name` on the `*projectHandle`.
- CLI hierarchy: env (none) < `clawker.yaml::project.name` (file) < `--name` flag / positional arg (handled at init/register write path, persisted into the registry).

### 5. CLI wiring

- `internal/cmd/project/init/init.go`: replaced the local `projectNameRe` regex + `validateProjectName` + `strings.ToLower(dirName)` with `cmdutil.ProjectSlugify(...)`. Wizard `project_name` field has its validator dropped — slugify accepts anything, and the only handler-side check is "non-empty after normalization". The wizard's submitted value is slugified before being persisted/registered.
- `internal/cmd/project/register/register.go`: hierarchy is `opts.Name` > `cfg.Project().Name` > prompt/default. Every source flows through `cmdutil.ProjectSlugify`. If the slug resolves to empty (e.g. all-control-char input), register errors with an actionable message pointing to the clawker.yaml override.

### 6. Tests

- `internal/cmdutil/slugify_test.go` — corpus: `My App` → `my-app`, `foo.bar` → `foo.bar`, tabs/newlines collapse, control chars stripped, leading/trailing whitespace trimmed, unicode and emoji pass through.
- `internal/auth/identity_test.go` — minimal sanity tests; the old charset/length corpus is gone because there's no charset/length rule.
- `internal/docker/names_test.go` — `TestParseContainerName` and `TestContainerName_HeadroomForMaxFields` deleted; the 128-byte cap row in `TestValidateResourceName` updated to a long-but-valid case.
- `internal/controlplane/agent/register_handler_test.go::TestRegister_RequestValidation` — dropped the "invalid project chars" subcase (no longer rejected at the auth layer); kept the "empty agent_name" case.
- `internal/cmd/project/init/init_test.go::TestValidateProjectName` deleted (the function it tested is gone).

### 7. Docs

- `cmd/gen-docs/configuration.mdx.tmpl` gained a "Project name normalization" subsection under "Project Registration" describing the slugify behavior + the `project.name` override.
- `internal/docs/configdoc.go::buildSections` now renders top-level scalar fields (i.e. `Project.Name`) into their own reference table so the regenerated `docs/configuration.mdx` shows the new field.

## Decisions that did NOT make it (intentionally out of scope)

- **Projectless agent redesign.** `AgentFullName(ProjectSlug{}, agent)` → `"clawker.<agent>"` (2-segment) is a documented intentional feature for "I want to run clawker outside a registered project, just with my global config." The trust check is symmetric (cert SAN tail and label-derived AgentFullName both compose the 2-segment form) — not a bug. Stays.
- **Registry `(project, agent_name)` uniqueness.** Two projectless agents named `dev` on the same host CP both authenticate as identity `clawker.dev`. Their certs differ by container ID SAN, registry rows differ by `(thumbprint, container_id)` PK. Schema gap is real but doesn't break auth.
- **Agent-name slugify.** `--agent` is explicit user typing, not path-derived. `auth.NewAgentName`'s empty-reject is sufficient; Docker rejects anything else at op time. No `cmdutil.AgentSlugify` exists.
- **Legacy migration of pre-fix registry entries.** Don't worry about it. Users with `My App`-style entries can re-register or edit `clawker.yaml::project.name`.
- **Length budgets / DNS-label / composite math.** Verified moot — Docker has no engine-level length cap; we don't use container-name DNS service discovery.

## Files changed (final list)

- `internal/cmdutil/slugify.go` *(new)*
- `internal/cmdutil/slugify_test.go` *(new)*
- `internal/auth/identity.go` — gutted validators
- `internal/auth/identity_test.go` — rewritten as sanity tests
- `internal/auth/agent_cert_test.go` — replaced `MaxShortNameLen()` call with literal 100 in `TestMintAgentCert_MaxLengthRoundTrip`
- `internal/docker/names.go` — deleted `ParseContainerName`, deleted 128 cap, updated comments
- `internal/docker/names_test.go` — deleted `TestParseContainerName` and `TestContainerName_HeadroomForMaxFields`, updated cap row in `TestValidateResourceName`
- `internal/config/schema.go` — added `Project.Name` field
- `internal/project/manager.go` — `CurrentProject` honors `cfg.Project().Name`
- `internal/cmd/project/init/init.go` — slugify replaces validator, dropped wizard validator
- `internal/cmd/project/init/init_test.go` — deleted `TestValidateProjectName`
- `internal/cmd/project/register/register.go` — slugify + clawker.yaml override hierarchy
- `internal/controlplane/agent/register_handler_test.go` — pruned now-moot subcase
- `internal/docs/configdoc.go` — render top-level scalar field sections
- `cmd/gen-docs/configuration.mdx.tmpl` — added Project name normalization subsection
- `docs/configuration.mdx` — regenerated
- `docs/cli-reference/*` — regenerated (no surface change, just regen artifacts)

## Verification

- `go build -buildvcs=false ./...` clean.
- Unit test sweep (`go test` over the non-e2e package set, since clawker container env can't run e2e per CLAUDE.md guardrail) passes.
- `auth.NewProjectSlug("foo.bar")` succeeds; `auth.NewProjectSlug("My App")` succeeds. The slugify upstream produces `my-app` from `"My App"` before either ever sees it in a real CLI flow.
- `clawker.yaml::project.name` override surfaces in the regenerated `docs/configuration.mdx` reference table.
