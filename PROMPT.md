Refactor build logic:
1. Use serena memory `build_migrate` to track this change
2. Review branch history and past code changes related to @pkg/cmd/build and @pkg/image/build to understand the current state
3. The logic in @pkg/cmd/build needs to be merged into @pkg/image/build
4. The build command in @pkg/cmd/build should alias the build command in @pkg/image/build
5. Update tests accordingly to reflect the new structure and ensure all functionalities are covered. Do not remove any existing tests unless they are redundant after the refactor.
6. Ensure all documentation is updated to reflect the new structure and usage of the build command.
7. Verify that the build process works seamlessly after the refactor by running end-to-end tests
   1. `image build` and `build` commands should produce identical results
   2. Built should have the configured labels in @pkg/internal/labels/labels.go
   3. Builds should use `clawker.yaml` to create the build context unless a dockefile is explicitly provided using `-f/--file` flag
   4. Build command should have same flags and options listed in `docker help image build`
   5. Validate with different configurations and edge cases
8. Frequently update serena memory `build_migrate` with tasks, gotchas, learnings etc. Especially before context compaction (approx 70% context left). Always remove prune the memory with stale context
9.  Commit changes but DO NOT push them
10. Output <promise>DONE</promise> after changes are commited.
