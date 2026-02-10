# Git Worktree — Abstract Behavioral Test Cases

## STATUS: REFERENCE

# Git Worktree — Behavioral Test Cases (Gherkin)

Feature: Worktree Add
As a developer
I want to create linked worktrees
So that I can work on multiple branches simultaneously

# Branch Resolution & Auto-Creation

Scenario: Add worktree targeting an unchecked-out branch
Given a repository with a branch "feature-a" that is not checked out anywhere
When I add a worktree at a new path targeting "feature-a"
Then the operation should succeed
And the new worktree should have "feature-a" checked out
And HEAD should match the branch tip

Scenario: Add worktree with basename matching existing unchecked-out branch
Given a repository with a branch "hotfix" that is not checked out anywhere
When I add a worktree at path "../hotfix" without specifying a commit-ish
Then the operation should succeed
And the existing "hotfix" branch should be checked out
And no duplicate branch should be created

Scenario: Add worktree with basename not matching any branch
Given a repository with no branch named "hotfix"
When I add a worktree at path "../hotfix" without specifying a commit-ish
Then the operation should succeed
And a new branch "hotfix" should be created based on HEAD
And the new branch should be checked out in the worktree

Scenario: Add worktree when branch is already checked out elsewhere
Given a repository with branch "main" checked out in the main worktree
When I add a worktree targeting "main"
Then the operation should be refused

Scenario: Add worktree when branch is checked out elsewhere with force
Given a repository with branch "main" checked out in the main worktree
When I add a worktree targeting "main" with force
Then the operation should succeed

Scenario: Add worktree when path is assigned but directory is missing
Given a linked worktree was previously created at "/tmp/wt-a"
And the directory "/tmp/wt-a" was manually deleted
And the worktree metadata still exists
When I add a worktree at "/tmp/wt-a"
Then the operation should be refused

Scenario: Add worktree reclaiming missing path with force
Given a linked worktree was previously created at "/tmp/wt-a"
And the directory "/tmp/wt-a" was manually deleted
And the worktree metadata still exists
When I add a worktree at "/tmp/wt-a" with force
Then the operation should succeed
And the path should be reclaimed

Scenario: Add worktree reclaiming missing locked path requires double force
Given a linked worktree was previously created at "/tmp/wt-a"
And the worktree is locked
And the directory "/tmp/wt-a" was manually deleted
When I add a worktree at "/tmp/wt-a" with single force
Then the operation should be refused
When I add a worktree at "/tmp/wt-a" with double force
Then the operation should succeed

Scenario: Add worktree with duplicate name
Given a worktree named "feature" already exists
When I add a worktree with the same name "feature"
Then the operation should be refused
And the error should indicate the worktree already exists

Scenario: Add worktree targeting a commit hash
Given a repository with a commit "abc123"
When I add a worktree targeting that commit hash
Then the operation should succeed
And HEAD should be detached at that exact commit

Scenario: Add worktree targeting a tag
Given a repository with a tag "v1.0" pointing at a commit
When I add a worktree targeting "v1.0"
Then the operation should succeed
And HEAD should be detached at the tagged commit

Scenario: Add worktree with bare dash as commit-ish
Given the main worktree previously had "develop" checked out before "main"
When I add a worktree targeting "-"
Then the operation should succeed
And the worktree should have "develop" checked out

# Branch Creation

Scenario: Add worktree creating a new branch
Given a repository with no branch named "new-feature"
When I add a worktree requesting new branch "new-feature"
Then the operation should succeed
And branch "new-feature" should be created at HEAD
And the worktree should have "new-feature" checked out

Scenario: Add worktree creating branch that already exists
Given a repository with an existing branch "existing"
When I add a worktree requesting new branch "existing"
Then the operation should be refused

Scenario: Add worktree force-creating branch that already exists
Given a repository with an existing branch "existing" at commit A
When I add a worktree force-creating branch "existing" at commit B
Then the operation should succeed
And branch "existing" should be reset to commit B

# Detached HEAD & Orphan

Scenario: Add worktree in detached HEAD mode
Given a repository with a commit "abc123"
When I add a worktree in detached mode at "abc123"
Then the operation should succeed
And HEAD should be detached
And no branch should be checked out

