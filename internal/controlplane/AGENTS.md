# internal/controlplane

Entrypoint package for the control-plane daemon binary. Owns `Main()` and `run()`: startup sequence, aggregate health, and gRPC stack wiring. `cmd/clawkercp/clawkercp.go` is the thin `main` shell. The subsystems live under `controlplane/`.

Read the Control-plane safety section of `controlplane/AGENTS.md` before changing this package. Its failure rules (no `panic`, `log.Fatal`, or `os.Exit` on the boot or serve path; every long-lived goroutine recovers; subsystems degrade with `event=<subsystem>_unavailable`) apply here.
