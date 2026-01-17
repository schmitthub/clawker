Ensure all methods in @pkg/whail that wrap a method from "github.com/moby/moby/client" expose the exact same method signature and return types:
* Check `moby_upgrade` serena memory for plan and research insigt before doing anything else
* Check if moby/moby signatures in `moby_upgrade` before reading its source code.
* Review past code changes for current state
* It is OK if the wrapper signature has additional parameters
* It is NOT OK if the wrapper has a different return type
* Fix all dependents affected by this change
* Existing testing logic can only be modified for signature and return type changes
* The following external resources can be helpful for code examples:
  * Cobra cli setup [github.com/docker/cli/commands](https://github.com/docker/cli/tree/master/cmd/docker)
  * Implementing cobra cli commands using moby/moby/client: [https://github.com/docker/cli/command](https://github.com/docker/cli/tree/master/cli/command)
* All tests must pass in the entire project
* Frequently update serena memory `moby_upgrade` with tasks, gotchas, learning, moby/moby signatures etc. Especially before context compaction (approx 70% context left). Always remove prune the memory with stale context
* Commit changes but DO NOT push them
* Output <promise>DONE</promise> after changes are commited.
