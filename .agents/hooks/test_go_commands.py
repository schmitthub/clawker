"""Check hook decisions with input data; never execute the supplied commands."""

import json
from pathlib import Path
import subprocess
import unittest


HOOK = Path(__file__).with_name("go-commands.sh")


class GoCommandGuardTest(unittest.TestCase):
    def decision(self, command):
        result = subprocess.run(
            ["bash", str(HOOK)],
            input=json.dumps({"tool_input": {"command": command}}),
            text=True,
            capture_output=True,
            check=True,
        )
        if not result.stdout.strip():
            return None
        return json.loads(result.stdout)["hookSpecificOutput"]["permissionDecision"]

    def test_blocks_test_invocations(self):
        commands = [
            "go test ./...",
            "go test -race ./... -count=1",
            "GOFLAGS=-mod=readonly go test ./...",
            "env GOFLAGS=-mod=readonly go test ./...",
            "pwd && go test ./...",
            "go test ./... | cat",
            "go test " + "\\" + "\n./...",
            "command go test './...'",
            "bash -c 'go test ./...'",
            "echo $(go test ./...)",
            'echo "$(go test ./...)"',
            "echo `go test ./...`",
            "cat <<EOF\n$(go test ./...)\nEOF",
            "bash <<'EOF'\ngo test ./...\nEOF",
            "env -u GOFLAGS go test ./...",
            "eval 'go test ./...'",
            "bash <<< 'go test ./...'",
            "> /tmp/output go test ./...",
            "if go test ./...; then echo done; fi",
            "cat <<'EOF'\nexample\nEOF\ngo test ./...",
        ]
        for command in commands:
            with self.subTest(command=command):
                self.assertEqual(self.decision(command), "deny")

    def test_allows_document_text_and_targeted_commands(self):
        commands = [
            "cat > AGENTS.md <<'EOF'\nNever run `go test ./...`.\nEOF",
            'cat <<"EOF"\ngo test ./...\nEOF',
            "cat <<-'EOF'\n\tgo test ./...\n\tEOF",
            "python3 - <<'PY'\ntext = 'go test ./...'\nprint(text)\nPY",
            "printf '%s\\n' 'go test ./...'",
            "echo go test ./...",
            "printf '%s' ';' go test ./...",
            "echo '$(go test ./...)'",
            "cat <<'EOF'\n$(go test ./...)\nEOF",
            "cat <<EOF\ngo test ./...\nEOF",
            "rg 'go test ./...' AGENTS.md",
            "# go test ./...\npwd",
            "# $(go test ./...)\npwd",
            "go test ./internal/config/...",
            "go test ./...example",
            "make test",
        ]
        for command in commands:
            with self.subTest(command=command):
                self.assertIsNone(self.decision(command))


if __name__ == "__main__":
    unittest.main()
