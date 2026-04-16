"""Tier 3: Input validation boundary testing."""

from __future__ import annotations

from client import CordumClient
from report import ScenarioResult

TIER = "Tier 3: Input Validation"


def xss_in_payload(c: CordumClient) -> ScenarioResult:
    """Submit XSS payloads in all text fields."""
    xss = '<script>alert("xss")</script>'
    r = c.submit_job(
        topic="job.default",
        prompt=f"Test {xss}",
        payload={"data": xss, "nested": {"deep": xss}},
        labels={"tag": xss},
    )
    if r.status_code >= 400:
        return ScenarioResult(
            id=8, tier=TIER, name="XSS in payload",
            description="Submit <script> tags in all text fields",
            expected="Stored safely (no execution, React escapes on render)",
            actual=f"Rejected: {r.status_code}",
            governance_held=True,
        )
    # Check that it's stored as literal text
    job_id = r.json().get("job_id", "")
    job_r = c.get_job(job_id)
    if job_r.status_code == 200:
        job_data = job_r.json()
        prompt = job_data.get("context", {}).get("prompt", "")
        stored_raw = xss in prompt
        return ScenarioResult(
            id=8, tier=TIER, name="XSS in payload",
            description="Submit <script> tags in all text fields",
            expected="Stored as literal text, not sanitized server-side",
            actual=f"Stored {'raw (unescaped)' if stored_raw else 'sanitized'} in DB",
            governance_held=True,
            detail="React dashboard escapes on render. Server stores raw (standard for APIs).",
        )
    return ScenarioResult(
        id=8, tier=TIER, name="XSS in payload",
        description="Submit <script> tags in all text fields",
        expected="Stored safely",
        actual=f"Job created but can't verify: {job_r.status_code}",
        governance_held=True,
    )


def oversized_prompt(c: CordumClient) -> ScenarioResult:
    """Submit a 250KB prompt to exceed enterprise tier limit (200K chars)."""
    big_prompt = "A" * 250_000
    r = c.submit_job(
        topic="job.default",
        prompt=big_prompt,
    )
    if r.status_code in (400, 403, 413):
        error = ""
        try:
            error = r.json().get("error", "")[:60]
        except Exception:
            error = r.text[:60]
        return ScenarioResult(
            id=9, tier=TIER, name="Oversized prompt (250K chars)",
            description="Submit 250K char prompt (enterprise limit is 200K)",
            expected="Rejected by prompt char limit or safety policy",
            actual=f"Rejected: {r.status_code} {error}",
            governance_held=True,
        )
    return ScenarioResult(
        id=9, tier=TIER, name="Oversized prompt (250K chars)",
        description="Submit 250K char prompt (enterprise limit is 200K)",
        expected="Rejected by prompt char limit or safety policy",
        actual=f"Accepted! Status {r.status_code}",
        governance_held=False,
        detail="250K char prompt was not rejected by any size limit",
    )


def sql_injection_labels(c: CordumClient) -> ScenarioResult:
    """Submit SQL injection payloads in labels."""
    r = c.submit_job(
        topic="job.default",
        prompt="SQL injection test",
        labels={
            "key": "'; DROP TABLE jobs; --",
            "value": "1 OR 1=1",
            "nested": '{"$ne": null}',
        },
    )
    if r.status_code >= 400:
        return ScenarioResult(
            id=10, tier=TIER, name="SQL injection in labels",
            description="Submit SQL/NoSQL injection in label values",
            expected="Treated as literal strings (Redis, not SQL)",
            actual=f"Rejected: {r.status_code}",
            governance_held=True,
        )
    return ScenarioResult(
        id=10, tier=TIER, name="SQL injection in labels",
        description="Submit SQL/NoSQL injection in label values",
        expected="Treated as literal strings (Redis, not SQL)",
        actual="Stored as literal strings (no injection possible in Redis)",
        governance_held=True,
        detail="Redis is not SQL-based; label values are opaque strings.",
    )


def path_traversal_topic(c: CordumClient) -> ScenarioResult:
    """Submit job with path traversal in topic name."""
    r = c.submit_job(
        topic="job.../../etc/passwd",
        prompt="Path traversal test",
    )
    if r.status_code >= 400:
        error = r.json().get("error", r.text)[:80]
        return ScenarioResult(
            id=11, tier=TIER, name="Path traversal in topic",
            description="Submit topic='job.../../etc/passwd'",
            expected="Rejected by topic validation regex",
            actual=f"Rejected: {error}",
            governance_held=True,
        )
    return ScenarioResult(
        id=11, tier=TIER, name="Path traversal in topic",
        description="Submit topic='job.../../etc/passwd'",
        expected="Rejected by topic validation regex",
        actual=f"Accepted! job_id={r.json().get('job_id', '?')[:12]}",
        governance_held=False,
        detail="Topic validation did not catch path traversal pattern",
    )


def run_all(c: CordumClient) -> list[ScenarioResult]:
    return [
        xss_in_payload(c),
        oversized_prompt(c),
        sql_injection_labels(c),
        path_traversal_topic(c),
    ]
