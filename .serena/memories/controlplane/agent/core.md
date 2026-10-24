# Agent identity and sessions

- `controlplane/agent/` owns the registry, agent repository, dialer, executor, and AgentService identity checks.
- `controlplane/agent/dialer.go` exposes `NewDialer`; `exec.go` exposes `NewExecutor`.
- CP dials clawkerd and uses its bidirectional Session stream for commands. Registration uses clawkerd's outbound AgentService call when CP requests it.

## Separate trust rules

- The clawkerd listener is strict: CA chain, client-auth EKU, and CP common-name pin are checked at TLS.
- The CP dialer must remain permissive about the peer certificate and identity. CP needs the connection to issue containment commands.
- Dialer trust results are fields on `SessionConnected` events. Event consumers apply policy; the dialer must not add identity-based connection rejection.
- The AgentService identity interceptor uses the peer IP to find the container, reads its labels, and checks the certificate identity against that independent source.
- A missing project label is a valid global agent. A missing or malformed agent label is not.
- Registry rows bind the certificate thumbprint to the container ID. Do not use certificate claims alone as container identity.
- `AgentService.Export` is the agent telemetry lane (public scope; clawkerd cannot mint a second bearer). `Handler.Export` adds the registry thumbprint check, then forwards through `controlplane/ingest.Gateway`, which overwrites `project`/`agent`/`container_id`/`ingest_source` with CP-resolved values and pushes to the collector's `otlp/agents` receiver with the `cp` infra leaf. The gateway degrades to nil (`event=agent_ingest_unavailable`) and carries a breaker because monitoring is optional (3 failures → silent drop, probe each minute, auto-recover). `agent` imports `ingest`; `ingest` never imports `agent`.
- Keep declared registry data, Docker observations, and session reports distinct.

## Startup and proof

- The executor drives initialization and boot over Session. `AgentInitialized` records initialization; `AgentReady` permits the user CMD to start.
- Constructor or dispatch failures must preserve the CP failure rules in `mem:controlplane/core`.
- Listener and child-process details: `mem:runtime/core`.
- Source contracts: `controlplane/agent/AGENTS.md`, `clawkerd/AGENTS.md`.
- Prior AgentService adversarial-test proposal: `mem:plans/agent-service-adversarial-tests`. Check the present RPC surface before reuse.
