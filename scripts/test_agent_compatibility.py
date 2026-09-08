#!/usr/bin/env python3
"""Exercise each layout check in a temporary Git repository."""

import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location(
    "agent_compatibility", Path(__file__).with_name("check-agent-compatibility.py")
)
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)

class LayoutTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        subprocess.run(["git", "init", "--quiet"], cwd=self.root, check=True)
        self.write("AGENTS.md", "# Root\n")
        self.link("CLAUDE.md", "AGENTS.md")

    def write(self, name, text="x\n"):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def link(self, name, target):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.symlink_to(target)

    def errors(self):
        return checker.check(self.root)[0]

    def assert_error(self, fragment):
        found = self.errors()
        self.assertTrue(any(fragment in e for e in found), f"{fragment!r} not in {found}")

    def test_bare_repository_passes(self):
        self.assertEqual([], self.errors())

    def test_instruction_pairs(self):
        self.write("pkg/AGENTS.md")
        self.assert_error("pkg/CLAUDE.md")
        self.write("pkg/CLAUDE.md")
        self.assert_error("pkg/CLAUDE.md")
        (self.root / "pkg/CLAUDE.md").unlink()
        self.link("pkg/CLAUDE.md", "AGENTS.md")
        self.assertEqual([], self.errors())
        (self.root / "pkg/AGENTS.md").unlink()
        self.assert_error("pkg/AGENTS.md")

    def test_ignored_files_are_skipped(self):
        self.write(".gitignore", "scratch/\n")
        self.write("scratch/AGENTS.md")
        self.assertEqual([], self.errors())

    def test_any_link_layout_into_shared_is_accepted(self):
        self.write(".agents/rules/a.md")
        self.write(".agents/skills/s/SKILL.md")
        self.write(".agents/skills/s/agents/claude.md")
        self.link(".claude/rules", "../.agents/rules")
        self.link(".claude/skills/s", "../../.agents/skills/s")
        self.link(".claude/agents/s.md", "../../.agents/skills/s/agents/claude.md")
        self.write(".claude/agents/native-only.md")
        self.assertEqual([], self.errors())

    def test_rejects_bad_links_in_agent_directories(self):
        self.write(".agents/rules/a.md")
        cases = {
            ".claude/rules/broken.md": "../../.agents/rules/missing.md",
            ".claude/rules/absolute.md": str(self.root / ".agents/rules/a.md"),
            ".codex/outside.md": "../../outside.md",
            ".agents/hooks/native.json": "../../.claude/settings.json",
        }
        self.write("../outside.md")
        self.write(".claude/settings.json")
        for name, target in cases.items():
            with self.subTest(name=name):
                self.link(name, target)
                self.assert_error(name)
                (self.root / name).unlink()

    def test_codex_config_file_must_resolve_to_a_regular_file(self):
        self.write(".codex/config.toml", '[agents.gone]\nconfig_file = "roles/gone.toml"\n')
        self.assert_error("[agents.gone]")
        self.write(".codex/roles/gone.toml")
        self.assertEqual([], self.errors())
        (self.root / ".codex/roles/gone.toml").unlink()
        self.write(".agents/skills/s/agents/codex.toml")
        self.link(".codex/roles/gone.toml", "../../.agents/skills/s/agents/codex.toml")
        self.assert_error("[agents.gone]")
        self.write(".codex/config.toml", '[agents.shared]\nconfig_file = "../.agents/skills/s/agents/codex.toml"\n')
        self.assertEqual([], self.errors())

    def test_navigation_links_resolve(self):
        self.write(".agents/rules/git.md")
        self.write(".agents/skills/agent-files/SKILL.md", "[gone](docs/gone.md) [ok](../../rules/git.md) [ext](https://x) `f[T](x)`\n"
                                        "```\n[code](not/a/link.md)\n```\n")
        self.assertEqual([".agents/skills/agent-files/SKILL.md: broken link docs/gone.md"], self.errors())


if __name__ == "__main__":
    unittest.main()
