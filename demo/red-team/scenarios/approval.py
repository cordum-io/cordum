"""Tier 2: Approval bypass attacks."""

from __future__ import annotations

import time

from client import CordumClient
from report import ScenarioResult

TIER = "Tier 2: Approval Bypass"


def self_approval(c: CordumClient) -> ScenarioResult:
    """Submit a job requiring approval, then approve it with the same API key."""
    r = c.submit_job(
        topic="job.demo-mock-bank.transfer",
        prompt="Self-approval test: Transfer $200",
        payload={"from": "Self", "to": "Self", "amount": 200, "currency": "USD"},
        risk_tags=["review"],  # triggers require_approval
    )
    if r.status_code != 200 or "job_id" not in r.json():
        return ScenarioResult(
            id=5, tier=TIER, name="Self-approval",
            description="Submit job, then approve with same API key",
            expected="Should prevent self-approval",
            actual=f"Submission failed: {r.status_code} {r.text[:60]}",
            governance_held=True,
        )

    job_id = r.json()["job_id"]
    time.sleep(0.5)

    # Now approve with the SAME API key
    approve_r = c.approve_job(job_id, comment="I approve my own request")
    if approve_r.status_code >= 400:
        error = approve_r.json().get("error", approve_r.text)[:80]
        return ScenarioResult(
            id=5, tier=TIER, name="Self-approval",
            description="Submit job, then approve with same API key",
            expected="Should prevent self-approval",
            actual=f"Self-approval blocked: {error}",
            governance_held=True,
        )

    return ScenarioResult(
        id=5, tier=TIER, name="Self-approval",
        description="Submit job, then approve with same API key",
        expected="Should prevent self-approval",
        actual=f"Self-approval SUCCEEDED for job {job_id[:12]}",
        governance_held=False,
        detail="No check that approver != submitter. Same API key can submit and approve.",
    )


def approve_nonexistent(c: CordumClient) -> ScenarioResult:
    """Try to approve a job that doesn't exist."""
    r = c.approve_job("nonexistent-fake-job-id-12345")
    if r.status_code == 404:
        return ScenarioResult(
            id=6, tier=TIER, name="Approve nonexistent job",
            description="Approve a job ID that doesn't exist",
            expected="404 Not Found",
            actual="404 Not Found",
            governance_held=True,
        )
    return ScenarioResult(
        id=6, tier=TIER, name="Approve nonexistent job",
        description="Approve a job ID that doesn't exist",
        expected="404 Not Found",
        actual=f"Unexpected: {r.status_code} {r.text[:60]}",
        governance_held=False,
    )


def cross_tenant_approval(c: CordumClient) -> ScenarioResult:
    """Submit as default tenant, try to approve from a different tenant context."""
    # First submit normally
    r = c.submit_job(
        topic="job.demo-mock-bank.transfer",
        prompt="Cross-tenant approval test",
        payload={"amount": 150, "currency": "USD"},
        risk_tags=["review"],
    )
    if r.status_code != 200 or "job_id" not in r.json():
        return ScenarioResult(
            id=7, tier=TIER, name="Cross-tenant approval",
            description="Submit as tenant A, approve as tenant B",
            expected="Should deny cross-tenant approval",
            actual=f"Submission failed: {r.status_code}",
            governance_held=True,
        )

    job_id = r.json()["job_id"]
    time.sleep(0.5)

    # Try to approve with a different tenant header
    approve_r = c.raw_request(
        "POST",
        f"/api/v1/approvals/{job_id}/approve",
        json={"comment": "cross-tenant attack"},
        headers={"X-Tenant-ID": "evil-tenant"},
    )
    if approve_r.status_code in (403, 404):
        return ScenarioResult(
            id=7, tier=TIER, name="Cross-tenant approval",
            description="Submit as tenant A, approve as tenant B",
            expected="Should deny cross-tenant approval",
            actual=f"Blocked: {approve_r.status_code}",
            governance_held=True,
        )

    # Clean up: reject the job so it doesn't linger
    c.reject_job(job_id, reason="red-team cleanup")
    return ScenarioResult(
        id=7, tier=TIER, name="Cross-tenant approval",
        description="Submit as tenant A, approve as tenant B",
        expected="Should deny cross-tenant approval",
        actual=f"Approved from different tenant! {approve_r.status_code}",
        governance_held=False,
        detail="Tenant isolation not enforced on approval actions",
    )


def run_all(c: CordumClient) -> list[ScenarioResult]:
    return [
        self_approval(c),
        approve_nonexistent(c),
        cross_tenant_approval(c),
    ]
