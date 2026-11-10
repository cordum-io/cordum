#!/usr/bin/env python3
"""Credential sources reached transitively from GitHub workflow run steps.

Also holds the shell-text helpers that workflow_credential_guard.py shares.
"""
import re
def heredoc_lines(lines: list[str]) -> set[int]:
    marked: set[int] = set()
    delimiter: str | None = None
    for index, line in enumerate(lines):
        if delimiter is not None:
            marked.add(index)
            if line.strip() == delimiter:
                delimiter = None
            continue
        match = re.search(r"<<-?\s*['\"]?([A-Za-z_][A-Za-z0-9_]*)['\"]?", line)
        if match:
            delimiter = match.group(1)
    return marked
def shell_depth(lines: list[str], marked: set[int], stop: int) -> int:
    depth = 0
    for index, line in enumerate(lines[:stop]):
        if index in marked or not line.strip() or line.lstrip().startswith("#"):
            continue
        text = line.strip()
        if text in {"}", "fi", "done", "esac"}:
            depth -= 1
        if re.match(r"^(?:(?:function\s+)?[A-Za-z_]\w*\s*\(\)|function\s+[A-Za-z_]\w*)\s*\{$", text) or re.match(r"^(?:if\b.*;\s*then|(?:for|while|until|select)\b.*;\s*do|case\b.*\bin)$", text):
            depth += 1
        if depth < 0:
            return -1
    return depth
