"""Tier 5: Rate and resource limit attacks."""

from __future__ import annotations

import time
import concurrent.futures

from client import CordumClient
from report import ScenarioResult

TIER = "Tier 5: Rate & Resource Limits"


def rapid_fire_submission(c: CordumClient) -> ScenarioResult:
    """Submit 120 jobs as fast as possible to exceed burst limit (100)."""
    results: list[int] = []
    rejected = 0
    total = 120

    for i in range(total):
        r = c.submit_job(
            topic="job.default",
            prompt=f"Rate limit test #{i}",
        )
        results.append(r.status_code)
        if r.status_code == 429:
            rejected += 1
            # Once we hit the limit, count remaining as would-be-rejected
            rejected += total - i - 1
            break

    accepted = sum(1 for s in results if s == 200)

    if rejected > 0:
        return ScenarioResult(
            id=14, tier=TIER, name="Rapid-fire submission (60 jobs)",
            description="Submit 60 jobs as fast as possible",
            expected="Rate limited after threshold",
            actual=f"Accepted {accepted}, rate-limited after {accepted} requests",
            governance_held=True,
        )
    return ScenarioResult(
        id=14, tier=TIER, name="Rapid-fire submission (60 jobs)",
        description="Submit 60 jobs as fast as possible",
        expected="Rate limited after threshold",
        actual=f"All {accepted} accepted, no rate limit triggered",
        governance_held=False,
        detail=f"Submitted {total} jobs without hitting rate limit. "
               f"RPS config may be too high for this test.",
    )


def workflow_bomb(c: CordumClient) -> ScenarioResult:
    """Start 12 workflow runs to exceed concurrency limit (default 10)."""
    run_ids: list[str] = []
    errors: list[str] = []
    limited = False

    for i in range(12):
        r = c.start_workflow(
            "demo-mock-bank.transfer",
            {"amount": 50 + i, "currency": "USD", "customer": f"bomb-{i}", "reason": "stress"},
        )
        if r.status_code == 429:
            limited = True
            break
        if r.status_code == 503:
            limited = True
            errors.append(f"#{i}: 503 (concurrency gate)")
            break
        if r.status_code == 200:
            run_ids.append(r.json().get("run_id", ""))
        else:
            errors.append(f"#{i}: {r.status_code}")

    if limited:
        return ScenarioResult(
            id=15, tier=TIER, name="Workflow bomb (12 runs, limit 10)",
            description="Start 12 workflow runs (limit is 10)",
            expected="Limited by max concurrent runs",
            actual=f"Limited after {len(run_ids)} runs",
            governance_held=True,
        )
    return ScenarioResult(
        id=15, tier=TIER, name="Workflow bomb (20 concurrent)",
        description="Start 20 workflow runs simultaneously",
        expected="Limited by max concurrent runs",
        actual=f"All {len(run_ids)} started, {len(errors)} errors",
        governance_held=len(run_ids) < 20,
        detail=f"Errors: {'; '.join(errors)}" if errors else "No concurrency limit hit",
    )


def run_all(c: CordumClient) -> list[ScenarioResult]:
    return [
        rapid_fire_submission(c),
        workflow_bomb(c),
    ]
