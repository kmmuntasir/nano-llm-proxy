#!/usr/bin/env python3
"""Sanitize captured agent-request fixtures before they are committed.

The recordings under internal/gateway/testdata/zai/agents/ are raw captures of
real coding-agent sessions. Two things in them do not belong in a public
repository:

  1. The operator's home directory paths (/home/<user>/...).
  2. The operator's personal instruction files, which the agents inline into
     the request (Claude Code injects CLAUDE.md verbatim; pi injects its own
     system prompt and tool docs).

The key is already redacted by the capture step, but neither of the above was.
This script removes them, and is safe to run over an already-clean tree.

What is deliberately preserved: the shape of an agent request — the large
system message, the tool schemas, the <system-reminder> block, and the
synthetic probe prompt. Those are the reasons the fixtures exist.

Usage:
    scripts/sanitize-fixtures.py [--check] [DIR ...]

    --check   exit 1 if anything would change; do not write (for CI)

Defaults to internal/gateway/testdata/zai/agents/.
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_DIRS = [ROOT / "internal" / "gateway" / "testdata" / "zai" / "agents"]

# Home directory of whoever ran the recorder: /home/<user>/... -> /home/<user>/...
HOME_PATH = re.compile(r"/home/[^/\s\"'\\]+")

# The block Claude Code prepends, which inlines the operator's CLAUDE.md.
# Matched on the "Contents of ... (user's private global instructions ...)"
# marker, through to the closing tag, so we replace the whole injection.
SYSTEM_REMINDER = re.compile(
    r"<system-reminder>.*?Contents of .*?</system-reminder>",
    re.DOTALL,
)

REMINDER_REPLACEMENT = (
    "<system-reminder>\n"
    "Codebase and user instructions are shown below. Be sure to adhere to these "
    "instructions. IMPORTANT: These instructions OVERRIDE any default behavior "
    "and you MUST follow them exactly as written.\n\n"
    "Contents of /home/<user>/.claude/CLAUDE.md (redacted: operator's private "
    "global instructions):\n\n"
    "<redacted for publication>\n"
    "</system-reminder>"
)

# The vendor system prompt that Claude Code and pi prepend. It is largely
# product documentation plus the operator's local paths; replace the whole
# message with a labelled stand-in of the same kind and rough size.
SYSTEM_PLACEHOLDER = (
    "[redacted for publication: the coding agent's built-in system prompt. "
    "Retained in outline because its size and structure are the point of this "
    "fixture — a real agent prepends a multi-kilobyte system message ahead of "
    "the conversation, and the gateway has to pass it through untouched.]"
)


def scrub_text(text: str) -> str:
    """Redact personal paths and inlined private instructions from one string."""
    if not isinstance(text, str):
        return text
    if "Contents of " in text and "<system-reminder>" in text:
        text = SYSTEM_REMINDER.sub(REMINDER_REPLACEMENT, text)
    if SYSTEM_PLACEHOLDER not in text:
        # A bare system message with no reminder wrapper: the agent's own
        # system prompt. Replace wholesale if it still names a local path or
        # is plainly the vendor prompt.
        if re.search(r"/home/[^/\s\"'\\]+|/Users/[^/\s\"'\\]+", text):
            text = SYSTEM_PLACEHOLDER
    return HOME_PATH.sub("/home/<user>", text)


def scrub(node):
    """Recursively scrub every string in a parsed-JSON structure."""
    if isinstance(node, str):
        return scrub_text(node)
    if isinstance(node, list):
        return [scrub(item) for item in node]
    if isinstance(node, dict):
        return {key: scrub(value) for key, value in node.items()}
    return node


def process(path: Path, check: bool) -> bool:
    """Sanitize one file. Returns True if it changed (or would change)."""
    raw = path.read_text(encoding="utf-8")
    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError:
        # Not JSON (.headers, .txt, .sse): scrub as plain text.
        cleaned = HOME_PATH.sub("/home/<user>", raw)
        if cleaned == raw:
            return False
        if not check:
            path.write_text(cleaned, encoding="utf-8")
        return True

    original = json.loads(raw)
    cleaned_obj = scrub(original)
    # Report only real content changes. Comparing the parsed structures (not
    # the serialized text) keeps an already-clean file that merely uses
    # different indentation from being rewritten on every run.
    if cleaned_obj == original:
        return False
    cleaned = json.dumps(cleaned_obj, indent=1, ensure_ascii=False) + "\n"
    if not check:
        path.write_text(cleaned, encoding="utf-8")
    return True


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true",
                        help="report without writing; exit 1 if changes needed")
    parser.add_argument("dirs", nargs="*", type=Path, help="directories to sanitize")
    args = parser.parse_args()

    dirs = args.dirs or DEFAULT_DIRS
    changed: list[Path] = []
    for directory in dirs:
        if not directory.is_dir():
            print(f"error: not a directory: {directory}", file=sys.stderr)
            return 2
        for path in sorted(directory.rglob("*")):
            if path.is_file() and process(path, args.check):
                changed.append(path)

    if not changed:
        print("fixtures clean — nothing to sanitize")
        return 0

    verb = "would sanitize" if args.check else "sanitized"
    for path in changed:
        print(f"  {verb}: {path.relative_to(ROOT)}")
    print(f"{verb} {len(changed)} file(s)")
    return 1 if args.check else 0


if __name__ == "__main__":
    sys.exit(main())
