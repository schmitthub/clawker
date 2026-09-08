# cmd/clawkerd

Thin `main` shell: `os.Exit(clawkerd.Main())`. All logic lives in `internal/clawkerd` and `clawkerd/`.

Read the Control-plane safety section of `controlplane/AGENTS.md` before changing this package. Its failure rules (no `panic`, `log.Fatal`, or `os.Exit` on the boot or serve path; every long-lived goroutine recovers; subsystems degrade with `event=<subsystem>_unavailable`) apply here.
