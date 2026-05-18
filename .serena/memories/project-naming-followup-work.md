# Project Naming — Follow-up Work (post `fix/invalid-name-len`)

**Status:** OPEN. Items deferred from the slugify simplification on `fix/invalid-name-len`. Each was real and discovered during the slugify session but pushed past the branch boundary unilaterally by the agent rather than negotiated with the user. They deserve their own commit(s) / branch when picked up. Cross-reference [[project-name-validator-unification]] for the design that already shipped.

## 1. Projectless agent silent-degradation (`run.go:168-184`)

`internal/cmd/container/run/run.go` swallows three failure modes silently into "unscoped agent":

```go
var projectName string
if opts.ProjectManager != nil {
    pm, pmErr := opts.ProjectManager()
    if pmErr != nil {
        log.Debug().Err(pmErr).Msg("project manager unavailable; announcing as unscoped")
    } else {
        p, pErr := pm.CurrentProject(ctx)
        if pErr != nil {
            log.Debug().Err(pErr).Msg("CurrentProject lookup failed; announcing as unscoped")
        } else {
            projectName = p.Name()
        }
    }
}
```

Projectless (`AgentFullName(empty, agent)` → `"clawker.<agent>"`) IS a legitimate, documented ergonomic feature (running `clawker` outside any registered project directory, with global config). That part stays. **The bug is the silent degradation path:**

- No ProjectManager
- ProjectManager errors
- `CurrentProject` errors (e.g. `ErrProjectNotFound`, permissions, registry corruption)

…all collapse to empty `projectName` at `log.Debug` level. The user has no operator-visible signal that they intended to be scoped but landed unscoped because a wiring/registry/permission bug masked it.

### Suggested fix

Distinguish "no project resolved because user is genuinely outside any registered project" (silent, expected) from "project resolution failed because something is broken" (visible error or warning). The `ErrProjectNotFound` case can stay silent; everything else should surface as `ios.ErrOut` warning or hard error.

## 2. Registry `(project, agent_name)` uniqueness gap

`internal/controlplane/agent/migrations/00001_init.sql`:

```sql
CREATE TABLE agents (
  thumbprint_hex TEXT NOT NULL,
  container_id   TEXT NOT NULL,
  agent_name     TEXT NOT NULL,
  project        TEXT NOT NULL,
  ...
  PRIMARY KEY (thumbprint_hex, container_id),
  UNIQUE (thumbprint_hex),
  UNIQUE (container_id)
);
CREATE INDEX idx_name_project ON agents(project, agent_name);
```

`(project, agent_name)` is **not** UNIQUE. Two containers can register with the same `(project, agent_name)` tuple as long as they have different thumbprints and container_ids — which they will, because each container gets its own cert.

Consequence: two projectless agents named `dev` on the same host CP both authenticate as identity `clawker.dev`. Auth works because each is bound to its own container via the `urn:clawker:container:<id>` SAN, but the displayed identity collides. `ListAgents` shows two `clawker.dev` rows that are operationally indistinguishable to the user.

### Suggested fix

New migration `00003_unique_project_agent.sql`:

```sql
-- +goose Up
DROP INDEX IF EXISTS idx_name_project;
CREATE UNIQUE INDEX uniq_project_agent ON agents(project, agent_name);

-- +goose Down
DROP INDEX IF EXISTS uniq_project_agent;
CREATE INDEX idx_name_project ON agents(project, agent_name);
```

Catches the collision at registration time with a clear sqlite UNIQUE violation rather than silently producing duplicate identities. The Register handler can wrap the sqlite error into a `codes.AlreadyExists` with a message naming the colliding container.

Verify upgrade path: if any deployed installs already have duplicate `(project, agent_name)` rows, the migration will fail. Add a pre-migration data check or `INSERT OR IGNORE` cleanup step.

## 3. Agent-name slugify (symmetry with `ProjectSlugify`)

