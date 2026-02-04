# Git Worktree — Abstract Behavioral Test Cases

## STATUS: NOT STARTED

Plain-language descriptions of expected behavior for a git worktree wrapper.
Each case states a situation and what should happen. No implementation details,
no specific APIs, no CLI flag syntax. Adapt these into concrete tests against
whatever your wrapper's interface looks like.

---

## ADD

case: Adding a worktree at a new path targeting a branch that is not checked out anywhere.
wants: Succeed. The new worktree has that branch checked out and HEAD matches the branch tip.

case: Adding a worktree at a path whose basename matches a branch that already exists and is not checked out anywhere, without specifying a commit-ish.
wants: Check out the existing branch in the new worktree. Do not create a duplicate branch.

case: Adding a worktree at a path whose basename does not match any existing branch, without specifying a commit-ish.
wants: Automatically create a new branch named after the path basename, based on HEAD, and check it out.

case: Adding a worktree when the commit-ish is a branch name that is already checked out by another worktree.
wants: Refuse to create the new worktree.

case: Adding a worktree when the commit-ish is a branch name that is already checked out by another worktree, but force is specified.
wants: Allow creation despite the branch being checked out elsewhere.

case: Adding a worktree when the path is already assigned to some worktree but the directory is missing from disk (e.g., it was deleted manually).
wants: Refuse to create the new worktree.

case: Adding a worktree when the path is already assigned but missing, and force is specified.
wants: Allow creation and reclaim the path.

case: Adding a worktree when the path is already assigned, missing, AND the existing worktree entry is locked.
wants: Single force is not enough. Requires double force to proceed.

case: Adding a worktree with a name that already exists in worktree metadata.
wants: Refuse with an error indicating the worktree already exists.

case: Adding a worktree targeting a specific commit hash rather than a branch.
wants: Succeed with a detached HEAD pointing at that exact commit.

case: Adding a worktree targeting a tag.
wants: Resolve the tag to its commit and check out in detached HEAD mode.

case: Adding a worktree with the bare `-` as commit-ish.
wants: Treat it as the previously checked-out branch (`@{-1}`).

case: Adding a worktree requesting a new branch that does not yet exist.
wants: Create the new branch at the specified commit-ish (or HEAD if none given) and check it out.

case: Adding a worktree requesting a new branch name that already exists.
wants: Refuse because the branch already exists.

case: Adding a worktree requesting a force-create of a branch that already exists.
wants: Reset the existing branch to the specified commit and check it out.

case: Adding a worktree in detached HEAD mode.
wants: HEAD is detached at the specified commit. No branch is checked out.

case: Adding a worktree in detached HEAD mode with no commit-ish specified.
wants: Detach at whatever commit the current HEAD resolves to.

case: Adding a worktree in orphan mode.
wants: The worktree is associated with a new unborn branch. The index and working directory are empty.

case: Adding a worktree with checkout suppressed.
wants: Worktree directory is created and HEAD is set, but no files from the commit are written to the working directory.

case: Adding a worktree with the lock option.
wants: Worktree is created already in a locked state. No race window between creation and locking.

case: Adding a worktree with lock and a reason string.
wants: Worktree is created locked and the reason is stored with the lock.

case: Adding a worktree with quiet mode.
wants: No informational output on success. Errors still reported.

case: Adding a worktree when no local branch matches the basename but exactly one remote has a tracking branch with that name.
wants: Auto-create a local branch tracking the remote branch and check it out.

case: Adding a worktree when no local branch matches the basename and multiple remotes have a tracking branch with that name.
wants: Refuse due to ambiguity.

case: Adding a worktree when multiple remotes match but a default remote is configured for disambiguation.
wants: Use the configured default remote and succeed.

case: Adding a worktree with guess-remote enabled and a matching remote-tracking branch exists.
wants: Create local branch tracking the remote branch.

case: Adding a worktree with guess-remote enabled but no remote match exists.
wants: Fall through to creating a new branch based on HEAD.

case: Adding a worktree on a repo with no commits and no branches at all.
wants: Associate the worktree with a new unborn branch named after the path basename, as if orphan mode was used.

