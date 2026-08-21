# internal/sudo

Clawker's privileged one-shot lane: prompting for the sudo credential and
running an embedded helper binary under it exactly once. Callers decide that
elevation is warranted; this package does the staging, prompting, and
execution.

## Surface

```go
var ErrSudoUnavailable error            // sudo is not on PATH — the step cannot even be attempted

type ElevatedHelper struct {
    Name   string   // staged filename — must be a plain base name (no separators/traversal; staging rejects otherwise); what the person sees in the sudo prompt and audit log
    Binary []byte   // the embedded helper
    Args   []string // handed verbatim to the root process
}

func RunElevated(ctx, ios *iostreams.IOStreams, helper ElevatedHelper) error
func Password(ios *iostreams.IOStreams) (string, error)
```

`RunElevated` stages the embedded binary into a fresh private 0700 temp
directory (a fixed path in a world-writable directory is a name another local
user can win a race for), stops any spinner, prompts via `Password`, runs the
helper once with `sudo -S -p ''` feeding the credential on stdin, and removes
the staging directory. Helpers are embedded rather than installed: nothing is
left on the user's system.

The staged file mode is 0755 — the other-execute bit is load-bearing for
helpers that re-exec themselves inside a fresh user namespace (idmap-mount's
holder child), where the owner uid is unmapped before `uid_map` is written and
the DAC check falls through to the "other" bits.

`Password` prompts without echo and RETURNS the credential; it runs nothing.
A non-interactive session errors instead of reading cleartext from a pipe.

## Consumers

- `internal/cmd/controlplane/shared` `AssistSOS` — the bpffs delegation heal
  (`bpffs-delegate` embed).
- `internal/cmd/container/shared` `ensureIDMappedWorkspace` — the ID-mapped
  workspace view attach (`idmap-mount` embed).
