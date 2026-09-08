#!/usr/bin/env python3
"""Check the agent-file layout shared by Claude Code and Codex.

The checks validate what exists; they do not require any file beyond the
instruction pairs:

1. Every AGENTS.md is a regular file with a sibling CLAUDE.md link to it.
2. Every symbolic link under .agents, .claude, and .codex is relative and
   resolves inside the repository. Links under .agents never resolve into a
   harness directory.
3. Every Codex `[agents.<name>] config_file` resolves to a regular file.
4. Relative links in the root AGENTS.md and the top-level .agents files resolve.
"""

import os
import re
import subprocess
import sys
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SHARED = Path(".agents")
NATIVE = (Path(".claude"), Path(".codex"))
CODEX_CONFIG = Path(".codex/config.toml")


def repo_entries(root):
    """Tracked and untracked paths that Git does not ignore, plus their parents."""
    out = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=root
    )
    entries = set()
    for raw in out.split(b"\0"):
        if raw:
            path = Path(os.fsdecode(raw))
            entries.add(path)
            entries.update(path.parents)
    entries.discard(Path("."))
    return entries


def check_instruction_pairs(root, entries):
    errors = []
    dirs = {p.parent for p in entries if p.name in ("AGENTS.md", "CLAUDE.md")}
    for directory in sorted(dirs):
        agents = root / directory / "AGENTS.md"
        claude = root / directory / "CLAUDE.md"
        if agents.is_symlink() or not agents.is_file():
            errors.append(f"{directory / 'AGENTS.md'}: must be a regular file")
        if not (claude.is_symlink() and os.readlink(claude) == "AGENTS.md"):
            errors.append(f"{directory / 'CLAUDE.md'}: must be a symbolic link to AGENTS.md")
    return errors, len(dirs)


def check_links(root):
    errors = []
    count = 0
    for base in (SHARED, *NATIVE):
        if not (root / base).is_dir():
            continue
        for directory, children, files in os.walk(root / base, followlinks=False):
            for name in children + files:
                link = Path(directory) / name
                if not link.is_symlink():
                    continue
                count += 1
                rel = link.relative_to(root)
                if Path(os.readlink(link)).is_absolute():
                    errors.append(f"{rel}: symbolic link must be relative")
                    continue
                try:
                    target = link.resolve(strict=True)
                except (OSError, RuntimeError) as error:
                    errors.append(f"{rel}: broken symbolic link ({error})")
                    continue
                if not target.is_relative_to(root):
                    errors.append(f"{rel}: symbolic link leaves the repository")
                elif base == SHARED and any(
                    target.is_relative_to(root / native) for native in NATIVE
                ):
                    errors.append(f"{rel}: shared content must not link into a harness directory")
    return errors, count


def check_codex_roles(root):
    errors = []
    config = root / CODEX_CONFIG
    if not config.is_file():
        return errors, 0
    try:
        roles = tomllib.loads(config.read_text()).get("agents", {})
    except tomllib.TOMLDecodeError as error:
        return [f"{CODEX_CONFIG}: {error}"], 0
    for name, role in roles.items():
        value = role.get("config_file")
        if value is None:
            continue
        path = config.parent / value
        if path.is_symlink() or not path.is_file():
            errors.append(f"{CODEX_CONFIG}: [agents.{name}] config_file must be a regular file")
    return errors, len(roles)


def check_nav_links(root):
    errors = []
    files = [root / "AGENTS.md", *sorted((root / SHARED).glob("*.md"))]
    for path in files:
        if not path.is_file():
            continue
        text = re.sub(r"```.*?```", "", path.read_text(), flags=re.DOTALL)
        for match in re.finditer(r"\]\(([^)\s#]+)(?:#[^)]*)?\)", text):
            target = match[1]
            if re.match(r"[a-z]+:", target):
                continue
            if not (path.parent / target).exists():
                errors.append(f"{path.relative_to(root)}: broken link {target}")
    return errors


def check(root):
    root = Path(root).resolve()
    errors, pairs = check_instruction_pairs(root, repo_entries(root))
    link_errors, links = check_links(root)
    role_errors, roles = check_codex_roles(root)
    errors += link_errors + role_errors + check_nav_links(root)
    counts = {"instruction pairs": pairs, "symbolic links": links, "Codex roles": roles}
    return errors, counts


if __name__ == "__main__":
    try:
        problems, counts = check(ROOT)
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"Agent compatibility check failed: {error}", file=sys.stderr)
        sys.exit(1)
    for problem in problems:
        print(problem, file=sys.stderr)
    if problems:
        sys.exit(1)
    summary = ", ".join(f"{n} {label}" for label, n in counts.items())
    print(f"Agent compatibility checks passed: {summary}.")