Scenario: Add worktree in detached mode without commit-ish
Given a repository with HEAD pointing at commit "abc123"
When I add a worktree in detached mode without specifying a commit
Then the operation should succeed
And HEAD should be detached at "abc123"

Scenario: Add worktree in orphan mode
Given a repository
When I add a worktree in orphan mode with branch "fresh-start"
Then the operation should succeed
And the worktree should have an unborn branch "fresh-start"
And the index should be empty
And the working directory should be empty

Scenario: Add worktree with checkout suppressed
Given a repository with files in HEAD
When I add a worktree with checkout suppressed
Then the operation should succeed
And HEAD should be set
And the working directory should be empty

# Lock at Creation

Scenario: Add worktree with lock option
Given a repository
When I add a worktree with the lock option
Then the operation should succeed
And the worktree should be in a locked state immediately

Scenario: Add worktree with lock and reason
Given a repository
When I add a worktree with lock and reason "on USB drive"
Then the operation should succeed
And the worktree should be locked
And the lock reason should be "on USB drive"

Scenario: Add worktree in quiet mode
Given a repository
When I add a worktree in quiet mode
Then the operation should succeed
And there should be no informational output

# Remote Tracking

Scenario: Add worktree with basename matching single remote tracking branch
Given a repository with no local branch "feature-x"
And exactly one remote has "origin/feature-x"
When I add a worktree at "../feature-x" without specifying a commit-ish
Then the operation should succeed
And a local branch "feature-x" should be created
And "feature-x" should track "origin/feature-x"

Scenario: Add worktree with basename matching multiple remote tracking branches
Given a repository with no local branch "feature-x"
And remote "origin" has "origin/feature-x"
And remote "upstream" has "upstream/feature-x"
When I add a worktree at "../feature-x" without specifying a commit-ish
Then the operation should be refused due to ambiguity

Scenario: Add worktree with default remote configured for disambiguation
Given a repository with no local branch "feature-x"
And remote "origin" has "origin/feature-x"
And remote "upstream" has "upstream/feature-x"
And checkout.defaultRemote is set to "origin"
When I add a worktree at "../feature-x" without specifying a commit-ish
Then the operation should succeed
And "feature-x" should track "origin/feature-x"

Scenario: Add worktree with guess-remote and matching remote branch
Given guess-remote is enabled
And remote "origin" has "origin/hotfix"
And no local branch "hotfix" exists
When I add a worktree at "../hotfix"
Then the operation should succeed
And local branch "hotfix" should be created tracking "origin/hotfix"

Scenario: Add worktree with guess-remote and no remote match
Given guess-remote is enabled
And no remote has a branch matching "hotfix"
When I add a worktree at "../hotfix"
Then the operation should succeed
And a new branch "hotfix" should be created based on HEAD

Scenario: Add worktree on empty repository
Given a freshly initialized repository with no commits
When I add a worktree at "../wt"
Then the operation should succeed
And the worktree should have an unborn branch named "wt"

Scenario: Add worktree with guess-remote on repo with remote but no matching branch
Given guess-remote is enabled
And a remote exists but has no branch matching "hotfix"
And no local branches exist
When I add a worktree at "../hotfix"
Then the operation should fail with a warning to fetch first
When I add a worktree at "../hotfix" with force
Then the operation should succeed

Scenario: Add worktree tracking remote branch sets upstream
Given remote "origin" has "origin/feature"
When I add a worktree with commit-ish "origin/feature"
Then the operation should succeed
And the new local branch should have "origin/feature" as upstream

Scenario: Add worktree tracking remote branch with tracking disabled
Given remote "origin" has "origin/feature"
When I add a worktree with commit-ish "origin/feature" and tracking disabled
Then the operation should succeed
And the new branch should NOT have any upstream configured


Feature: Worktree List
As a developer
I want to list all worktrees
So that I can see what branches I'm working on

Scenario: List with no linked worktrees
Given a repository with only the main worktree
When I list worktrees
Then the main worktree should be shown

Scenario: List with multiple linked worktrees
Given a repository with linked worktrees "wt-a", "wt-b", and "wt-c"
When I list worktrees
Then the main worktree should be listed first
And all linked worktrees should be listed

