"""Inspect shell words without executing the command supplied to the hook."""

import io
import json
from pathlib import PurePosixPath
import re
import shlex
import sys


SEPARATORS = {";", "&", "&&", "||", "|", "(", ")", "{", "}"}
PREFIXES = {"env", "command", "exec", "sudo", "time", "!", "then", "do", "else",
            "if", "elif", "while", "until"}
SHELLS = {"sh", "bash", "zsh", "dash"}


def unquote(word):
    return shlex.split(word)[0] if word[:1] in {"'", '"'} else word


def substitutions(text, heredoc=False):
    """Read executable substitutions while preserving literal single quotes."""
    index, quote = 0, None
    while index < len(text):
        char = text[index]
        if char == "\\" and quote != "'":
            index += 2
            continue
        if not heredoc and quote is None and char == "#" and (index == 0 or text[index - 1].isspace()):
            newline = text.find("\n", index)
            index = len(text) if newline < 0 else newline + 1
            continue
        if not heredoc and char in "\"'" and (quote is None or quote == char):
            quote = None if quote else char
            index += 1
            continue
        if quote != "'" and text.startswith("$(", index):
            start = index + 2
            end, depth, inner_quote = start, 1, None
            while end < len(text) and depth:
                current = text[end]
                if current == "\\" and inner_quote != "'":
                    end += 2
                    continue
                if current in "\"'" and (inner_quote is None or inner_quote == current):
                    inner_quote = None if inner_quote else current
                elif inner_quote is None:
                    depth += (current == "(") - (current == ")")
                end += 1
            if depth:
                raise ValueError("Unclosed command substitution")
            yield text[start:end - 1]
            index = end
            continue
        if quote != "'" and char == "`":
            end = index + 1
            while end < len(text) and text[end] != "`":
                end += 2 if text[end] == "\\" else 1
            if end >= len(text):
                raise ValueError("Unclosed command substitution")
            yield text[index + 1:end]
            index = end + 1
            continue
        index += 1


def shell_lines(command):
    """Keep heredoc data separate from the shell command that consumes it."""
    lines = iter(command.splitlines(keepends=True))
    for line in lines:
        while True:
            try:
                lexer = shlex.shlex(io.StringIO(line), posix=False, punctuation_chars=True)
                lexer.whitespace_split = True
                words = list(lexer)
                break
            except ValueError:
                continuation = next(lines, None)
                if continuation is None:
                    raise
                line += continuation
        bodies = []
        for index, word in enumerate(words[:-1]):
            if word != "<<":
                continue
            delimiter = words[index + 1]
            strip_tabs = delimiter.startswith("-")
            if strip_tabs:
                delimiter = delimiter[1:]
            quoted = any(char in delimiter for char in "\"'\\")
            delimiter = shlex.split(delimiter)[0]
            body = []
            for data in lines:
                candidate = data.lstrip("\t") if strip_tabs else data
                if candidate.rstrip("\r\n") == delimiter:
                    break
                body.append(data)
            bodies.append(("".join(body), quoted))
        yield line, words, bodies


def blocked(command):
    if not all(part in command for part in ("go", "test", "./...")):
        return False
    # Shell line continuations join words before argument parsing.
    command = command.replace("\\\n", "")
    for line, words, bodies in shell_lines(command):
        if any(blocked(part) for part in substitutions(line)):
            return True
        for body, quoted in bodies:
            if not quoted and any(blocked(part) for part in substitutions(body, heredoc=True)):
                return True
        start = True
        skip = -1
        for index, raw_word in enumerate(words):
            if index == skip:
                continue
            if raw_word in SEPARATORS:
                start = True
                continue
            if not start:
                continue
            if raw_word in {"<", ">", ">>", "<<", "<<<"}:
                skip = index + 1
                continue
            word = unquote(raw_word)
            if word in PREFIXES or re.match(r"^[A-Za-z_][A-Za-z_0-9]*=", word):
                continue
            if word.startswith("-"):
                if word in {"-u", "--unset", "-C", "--chdir"}:
                    skip = index + 1
                continue
            start = False
            name = PurePosixPath(word).name
            arguments = []
            for argument in words[index + 1:]:
                if argument in SEPARATORS:
                    break
                arguments.append(unquote(argument))
            if name == "go" and arguments[:1] == ["test"] and "./..." in arguments[1:]:
                return True
            if name == "eval" and blocked(" ".join(arguments)):
                return True
            if name in SHELLS:
                for offset, argument in enumerate(arguments[:-1]):
                    if argument.startswith("-") and "c" in argument[1:]:
                        if blocked(arguments[offset + 1]):
                            return True
                    if argument == "<<<" and blocked(arguments[offset + 1]):
                        return True
                if any(blocked(body) for body, _quoted in bodies):
                    return True
    return False


def main():
    request = json.load(sys.stdin)
    command = request.get("tool_input", {}).get("command", "")
    try:
        deny = blocked(command)
    except ValueError:
        # Do not approve text that the shell-word reader cannot check.
        deny = True
    if deny:
        print(json.dumps({"hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "deny",
            "permissionDecisionReason": (
                "The Go command guard blocked an unrestricted test invocation "
                "or shell text it could not parse. Use make test or targeted packages."
            ),
        }}))


if __name__ == "__main__":
    main()
