#!/usr/bin/env python3
"""Add 503 ServiceUnavailable response refs to every operation in the
spec that lists a 500 but not a 503. Additive-only: any op that already
lists 503 is left untouched, and any op without a 500 is skipped (no
500 response is a spec bug the baseline already tolerates).
"""
from __future__ import annotations

import io
import pathlib
import sys

SPEC = pathlib.Path(__file__).resolve().parents[2] / "docs" / "api" / "openapi" / "cordum-api.yaml"

SERVICE_UNAVAILABLE_REF = "$ref: '#/components/responses/ServiceUnavailable'"


def process(lines: list[str]) -> tuple[list[str], int]:
    """Walk the paths: block, find each operation's responses: block,
    append 503 under it when 500 exists but 503 does not.

    Approach: streaming scan. Track current responses block via
    indentation. When we encounter a `responses:` line, remember its
    indent; buffer subsequent lines at a deeper indent. At the end of
    the block (dedent), inspect the buffered lines and emit a 503 entry
    when conditions are met, preserving indentation.
    """
    out: list[str] = []
    i = 0
    patched = 0
    in_paths = False

    while i < len(lines):
        line = lines[i]
        stripped = line.strip()

        if line.startswith("paths:"):
            in_paths = True
        elif in_paths and line.startswith("components:"):
            in_paths = False

        if not in_paths:
            out.append(line)
            i += 1
            continue

        # Detect start of a responses: block at any indent.
        if stripped == "responses:":
            base_indent = len(line) - len(line.lstrip(" "))
            out.append(line)
            i += 1

            # Detect the actual inner indent step from the first
            # non-blank child line. YAML specs in this tree use 2-space
            # indent but other projects (or future refactors) may not;
            # inferring keeps the patcher tolerant of style.
            inner_indent = base_indent + 2
            status_indent = inner_indent + 2
            for probe in lines[i:]:
                if probe.strip() == "":
                    continue
                probe_indent = len(probe) - len(probe.lstrip(" "))
                if probe_indent > base_indent:
                    inner_indent = probe_indent
                    status_indent = inner_indent + (inner_indent - base_indent)
                break

            block: list[str] = []
            while i < len(lines):
                nxt = lines[i]
                if nxt.strip() == "":
                    block.append(nxt)
                    i += 1
                    continue
                nxt_indent = len(nxt) - len(nxt.lstrip(" "))
                if nxt_indent <= base_indent:
                    break
                block.append(nxt)
                i += 1

            has_500 = False
            has_503 = False
            for b in block:
                s = b.strip()
                if s.startswith("'500':") or s.startswith('"500":') or s.startswith("500:"):
                    bi = len(b) - len(b.lstrip(" "))
                    if bi == inner_indent:
                        has_500 = True
                if s.startswith("'503':") or s.startswith('"503":') or s.startswith("503:"):
                    bi = len(b) - len(b.lstrip(" "))
                    if bi == inner_indent:
                        has_503 = True

            # Find last non-empty line in block for trailing insertion.
            if has_500 and not has_503:
                insert_idx = len(block)
                for k in range(len(block) - 1, -1, -1):
                    if block[k].strip() != "":
                        insert_idx = k + 1
                        break
                pad = " " * inner_indent
                sub_pad = " " * status_indent
                block[insert_idx:insert_idx] = [
                    f"{pad}'503':\n",
                    f"{sub_pad}{SERVICE_UNAVAILABLE_REF}\n",
                ]
                patched += 1

            out.extend(block)
            continue

        out.append(line)
        i += 1

    return out, patched


def main() -> int:
    text = SPEC.read_text(encoding="utf-8")
    lines = text.splitlines(keepends=True)
    patched_lines, n = process(lines)
    SPEC.write_text("".join(patched_lines), encoding="utf-8")
    print(f"Added '503' to {n} operations")
    return 0


if __name__ == "__main__":
    sys.exit(main())