Scenario: List shows revision and branch
Given a repository with worktrees on various branches
When I list worktrees
Then each entry should include the commit hash
And each entry should include the branch name or "detached HEAD"

Scenario: List on bare repository
Given a bare repository
When I list worktrees
Then the main entry should show a bare annotation

Scenario: List shows locked annotation
Given a repository with a locked worktree
When I list worktrees
Then the locked worktree should show a "locked" annotation

Scenario: List shows prunable annotation
Given a repository with a worktree whose directory was deleted
And the worktree is not locked
When I list worktrees
Then that worktree should show a "prunable" annotation

Scenario: List verbose shows lock reason
Given a repository with a worktree locked with reason "NFS share"
When I list worktrees in verbose mode
Then the lock reason should be displayed on the line following the entry

Scenario: List in porcelain mode
Given a repository with worktrees
When I list worktrees in porcelain mode
Then output should have one attribute per line
And entries should be separated by blank lines

Scenario: List in porcelain mode with NUL terminators
Given a repository with worktrees
When I list worktrees in porcelain mode with NUL terminators
Then lines should be terminated by NUL instead of newline

Scenario: List reflects removal
Given a repository with a linked worktree "wt-a"
When I remove "wt-a"
And I list worktrees
Then "wt-a" should not appear in the list


Feature: Worktree Lock
As a developer
I want to lock worktrees
So that they are protected from pruning and removal

Scenario: Lock an unlocked worktree
Given a repository with an unlocked worktree "wt-a"
When I lock "wt-a"
Then the operation should succeed
And "wt-a" should be in a locked state

Scenario: Lock with a reason
Given a repository with an unlocked worktree "wt-a"
When I lock "wt-a" with reason "mounted on USB"
Then the operation should succeed
And the lock reason should be stored
And the lock reason should be retrievable

Scenario: Lock an already locked worktree
Given a repository with a locked worktree "wt-a"
When I lock "wt-a"
Then the operation should be refused or warn

Scenario: Locked worktree survives prune when directory missing
Given a repository with a locked worktree "wt-a"
And the directory for "wt-a" was manually deleted
When I run prune
Then the metadata for "wt-a" should NOT be removed

Scenario: Remove locked worktree without force
Given a repository with a locked worktree "wt-a"
When I remove "wt-a" without force
Then the operation should be refused

Scenario: Remove locked worktree with double force
Given a repository with a locked worktree "wt-a"
When I remove "wt-a" with double force
Then the operation should succeed

Scenario: Move locked worktree without force
Given a repository with a locked worktree "wt-a"
When I move "wt-a" to a new path without force
Then the operation should be refused

Scenario: Move locked worktree with double force
Given a repository with a locked worktree "wt-a"
When I move "wt-a" to a new path with double force
Then the operation should succeed


Feature: Worktree Unlock
As a developer
I want to unlock worktrees
So that I can remove, move, or prune them

Scenario: Unlock a locked worktree
Given a repository with a locked worktree "wt-a"
When I unlock "wt-a"
Then the operation should succeed
And "wt-a" should no longer be locked
And "wt-a" should be removable normally

Scenario: Unlock a worktree that is not locked
Given a repository with an unlocked worktree "wt-a"
When I unlock "wt-a"
Then the operation should be refused with an error


Feature: Worktree Move
As a developer
I want to move worktrees
So that I can reorganize my workspace

Scenario: Move worktree to new path
Given a repository with a worktree at "/tmp/wt-old"
When I move it to "/tmp/wt-new"
Then the operation should succeed
And all metadata references should be updated
And the worktree should be functional at the new location

Scenario: Move worktree and list
Given a repository with a worktree at "/tmp/wt-old"
When I move it to "/tmp/wt-new"
And I list worktrees
Then the list should show "/tmp/wt-new"
And the list should not show "/tmp/wt-old"

Scenario: Move worktree to already-assigned path
Given a repository with worktrees at "/tmp/wt-a" and "/tmp/wt-b"
When I move "wt-a" to "/tmp/wt-b"
Then the operation should be refused

Scenario: Move worktree to assigned but missing path with force
Given a repository with a worktree at "/tmp/wt-a"
And a worktree was assigned to "/tmp/wt-b" but the directory is missing
When I move "wt-a" to "/tmp/wt-b" with force
Then the operation should succeed

