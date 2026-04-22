"""Shared grading engine.

Verdicts here must match the Go and TypeScript harnesses byte-for-byte
— the step-9 parity runner asserts this invariant. Wildcards:

    $any$          any value passes
    $timestamp$    string parsable as RFC3339 / ISO-8601
    $uuid$         string matching UUID v4 shape
    $int$          integer value (JSON number must round-trip to int)
    $request_id$   opaque request id (behaves like $any$ for now)
"""

from __future__ import annotations

import re
from datetime import datetime
from typing import Any

_WILDCARDS = {"$any$", "$timestamp$", "$uuid$", "$int$", "$request_id$"}

_UUID_RE = re.compile(
    r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
)


class DiffError(Exception):
    """Raised by diff() on the first divergence. .path is the
    fixture-scoped dotted path pointing at the failing field."""

    def __init__(self, path: str, message: str):
        super().__init__(f"{path}: {message}")
        self.path = path
        self.message = message


def is_wildcard(s: Any) -> bool:
    return isinstance(s, str) and s in _WILDCARDS


def diff(actual: Any, expected: Any, path: str = "$") -> None:
    """Compare actual against expected, applying wildcard rules.
    Raises DiffError on the first divergence, returns None on pass."""
    if is_wildcard(expected):
        _check_wildcard(actual, expected, path)
        return
    if isinstance(expected, dict):
        if not isinstance(actual, dict):
            raise DiffError(path, f"want object, got {type(actual).__name__}")
        for k in sorted(expected.keys()):
            if k not in actual:
                raise DiffError(f"{path}.{k}", "expected key missing from response")
            diff(actual[k], expected[k], f"{path}.{k}")
        return
    if isinstance(expected, list):
        if not isinstance(actual, list):
            raise DiffError(path, f"want array, got {type(actual).__name__}")
        if len(actual) != len(expected):
            raise DiffError(path, f"length mismatch (want {len(expected)}, got {len(actual)})")
        for i, item in enumerate(expected):
            diff(actual[i], item, f"{path}[{i}]")
        return
    if expected is None:
        if actual is not None:
            raise DiffError(path, f"want null, got {actual!r}")
        return
    if isinstance(expected, bool):
        if actual is not expected:
            raise DiffError(path, f"want {expected!r}, got {actual!r}")
        return
    if isinstance(expected, (int, float, str)):
        if actual != expected:
            raise DiffError(path, f"want {expected!r}, got {actual!r}")
        return
    raise DiffError(path, f"unsupported expected type {type(expected).__name__}")


def _check_wildcard(actual: Any, token: str, path: str) -> None:
    if token in ("$any$", "$request_id$"):
        return
    if token == "$timestamp$":
        if not isinstance(actual, str):
            raise DiffError(path, f"$timestamp$ expects string, got {type(actual).__name__}")
        try:
            _parse_iso(actual)
        except ValueError as exc:
            raise DiffError(path, f"{actual!r} is not an RFC3339 timestamp: {exc}") from None
        return
    if token == "$uuid$":
        if not isinstance(actual, str) or not _UUID_RE.match(actual):
            raise DiffError(path, f"{actual!r} is not a UUID")
        return
    if token == "$int$":
        if isinstance(actual, bool):
            raise DiffError(path, "$int$ expects integer, got bool")
        if isinstance(actual, int):
            return
        if isinstance(actual, float) and actual.is_integer():
            return
        raise DiffError(path, f"$int$ expects integer, got {actual!r}")
    raise DiffError(path, f"unknown wildcard {token!r}")


def _parse_iso(s: str) -> datetime:
    # RFC3339 uses 'Z' or '+HH:MM'. Python's fromisoformat handles the
    # latter on 3.11+; for 3.9/3.10 we normalize the trailing Z.
    normalized = s
    if normalized.endswith("Z"):
        normalized = normalized[:-1] + "+00:00"
    return datetime.fromisoformat(normalized)


def resolve_vars(value: Any, vars_bag: dict[str, Any]) -> Any:
    """Substitute ``$vars.key`` placeholders in nested structures.
    Returns a new structure; the input is not mutated."""
    if isinstance(value, str):
        if value.startswith("$vars."):
            key = value[len("$vars.") :]
            return vars_bag.get(key, "")
        return value
    if isinstance(value, dict):
        return {k: resolve_vars(v, vars_bag) for k, v in value.items()}
    if isinstance(value, list):
        return [resolve_vars(v, vars_bag) for v in value]
    return value


def select_json_path(root: Any, expr: str) -> Any:
    """Minimal JSONPath selector matching the Go harness.

    Supports ``$`` (root), ``$.foo``, ``$.foo.bar``, ``$.items[0]``,
    ``$.items[0].id``. Raises DiffError on malformed expressions or
    missing fields.
    """
    if not expr.startswith("$"):
        raise DiffError(expr, "path must start with $")
    if expr == "$":
        return root
    cur: Any = root
    parts = expr[1:].split(".")
    for raw in parts:
        if not raw:
            continue
        bracket = raw.find("[")
        if bracket >= 0 and raw.endswith("]"):
            name = raw[:bracket]
            idx_str = raw[bracket + 1 : -1]
            if name:
                if not isinstance(cur, dict):
                    raise DiffError(raw, f"cannot index {name} on {type(cur).__name__}")
                cur = cur.get(name)
            if not isinstance(cur, list):
                raise DiffError(raw, "not an array")
            try:
                idx = int(idx_str)
            except ValueError as exc:
                raise DiffError(raw, f"bad array index {idx_str}") from exc
            if idx < 0 or idx >= len(cur):
                raise DiffError(raw, f"index {idx} out of range (len={len(cur)})")
            cur = cur[idx]
            continue
        if not isinstance(cur, dict):
            raise DiffError(raw, f"cannot descend into {raw} on {type(cur).__name__}")
        cur = cur.get(raw)
    return cur


def infer_error_status(class_name: str) -> int:
    """Class name -> canonical HTTP status. Mirrors the Go harness."""
    return {
        "AuthenticationError": 401,
        "AuthorizationError": 403,
        "NotFoundError": 404,
        "ValidationError": 400,
        "ConflictError": 409,
        "RateLimitError": 429,
        "ServerError": 500,
    }.get(class_name, 0)
