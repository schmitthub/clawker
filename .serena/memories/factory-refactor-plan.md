# Plan: Refactor Factory Constructor + Clean Up Design Violations

## Status: Tasks 1-6 COMPLETE

All phases of the factory refactor are done:
1. config.Config -> config.Project (struct rename)
2. internal/prompts -> internal/prompter (package rename)
3. internal/resolver dissolved into internal/docker
4. config.Config gateway type created
5. Factory struct reduced from 25+ to 9 fields, constructor rewritten
6. All consumers updated (60+ files), all tests pass (2753 tests, 0 failures)

Key additions:
- `config.NewConfigForTest(workDir, project, settings)` helper for unit tests
- BuildKitEnabled now called directly in buildRun via `docker.BuildKitEnabled(ctx, client.APIClient)`
- HostProxy accessed via `opts.HostProxy()` manager pattern
- project/init and project/register use `cfgGateway.Registry()` wrapped in closure for RegisterProject