case: Adding a worktree with guess-remote on a repo that has a remote but no branches (local or remote) matching the basename.
wants: Fail with a warning to fetch from the remote first. Force overrides this.

case: Adding a worktree where the commit-ish is a remote-tracking branch.
wants: The newly created local branch should have the remote-tracking branch set as its upstream by default.

case: Adding a worktree where the commit-ish is a remote-tracking branch but tracking is explicitly disabled.
wants: The new branch should NOT have any upstream configured.

---

## LIST

case: Listing worktrees when no linked worktrees exist.
wants: At minimum the main worktree is shown.

case: Listing worktrees after adding several linked worktrees.
wants: Main worktree is listed first, followed by all linked worktrees.

case: Listing worktrees shows revision and branch for each entry.
wants: Each entry includes the commit hash and branch name (or "detached HEAD").

case: Listing worktrees when the main repo is bare.
wants: Main worktree entry shows a bare annotation.

case: Listing worktrees when one is locked.
wants: The locked worktree shows a "locked" annotation.

case: Listing worktrees when one has its directory missing (and is not locked).
wants: That worktree shows a "prunable" annotation.

case: Listing worktrees in verbose mode when a locked worktree has a reason.
wants: The reason is displayed on the line following the worktree entry.

case: Listing worktrees in porcelain mode.
wants: Machine-parseable output with one attribute per line, blank line between entries.

case: Listing worktrees in porcelain mode with NUL terminators.
wants: Lines terminated by NUL instead of newline.

case: Listing worktrees after adding one and then removing it.
wants: The removed worktree no longer appears.

---

## LOCK

case: Locking an unlocked worktree.
wants: Succeed. The worktree is now in a locked state.

case: Locking a worktree with a reason string.
wants: Succeed. The reason is stored and retrievable.

case: Locking a worktree that is already locked.
wants: Refuse or warn. Cannot double-lock.

case: A locked worktree when prune runs and its directory is missing.
wants: Prune does NOT remove the locked worktree's metadata.

case: Removing a locked worktree without force.
wants: Refuse.

case: Removing a locked worktree with double force.
wants: Succeed.

case: Moving a locked worktree without force.
wants: Refuse.

case: Moving a locked worktree with double force.
wants: Succeed.

---

## UNLOCK

case: Unlocking a locked worktree.
wants: Succeed. The worktree is no longer locked and can be removed, moved, or pruned normally.

case: Unlocking a worktree that is not locked.
wants: Refuse with an error.

---

## MOVE

case: Moving a worktree to a new path.
wants: Succeed. All metadata references are updated. The worktree is functional at the new location.

case: Moving a worktree and then listing.
wants: List shows the new path, not the old one.

case: Moving a worktree when the destination path is already assigned to another worktree.
wants: Refuse.

case: Moving a worktree when the destination is assigned to another worktree but that directory is missing, with force.
wants: Succeed. If the destination is also locked, double force is required.

case: Moving the main worktree.
wants: Refuse. The main worktree cannot be moved with this command.

case: Moving a worktree that contains submodules.
wants: Refuse. Worktrees with submodules cannot be moved.

case: After moving, the back-references between worktree and main repo are correct.
wants: The worktree's pointer to the repo and the repo's pointer to the worktree both resolve correctly.

---

## REMOVE

case: Removing a clean linked worktree (no uncommitted changes, no untracked files).
wants: Succeed. Both metadata and worktree directory are cleaned up. List no longer shows it.

case: Removing a worktree that has modified tracked files.
wants: Refuse due to unclean working tree.

case: Removing a worktree that has untracked files.
wants: Refuse due to unclean working tree.

case: Removing an unclean worktree with force.
wants: Succeed despite modifications or untracked files.

case: Removing a locked worktree without force.
wants: Refuse.

case: Removing a locked worktree with double force.
wants: Succeed.

case: Removing the main worktree.
wants: Always refuse, regardless of any flags.

case: Removing a worktree identified by just the unique trailing component of its path.
wants: Succeed if the trailing component is unambiguous among all worktrees.

case: Removing a worktree that does not exist.
wants: Refuse with a not-found error.

---

## PRUNE

