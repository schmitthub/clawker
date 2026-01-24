Assessment of output/error handling vs cli-guidelines.md:

Non-adherent output patterns:
- Success/primary output goes to stderr instead of stdout:
  - init success output in internal/cmd/init/init.go (lines 98-113).
  - config check success output in internal/cmd/config/config.go (lines 100-116).
  - build success output in internal/cmd/build/build.go (line 117).
- Monitoring commands print normal status/success to stderr (should be stdout):
  - monitor init uses stderr for normal output and log-style prefixes [INFO]/[SKIP]/[SUCCESS] in internal/cmd/monitor/init.go (lines 63-109).
  - monitor up warnings/success/status to stderr in internal/cmd/monitor/up.go (lines 72-109) and extra status output in checkRunningContainers (lines 138-151).
  - monitor status prints status to stderr in internal/cmd/monitor/status.go (lines 45-92).
  - monitor down prints status to stderr in internal/cmd/monitor/down.go (lines 73-81).
- Debug output goes to stdout with [DEBUG] labels rather than stderr/logger:
  - pkg/build/versions.go uses fmt.Printf for debug and “Full version…” output (lines 67-95).

Guideline references:
- Primary output should go to stdout; errors/logging to stderr.
- Don’t treat stderr like a log file by default (avoid [INFO]/[DEBUG] style prefixes unless in verbose mode).