Scenario: Move the main worktree
Given a repository with a main worktree
When I try to move the main worktree
Then the operation should be refused

Scenario: Move worktree containing submodules
Given a repository with a worktree that contains submodules
When I try to move that worktree
Then the operation should be refused

Scenario: Back-references correct after move
Given a repository with a worktree at "/tmp/wt-a"
When I move it to "/tmp/wt-new"
Then the worktree's pointer to the repo should resolve correctly
And the repo's pointer to the worktree should resolve correctly


Feature: Worktree Remove
As a developer
I want to remove worktrees
So that I can clean up when I'm done with a branch

Scenario: Remove a clean worktree
Given a repository with a clean linked worktree "wt-a"
When I remove "wt-a"
Then the operation should succeed
And the metadata should be cleaned up
And the worktree directory should be cleaned up
And "wt-a" should not appear in the list

Scenario: Remove worktree with modified tracked files
Given a repository with a worktree "wt-a" that has modified tracked files
When I remove "wt-a"
Then the operation should be refused due to unclean working tree

Scenario: Remove worktree with untracked files
Given a repository with a worktree "wt-a" that has untracked files
When I remove "wt-a"
Then the operation should be refused due to unclean working tree

Scenario: Remove unclean worktree with force
Given a repository with a worktree "wt-a" that has modifications
When I remove "wt-a" with force
Then the operation should succeed

Scenario: Remove locked worktree without force
Given a repository with a locked worktree "wt-a"
When I remove "wt-a" without force
Then the operation should be refused

Scenario: Remove locked worktree with double force
Given a repository with a locked worktree "wt-a"
When I remove "wt-a" with double force
Then the operation should succeed

Scenario: Remove the main worktree
Given a repository with a main worktree
When I try to remove the main worktree
Then the operation should always be refused

Scenario: Remove worktree by unique path suffix
Given worktrees at "/long/path/to/alpha" and "/other/path/to/beta"
When I remove "alpha"
Then the operation should succeed

Scenario: Remove nonexistent worktree
Given a repository with no worktree named "nonexistent"
When I remove "nonexistent"
Then the operation should be refused with a not-found error


Feature: Worktree Prune
As a developer
I want to prune stale worktree metadata
So that orphaned entries are cleaned up

Scenario: Prune after directory manually deleted
Given a repository with a worktree "wt-a"
And the directory for "wt-a" was manually deleted
When I run prune
Then the orphaned metadata for "wt-a" should be removed

Scenario: Prune when all directories exist
Given a repository with worktrees whose directories all exist
When I run prune
Then no metadata should be removed

Scenario: Prune respects locks
Given a repository with a locked worktree "wt-a"
And the directory for "wt-a" was manually deleted
When I run prune
Then the metadata for "wt-a" should NOT be removed

Scenario: Prune in dry-run mode
Given a repository with stale worktree metadata
When I run prune in dry-run mode
Then it should report what would be pruned
And no changes should be made

Scenario: Prune in verbose mode
Given a repository with stale worktree metadata
When I run prune in verbose mode
Then each removal should be reported in output

Scenario: Prune with expiry threshold not met
Given a repository with a stale worktree entry from 5 minutes ago
When I run prune with expiry threshold of 1 hour
Then the entry should not be pruned

Scenario: Prune with expiry threshold met
Given a repository with a stale worktree entry from 2 hours ago
When I run prune with expiry threshold of 1 hour
Then the entry should be pruned


Feature: Worktree Repair
As a developer
I want to repair worktree connections
So that moved repositories and worktrees work correctly

Scenario: Repair after main repository moved
Given a repository with linked worktrees
And the main repository was moved to a new location
When I run repair from the main worktree
Then all linked worktrees' back-references should be updated

Scenario: Repair after linked worktree manually moved
Given a repository with a linked worktree at "/old/path"
And the worktree was manually moved to "/new/path"
When I run repair from "/new/path"
Then the main repo's pointer to that worktree should be updated

Scenario: Repair multiple moved worktrees with explicit paths
Given a repository with worktrees that were moved to new paths
When I run repair with all the new paths specified
Then all back-references should be fixed in one operation

Scenario: Repair when both main and linked worktrees moved
Given a repository where both main repo and linked worktrees were moved
When I run repair from the main worktree with linked worktree paths
Then all connections should be fixed in both directions

