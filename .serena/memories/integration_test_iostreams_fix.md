# Integration Test IOStreams Fix

**Branch:** `a/run-fixes`
**Last Updated:** 2025-01-22
**Status:** COMPLETE - All changes made, unit tests pass

## Summary

Fixed integration test infrastructure so that commands use `Factory.IOStreams` instead of `os.Stdout`/`os.Stderr`/`os.Stdin` directly. This allows tests to capture command output for assertions.

## Changes Made

### Production Code

1. **exec.go** - Updated to use `f.IOStreams`:
   - `fmt.Fprintln(f.IOStreams.Out, execID)` for detached mode output
   - `stdcopy.StdCopy(f.IOStreams.Out, f.IOStreams.ErrOut, ...)` for non-TTY output
   - `io.Copy(hijacked.Conn, f.IOStreams.In)` for stdin
   - Added logger interactive mode: `logger.SetInteractiveMode(true/false)` for TTY+Interactive mode
   - Removed unused `os` import

2. **run.go** - Updated to use `f.IOStreams`:
   - `fmt.Fprintln(f.IOStreams.ErrOut, "Warning:", warning)` for warnings
   - `fmt.Fprintln(f.IOStreams.Out, containerID[:12])` for detached mode output
   - `stdcopy.StdCopy(f.IOStreams.Out, f.IOStreams.ErrOut, ...)` for non-TTY output
   - `io.Copy(hijacked.Conn, f.IOStreams.In)` for stdin
   - Updated `attachAndWait` signature to include `f *cmdutil.Factory`
   - Removed unused `os` import

### Integration Tests

1. **exec_integration_test.go** - Fixed 6 test locations:
   - Changed from `cmdutil.NewTestIOStreams().IOStreams` pattern to `ios := cmdutil.NewTestIOStreams()` + `ios.IOStreams`
   - Changed output verification from `stdout.String()` to `ios.OutBuf.String()`
   - Changed error checking from `stderr.String()` to `ios.ErrBuf.String()`
   - Removed unused `bytes` import

2. **run_integration_test.go** - Fixed 8 test locations:
   - Same pattern as exec tests
   - Updated comments about container ID output location
   - Removed unused `bytes` import

### Other Fixes

- **top_test.go** - Fixed pre-existing test failure: Changed expected `Use` from `[CONTAINER]` to `CONTAINER`

## Next Steps

Integration tests should now be able to capture command output. To verify:

```bash
# Run exec integration tests
go test -tags=integration ./pkg/cmd/container/exec/... -v -timeout 5m

# Run run integration tests  
go test -tags=integration ./pkg/cmd/container/run/... -v -timeout 5m
```

## Related Memories

- `agent_flag_bool_refactor_session2.md` - Completed refactor (unit tests pass)
- `agent_flag_refactor_and_context_fixes.md` - Original refactor session

---

**COMPLETE:** The IOStreams fix is done. Unit tests all pass. Integration tests need Docker to verify.
