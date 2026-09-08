# cmd/clawkercp

Thin `main` shell: `os.Exit(controlplane.Main())`. All logic lives in `internal/controlplane` and `controlplane/`.

Read the Control-plane safety section of `controlplane/AGENTS.md` before changing this package. Its failure rules (no `panic`, `log.Fatal`, or `os.Exit` on the boot or serve path; every long-lived goroutine recovers; subsystems degrade with `event=<subsystem>_unavailable`) apply here.