Scenario: Repair with path linking style mismatch
Given a repository using absolute paths
And the configuration specifies relative paths
When I run repair
Then links should be rewritten to use relative paths


Feature: Worktree Refs and Isolation
As a developer
I want worktrees to have proper ref isolation
So that operations in one worktree don't affect others unexpectedly

Scenario: Independent HEADs
Given a repository with two worktrees on different branches
When I change HEAD in one worktree
Then the other worktree's HEAD should be unchanged

Scenario: Shared branch refs
Given a repository with two worktrees
When I create a new branch in one worktree
Then the branch should be visible from the other worktree

Scenario: Per-worktree bisect refs
Given a repository with two worktrees
When I start a bisect in one worktree
Then bisect refs should NOT be visible from the other worktree

Scenario: Per-worktree refs namespace isolation
Given a repository with two worktrees
When I create refs in the per-worktree namespace in one worktree
Then those refs should not be visible from the other worktree

Scenario: Per-worktree rebase refs isolation
Given a repository with two worktrees
When I start an interactive rebase in one worktree
Then rebase refs should be scoped to that worktree only

Scenario: Cross-worktree ref access to linked worktree
Given a repository with main worktree and linked worktree "wt-a"
When I access "worktrees/wt-a/HEAD" from the main worktree
Then it should resolve to the linked worktree's HEAD

Scenario: Cross-worktree ref access to main worktree
Given a repository with a linked worktree
When I access "main-worktree/HEAD" from the linked worktree
Then it should resolve to the main worktree's HEAD


Feature: Worktree Configuration
As a developer
I want proper configuration sharing and isolation
So that worktrees have appropriate settings

Scenario: Shared repository config
Given a repository with a config value set
When I read that config from any worktree
Then the value should be readable

Scenario: Per-worktree config enabled
Given a repository with per-worktree config enabled
And a config value set in one worktree's config
When I read that config from another worktree
Then the value should NOT be visible

Scenario: core.bare and core.worktree scope
Given a repository with core.bare or core.worktree in shared config
And per-worktree config is disabled
Then those values should apply only to the main worktree


Feature: Worktree Metadata Structure
As a developer
I want correct worktree metadata structure
So that git operates correctly

Scenario: Linked worktree admin directory
Given I add a linked worktree
Then a private subdirectory should exist under the worktrees admin directory
And it should contain at minimum a gitdir file and a HEAD file

Scenario: Admin directory name collision
Given a repository
When I add two worktrees at paths with the same basename
Then the second worktree's admin directory should get a numeric suffix

Scenario: Linked worktree root contains .git file
Given a linked worktree
Then its root should contain a .git text file
And the .git file should point to the admin directory


Feature: Worktree Edge Cases
As a developer
I want edge cases handled correctly
So that the wrapper is robust

Scenario: Path with spaces
Given a repository
When I add a worktree at a path containing spaces
Then the operation should succeed
And all references and metadata should handle the spaces correctly

Scenario: Path with special characters
Given a repository
When I add a worktree at a path with special characters
Then the operation should succeed or fail gracefully
And no metadata should be corrupted

Scenario: Many worktrees in rapid succession
Given a repository
When I add many worktrees in rapid succession
Then all should succeed
And each should be independently listable and removable

Scenario: Concurrent worktree additions
Given a repository
When I add multiple worktrees concurrently with different names
Then all should succeed
And the shared metadata directory should not be corrupted

Scenario: Linked worktree from bare repository
Given a bare repository
When I add a linked worktree
Then the operation should succeed

Scenario: Object sharing between worktrees
Given a repository with a main worktree and a linked worktree
When I create objects in the main worktree
Then they should be accessible from the linked worktree without transfer

Scenario: Commit visibility across worktrees
Given a repository with a main worktree and a linked worktree
When I commit in the linked worktree
Then the commit should be reachable from the main worktree via branch refs

Scenario: Wrapper-created worktree visible to system git
Given I create a worktree using the wrapper
When I list worktrees using the system git installation
Then the worktree should appear in the list

Scenario: System-created worktree visible to wrapper
Given I create a worktree using the system git installation
When I list worktrees using the wrapper
Then the worktree should appear in the list