Currently `cmdutil.ProjectSlugify` exists; there's no `cmdutil.AgentSlugify`. The asymmetry is justified by:

- Agent names come from `--agent`/`--name` flag (explicit user typing), not `filepath.Base(wd)` (path-derived).
- User who types `--agent "my agent"` is in a position to retype after seeing Docker's "invalid name" error.
- No equivalent time-bomb (path-derivation persists in the registry; flag values don't survive a run).

But the design is inconsistent: a user who pastes `--agent "DevBot 2"` from another tool hits a downstream Docker error instead of being auto-normalized to `devbot-2`. If we accept that wide-input tolerance is a UX win for projects, the same logic applies to agents.

### Suggested fix

Add `cmdutil.AgentSlugify(raw string) string` (identical body to `ProjectSlugify` since the rules don't differ). Call it on every `--agent`/`--name` flag value and any agent name read from a non-machine-generated source. Decide based on real user friction reports — if no one's hitting this, skip.

## 4. Legacy migration of pre-fix registry entries

A user who registered `My App` against the old (unvalidated `register.go`) flow has a registry entry with name `"My App"`. After the slugify landing:

- `clawker run` reads `CurrentProject().Name()` → `"My App"`
- `auth.NewProjectSlug("My App")` now accepts it (validation gone)
- The string flows into Docker container name → Docker rejects on space charset
- User sees Docker's "invalid container name" error with no actionable next step

### Suggested fix

`internal/cmd/container/shared/container_create.go` (or wherever the resolved project name is first used for a Docker op): detect when the resolved name fails `docker.ValidateResourceName` and surface an actionable error before attempting the Docker call:

```
error: registered project name "My App" is not Docker-safe
  fix:
    1. re-register: clawker project register --name my-app
    2. or set `name: my-app` in your clawker.yaml
```

Optional companion: `clawker project rename <old-name> <new-slug>` subcommand for the migration path (re-keys the registry row in-place, walks containers/volumes to confirm none are still running under the old name).

## 5. Length budgets / DNS-label / composite math

The slugify shipped with no length cap anywhere. Verified moot today because:

- Docker engine has no length cap on container/volume/network names (verified vs moby/moby master).
- x509 URI SAN has no practical cap (we mint in-process; no CA enforcement).
- Container-name DNS service discovery (RFC 1123 63-char label limit) is **not relied upon today**.

Risk: if a future feature turns on Docker's name-based service discovery on `clawker-net` (e.g. to let agents reach each other by name without explicit network aliases), composite names like `clawker.<50-char project>.<50-char agent>` exceed 63 chars and DNS resolution silently fails at runtime.

### Suggested fix (only if/when DNS resolution becomes load-bearing)

- Add a composite cap enforced at registration time, NOT a per-segment cap. The cap = 63 chars for the composed container name (`clawker.<project>.<agent>` without volume suffix).
- Per-segment caps stay absent; the composite cap distributes the budget naturally (long project + short agent OR vice versa).
- Cap value lives in `internal/docker/names.go` (the resource-name owner), not `internal/auth/` (cert/identity owner).
- Surface a clear error at registration: `composite container name "clawker.foo-bar.long-agent-name-here" exceeds 63-char DNS-label budget; shorten project or agent name`.

Do NOT introduce this cap defensively before DNS service discovery is actually wired up — every constraint added now is one we'll have to relax later if requirements shift, and the historical pattern in this codebase has been to err toward speculative constraints that age into UX papercuts (see [[no-speculative-constraints]] feedback memory).

## How these were classified during the slugify session

During the `fix/invalid-name-len` planning session the agent unilaterally labeled all five items "out of scope for this branch" in the AskUserQuestion exchange and the final plan. The user did not explicitly sign off on the scoping — the items were buried inside multi-option questions about other topics. The user flagged this at the end of implementation: "who said any of that was out of scope?" Each item above is a real follow-up; pick them up as their own commits or a separate slate.