case: Pruning when a worktree's directory has been manually deleted from disk.
wants: Remove the orphaned metadata for that worktree.

case: Pruning when all worktree directories still exist.
wants: No changes. All metadata is preserved.

case: Pruning when a locked worktree's directory is missing.
wants: Do NOT remove the locked worktree's metadata.

case: Pruning in dry-run mode.
wants: Report what would be pruned but make no changes.

case: Pruning in verbose mode.
wants: Report each removal in output.

case: Pruning with an expiry threshold when the stale entry is newer than the threshold.
wants: Do not prune the entry.

case: Pruning with an expiry threshold when the stale entry is older than the threshold.
wants: Prune the entry.

---

## REPAIR

case: Running repair after the main repository was moved to a new location.
wants: All linked worktrees' back-references to the main repo are updated to the new location.

case: Running repair after a linked worktree was manually moved (not via the move command).
wants: The main repo's pointer to that worktree is updated to the new location.

case: Running repair with explicit paths for multiple moved worktrees.
wants: All specified worktree back-references are fixed in one operation.

case: Running repair when both the main repo and linked worktrees were moved.
wants: Running repair in the main worktree with each linked worktree's new path fixes all connections in both directions.

case: Running repair when there is a mismatch between absolute and relative path linking style.
wants: Links are rewritten to match the configured style, even if they were technically functional before.

---

## REFS & ISOLATION

case: Two worktrees on different branches each have independent HEADs.
wants: Changing HEAD in one (via commit, checkout, etc.) has no effect on the other.

case: A new branch is created in one worktree.
wants: The branch is immediately visible from every other worktree. Branch refs are shared.

case: A bisect is started in one worktree.
wants: Bisect refs are NOT visible from other worktrees. They are per-worktree.

case: Refs under the per-worktree refs namespace are created in one worktree.
wants: They are scoped to that worktree only. Not visible from others.

case: Refs used by interactive rebase are created in one worktree.
wants: They are scoped to that worktree only.

case: Accessing another linked worktree's HEAD from the main worktree via the cross-worktree ref path.
wants: Resolves to the linked worktree's current HEAD.

case: Accessing the main worktree's HEAD from a linked worktree via the cross-worktree ref path.
wants: Resolves to the main worktree's current HEAD.

---

## CONFIGURATION

case: A config value set in the shared repository config.
wants: Readable from every worktree (main and linked).

case: Per-worktree config is enabled and a value is set in one worktree's config.
wants: That value applies only to that worktree. Other worktrees do not see it.

case: core.bare and core.worktree are in shared config with per-worktree config disabled.
wants: Those values apply only to the main worktree, not linked ones.

---

## METADATA STRUCTURE

case: After adding a linked worktree.
wants: A private subdirectory exists under the repo's worktrees admin directory containing at minimum a gitdir file and a HEAD file.

case: Adding two worktrees whose filesystem paths have the same basename.
wants: The second worktree's admin directory gets a numeric suffix to avoid collision.

case: The root of a linked worktree.
wants: Contains a .git text file pointing back to the admin directory, not a .git directory.

---

## EDGE CASES

case: Adding a worktree at a path containing spaces.
wants: Succeed. All references and metadata handle the spaces correctly.

case: Adding a worktree at a path containing special characters.
wants: Succeed or fail gracefully with a clear error. No corruption of metadata.

case: Adding many worktrees in rapid succession.
wants: All succeed. Each is independently listable, openable, and removable.

case: Adding worktrees concurrently (parallel operations with different names and paths).
wants: All succeed without corrupting the shared worktree metadata directory.

case: Adding a linked worktree from a bare repository.
wants: Succeed. Bare repos can have linked worktrees even though they have no main worktree.

case: Objects created in the main worktree are accessible from a linked worktree.
wants: Commits, trees, and blobs are shared. No fetch or transfer needed.

case: A commit made in a linked worktree.
wants: The new commit is reachable from the main worktree via branch refs.

case: A worktree created by the wrapper is inspected by the system git installation.
wants: The system sees the worktree and reports it correctly.

case: A worktree created by the system git installation is inspected by the wrapper.
wants: The wrapper recognizes it and can operate on it.
