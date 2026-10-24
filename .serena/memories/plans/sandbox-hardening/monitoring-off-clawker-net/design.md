# Item 1: Monitoring stack off clawker-net (ruled 2026-10-24)

Part of `mem:plans/sandbox-hardening/initiative` (Task 6). Depends on the gateway in `mem:plans/sandbox-hardening/cp-otlp-gateway/design` being live, otherwise agent telemetry has no path.

## Problem

Every monitoring service (otel-collector, OpenSearch, Prometheus, Dashboards, bootstrap) joins `clawker-net`. The eBPF `is_in_subnet` early return never filters intra-subnet traffic, and the services carry no authentication. From an agent: OpenSearch index create/delete worked; the `claude-code` index holds `prompt_text`, `user.email`, and tool results; `clawker-envoy` and `clawker-ebpf-egress` are the audit trail. Legacy design: agents once used the Docker network as an infra net before the control plane existed.

## Ruling

- Split is host network vs clawker-net. Monitoring stack lives only on its own compose network with ports published on `127.0.0.1`. No monitoring container joins `clawker-net`. No separate "monitoring network" shared with CP.
- Envoy is external egress only. Agents reach nothing on clawker-net except Envoy, CoreDNS, and the CP agent ports (`mem:plans/sandbox-hardening/ebpf-intra-subnet-deny/design`).
- All trusted senders (CP, Envoy, CoreDNS) and the CP gateway reach the collector at `host.docker.internal:<port>`. CP already does (`controlplane/manager/cp_container.go` `ExtraHosts host.docker.internal:host-gateway`, endpoint `https://host.docker.internal:<OtelInfraPort>`).

## Changes

- `internal/monitor/templates/compose.yaml.tmpl`: drop every `networks: [clawker-net]` and the `networks.clawker-net.external` block; services use the compose default network. Keep `127.0.0.1` port publishes: OTel gRPC, OTel HTTP (until removed), OtelInfraPort, the new agent-ingest port, Prometheus, OpenSearch, Dashboards. Remove the comment that agents push via clawker-net DNS.
- `otel-config.yaml.tmpl`: remove the untrusted `otlp` receiver (4317/4318) and the `resource/untrusted_otlp` pipelines; CLI telemetry on the host loopback keeps whatever receiver it uses today (verify the CLI path before deleting a receiver it depends on). The Prometheus exporter stays on the compose network for the Prometheus scrape.
- `prometheus.yaml.tmpl`: scrape target `otel-collector:<PrometheusMetricsPort>` resolves on the compose network, unchanged name, confirm no clawker-net assumption in comments.
- `internal/consts/monitoring.go`: delete `MonitoringServiceHostnames` and its CoreDNS rewrite consumers; `controlplane/firewall/coredns_config.go` drops the monitoring forward zone and its tests. `ServiceURL`/`OtelCollectorURL` stop being used for the agent endpoint (Task 5 already redirected it); remove dead accessors.
- `controlplane/firewall/stack.go`: Envoy ALS cluster (`otel_collector_als`) and `CLAWKER_COREDNS_OTEL_ENDPOINT` target `host.docker.internal:<OtelInfraPort>`. Envoy and CoreDNS container configs need `ExtraHosts: host.docker.internal:host-gateway` the same way CP has it. The infra receiver TLS server cert must carry `host.docker.internal` as a SAN; check `AuthOtelServerCertPath` material and `monitor init` (the CP dial already works against it, so it likely does; verify, do not assume).
- `monitor init`/`up` precondition: the stack no longer requires `clawker-net` to exist. Remove any "created by clawker start/run" dependency in `internal/monitor`.
- Docs: `docs/` monitoring pages, `docs/threat-model.mdx`, and the managed agent prompt telemetry section (`internal/bundler/assets/clawker-agent-prompt.md`): the collector is host-side only; agents never resolve monitoring hostnames.

## Checks

- Compose template test: no service lists `clawker-net`; every `ports` entry starts with `127.0.0.1:`.
- CoreDNS config golden: no monitoring hostnames.
- Stack golden: ALS cluster and CoreDNS endpoint use `host.docker.internal`.
- Runtime: `docker network inspect clawker-net` lists no monitoring container; from an agent `getent hosts otel-collector` fails; Envoy, CoreDNS, netlogger, and gateway records still arrive in their indices (`.agents/skills/monitoring-checks/SKILL.md`). Start from `clawker monitor down --volumes`.

## Out of scope

- Authenticating OpenSearch or Prometheus. They are unreachable from agents after this item; host-side access is the user's own.
