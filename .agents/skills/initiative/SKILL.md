---
name: initiative
description: Create or continue multi-task development and testing initiatives with a shared Serena plan, acceptance checks, and one task per conversation. Use for work that needs task handoffs across conversations, not routine single-task changes.
---

# Initiative

Plan work in a shared memory, then complete one task per conversation. Use the
user's request to select planning or execution. Creating a plan does not start
its implementation. Follow the active session's permissions and mode; in a
read-only planning session, present the plan without writing memory or code.

## Create a plan

1. Read the project instructions, relevant code, and existing plans. Establish
   the intended result, scope, constraints, and measurable completion criteria.
   State assumptions and ask about choices that affect the work.
2. Divide the work into tasks small enough to complete and verify in one
   conversation. Give each task its dependencies, affected files or modules,
   implementation steps, and acceptance checks with expected results. Follow
   the project's test-first requirements for code changes.
3. Use [assets/initiative.md](assets/initiative.md) to create the memory. Replace
   all placeholders, omit unused optional fields, and repeat the task section
   as needed. Retain shared context once: background, key files, design choices,
   and links to applicable instructions.
4. Follow `.serena/memories/memory_maintenance.md`. Unless the user supplies a
   location, use `plans/<initiative-name>/initiative` as the memory name. Link
   it from the relevant parent memory, or `plans/core` for a top-level plan,
   using a `mem:` reference with a short description. Preserve existing records;
   do not overwrite another initiative with the same name.
5. Use Serena memory tools when available. Otherwise read and write the same
   files under `.serena/memories/`, adding `.md` to the memory name. Report the
   memory name and provide a prompt to start the first task.

## Continue a task

1. Read the initiative memory and its linked instructions. Check the current
   branch, working tree, and relevant code before relying on recorded progress.
   Preserve unrelated edits and do not switch branches over existing work.
2. Use the task named by the user. If none is named, resume an unfinished task
   already in progress; otherwise select the first pending or blocked task
   whose dependencies are complete. Recheck its blocker before resuming. If
   it still needs user input, or several tasks are already in progress, report
   the choice needed before editing.
   Do not bypass an incomplete dependency to start a requested task.
3. Mark the task `in_progress` and record the current agent. Implement only
   that task and changes needed to satisfy its acceptance criteria. If new
   evidence requires a scope decision, record it and ask before expanding work.
4. Run the task's acceptance checks. Record commands, results, and any checks
   that could not run. Fix failures within scope and rerun affected checks.
   A failed or unrun required check prevents completion.

Use `pending`, `in_progress`, `blocked`, and `complete` in the tracker. Record
the reason and the next action when blocked. Recheck that reason on resumption;
do not treat an old blocker as proof that work is still blocked. If all tasks
are complete, report the recorded results without starting additional work.

## Close the task and hand off

1. Review the task's diff. Select reviews that apply to the change: code
   correctness, error handling, tests, simplicity, comments, or type design.
   Use relevant available reviewers when delegation is permitted. Follow the
   project's test-hunter procedure for new or changed Go tests. When a reviewer
   is unavailable or delegation is prohibited, review directly and record that
   limit.
2. Address findings supported by the code and within the task's scope. Record
   unrelated findings as follow-up work. Rerun affected acceptance checks after
   fixes. Unresolved findings that prevent acceptance keep the task incomplete.
3. Update the tracker, task results, and key learnings. Mark the task complete
   only when its required checks and reviews are satisfied. Record failures and
   blockers accurately, including the next action needed to resume.
4. Commit only when the current session authorizes it. Include only this task's
   changes, preserve other staged work, and follow project commit rules. Record
   the commit result or why no commit was made. An optional commit does not
   determine acceptance; if the user requires a commit for completion, keep
   the task incomplete until it succeeds. The skill grants no extra permission
   to commit, push, publish, or change external systems.
5. **Stop after this task's wrap-up. Do not start the next task.** Present the
   result and a self-contained prompt for a new conversation. Include the
   initiative memory name, branch, completed or blocked task, next task or
   remaining action, and any relevant uncommitted work or verification limits.
   Respect an explicit user instruction to change the conversation boundary.

For the final task, report initiative completion and the acceptance results.
Do not produce a prompt for a nonexistent next task. If the final task is
blocked, provide a prompt to resume that task instead.
