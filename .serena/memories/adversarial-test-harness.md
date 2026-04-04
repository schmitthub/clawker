# Adversarial Test Harness

## Location
`test/adversarial/`

## Primary Workflow
Live red team sessions. The operator (Andrew) directs the AI agent inside a clawker container to attempt exfiltration. The C2 captures results in SQLite. The operator checks what got through.

NOT an automated test suite. The `scripts/run-tests.sh` is secondary.

## Components
- **C2 server** (`attacker-server/main.go`): Go + SQLite, multi-protocol (HTTPS/HTTP/UDP/ICMP), auto-decode pipeline for various encoding schemes
- **Dockerfile** (adversarial root): Multi-stage build, self-signed TLS
- **compose.yml**: C2 on `clawker-net` external bridge network
- **Payloads** (`payloads/01-30`): Reference from prior project (Benthic), not adapted for clawker yet

## Key Hostproxy Findings
1. Zero auth on all endpoints
2. Newline injection in git credential protocol format
3. Arbitrary port binding via `/callback/register`
4. Git credential extraction for any host
