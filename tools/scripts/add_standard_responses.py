#!/usr/bin/env python3
"""Add 429 (RateLimited) and 413 (PayloadTooLarge) responses to every
ACTIVE operation in the spec that lacks those two codes.

Both statuses are emitted by the gateway's global middleware
(rateLimit, maxBody) — they wrap the entire API surface, so every op
must surface them per the DoD "All error codes documented per
endpoint". Additive change — oasdiff classifies it as
`added-response`, never `removed-response`.

The gate is now "operation has any responses block" rather than "lists
500" because some ops legitimately omit 500 yet still pass through the
middleware. Deprecated operations are skipped (they're frozen for SDK
continuity and shouldn't sprout new responses post-deprecation).

Runs after `add_503_responses.py` (same shape, same idempotence).
"""
from __future__ import annotations

import pathlib
import sys

SPEC = pathlib.Path(__file__).resolve().parents[2] / "docs" / "api" / "openapi" / "cordum-api.yaml"

RATE_LIMITED_REF = "$ref: '#/components/responses/RateLimited'"
PAYLOAD_TOO_LARGE_REF = "$ref: '#/components/responses/PayloadTooLarge'"


def process(lines: list[str]) -> tuple[list[str], int, int]:
    out: list[str] = []
    i = 0
    patched_429 = 0
    patched_413 = 0
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

        if stripped == "responses:":
            base_indent = len(line) - len(line.lstrip(" "))
            # Detect actual inner indent from the first non-blank child
            # so the patcher tolerates non-2-space specs.
            inner_indent = base_indent + 2
            status_indent = inner_indent + 2
            for probe in lines[i + 1:]:
                if probe.strip() == "":
                    continue
                probe_indent = len(probe) - len(probe.lstrip(" "))
                if probe_indent > base_indent:
                    inner_indent = probe_indent
                    status_indent = inner_indent + (inner_indent - base_indent)
                break
            # Deprecation window: scan the already-emitted lines for the
            # CURRENT operation, regardless of whether the deprecated
            # flag appeared before or after other op fields. Walk back
            # while indent > base_indent (still inside the op body);
            # stop on the first line at or shallower than base_indent
            # (that's the parent op key or a sibling op's end). If any
            # line at base_indent level says 'deprecated: true', skip.
            deprecated_window = False
            for back in range(len(out) - 1, -1, -1):
                bline = out[back]
                bstripped = bline.strip()
                if not bstripped:
                    continue
                bindent = len(bline) - len(bline.lstrip(" "))
                if bindent < base_indent:
                    break
                if bindent == base_indent and bstripped == "deprecated: true":
                    deprecated_window = True
                    break
            out.append(line)
            i += 1

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

            # Gate on responses block presence rather than 500 — the
            # cross-cutting middleware applies to every active op
            # whether or not 500 is documented.
            has_429 = False
            has_413 = False
            for b in block:
                s = b.strip()
                bi = len(b) - len(b.lstrip(" "))
                if bi != inner_indent:
                    continue
                if s.startswith("'429':") or s.startswith('"429":') or s.startswith("429:"):
                    has_429 = True
                if s.startswith("'413':") or s.startswith('"413":') or s.startswith("413:"):
                    has_413 = True

            if not deprecated_window:
                pad = " " * inner_indent
                sub_pad = " " * status_indent
                additions: list[str] = []
                if not has_429:
                    additions.extend([
                        f"{pad}'429':\n",
                        f"{sub_pad}{RATE_LIMITED_REF}\n",
                    ])
                    patched_429 += 1
                if not has_413:
                    additions.extend([
                        f"{pad}'413':\n",
                        f"{sub_pad}{PAYLOAD_TOO_LARGE_REF}\n",
                    ])
                    patched_413 += 1
                if additions:
                    insert_idx = len(block)
                    for k in range(len(block) - 1, -1, -1):
                        if block[k].strip() != "":
                            insert_idx = k + 1
                            break
                    block[insert_idx:insert_idx] = additions

            out.extend(block)
            continue

        out.append(line)
        i += 1

    return out, patched_429, patched_413


def main() -> int:
    text = SPEC.read_text(encoding="utf-8")
    lines = text.splitlines(keepends=True)
    patched, n429, n413 = process(lines)
    SPEC.write_text("".join(patched), encoding="utf-8")
    print(f"Added '429' to {n429} operations; '413' to {n413} operations")
    return 0


if __name__ == "__main__":
    sys.exit(main())
