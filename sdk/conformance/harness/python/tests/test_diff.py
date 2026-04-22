"""Diff-engine parity tests.

These cases are mirrored by the Go (`diff_test.go`) and TypeScript
(`diff.test.ts`) harnesses. Step 9's parity runner cross-runs the
same cases through all three engines and fails if any verdicts
disagree — that's the safety net against silent grading divergence
across languages.
"""

from __future__ import annotations

import sys
from pathlib import Path

_HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(_HERE.parent))

import pytest  # noqa: E402 — path prep above

from conformance import _diff  # noqa: E402


@pytest.mark.parametrize(
    "actual,expected,want_pass",
    [
        ("hello", "hello", True),
        ("hello", "world", False),
        ("whatever", "$any$", True),
        (42, "$any$", True),
        ({"k": "v"}, "$any$", True),
        ("2026-01-01T00:00:00Z", "$timestamp$", True),
        ("not-a-date", "$timestamp$", False),
        (42, "$int$", True),
        (3.14, "$int$", False),
        ("550e8400-e29b-41d4-a716-446655440000", "$uuid$", True),
        ("not-a-uuid", "$uuid$", False),
        ("x", "$zzz$", False),
    ],
)
def test_diff_primitives(actual, expected, want_pass):
    try:
        _diff.diff(actual, expected, "$")
    except _diff.DiffError:
        assert not want_pass, "expected pass, got error"
        return
    assert want_pass, "expected fail, got pass"


def test_nested_object_extra_keys_ok():
    actual = {"id": "abc", "name": "alpha", "status": "active", "extra": "ignored"}
    expected = {"name": "alpha", "status": "active", "id": "$any$"}
    _diff.diff(actual, expected, "$")


def test_array_length_mismatch():
    with pytest.raises(_diff.DiffError):
        _diff.diff(["a", "b", "c"], ["a", "b"], "$")


def test_object_missing_key():
    with pytest.raises(_diff.DiffError):
        _diff.diff({"a": 1}, {"a": 1, "b": "$any$"}, "$")


def test_select_json_path_variants():
    root = {
        "id": "xyz",
        "items": [
            {"id": "item-0"},
            {"id": "item-1"},
        ],
    }
    assert _diff.select_json_path(root, "$.id") == "xyz"
    assert _diff.select_json_path(root, "$.items[0].id") == "item-0"
    assert _diff.select_json_path(root, "$.items[1].id") == "item-1"


def test_resolve_vars_substitution():
    vars_bag = {"agentId": "agent-0001", "tenant": "default"}
    in_value = {
        "id": "$vars.agentId",
        "tenant": "$vars.tenant",
        "nested": {"copy": "$vars.agentId"},
        "list": ["$vars.agentId", "literal"],
    }
    out = _diff.resolve_vars(in_value, vars_bag)
    assert out["id"] == "agent-0001"
    assert out["nested"]["copy"] == "agent-0001"
    assert out["list"] == ["agent-0001", "literal"]


def test_infer_error_status_table():
    assert _diff.infer_error_status("AuthenticationError") == 401
    assert _diff.infer_error_status("AuthorizationError") == 403
    assert _diff.infer_error_status("NotFoundError") == 404
    assert _diff.infer_error_status("ValidationError") == 400
    assert _diff.infer_error_status("ConflictError") == 409
    assert _diff.infer_error_status("RateLimitError") == 429
    assert _diff.infer_error_status("ServerError") == 500
    assert _diff.infer_error_status("NetworkError") == 0
    assert _diff.infer_error_status("TimeoutError") == 0


def test_int_rejects_bool():
    # Python treats bool as an int subclass; $int$ must reject.
    with pytest.raises(_diff.DiffError):
        _diff.diff(True, "$int$", "$")
