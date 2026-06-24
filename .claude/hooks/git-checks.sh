#!/usr/bin/env bash
set -uo pipefail

INPUT=$(cat)
CMD=$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)
[ -z "$CMD" ] && exit 0

printf '%s' "$CMD" | grep -qiE '\bgit\b' || exit 0

UNQUOTED=$(printf '%s' "$CMD" | sed -E "s/'[^']*'//g; s/\"[^\"]*\"//g")

makes_commit() {
    printf '%s' "$UNQUOTED" | grep -qE '\bgit\b[^|;&]*\b(commit|merge)\b'
}

block() {
    printf 'BLOCKED by git-checks hook: %s\n' "$1" >&2
    printf 'This guard keeps every commit going through the repo pre-commit hooks. If this is a genuine emergency, ask the user to run it on the host.\n' >&2
    exit 2
}

if printf '%s' "$UNQUOTED" | grep -qE -- '--no-verify'; then
    block "--no-verify skips git hooks"
fi

if printf '%s' "$UNQUOTED" | grep -qE '\bgit\b[^|;&]*\b(commit|merge)\b[^|;&]*(^|[[:space:]])-[A-Za-z]*n[A-Za-z]*([[:space:]]|$)'; then
    block "git commit/merge -n skips the pre-commit hook"
fi

if printf '%s' "$UNQUOTED" | grep -qiE '\bgit\b[^|;&]*-c[[:space:]=]+core\.hookspath' && makes_commit; then
    block "inline -c core.hooksPath override skips the pre-commit hook for this commit"
fi

if printf '%s' "$CMD" | grep -qE '\b(GIT_CONFIG_(COUNT|KEY_[0-9]+|VALUE_[0-9]+))=' && makes_commit; then
    block "inline GIT_CONFIG_* override can disable the pre-commit hook for this commit"
fi

if printf '%s' "$CMD" | grep -qE '(^|[[:space:]])SKIP=' && makes_commit; then
    block "SKIP= drops one or more pre-commit hooks for this commit"
fi

if printf '%s' "$UNQUOTED" | grep -qE '\bgit\b[^|;&]*\b(commit-tree|fast-import)\b'; then
    block "git plumbing (commit-tree/fast-import) bypasses pre-commit entirely"
fi

exit 0
