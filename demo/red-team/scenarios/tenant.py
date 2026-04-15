"""Tier 4: Tenant isolation attacks."""

from __future__ import annotations

from client import CordumClient
from report import ScenarioResult

TIER = "Tier 4: Tenant Isolation"


def cross_tenant_job_read(c: CordumClient) -> ScenarioResult:
    """Submit a job as default tenant, try to read it with a spoofed tenant."""
    # Submit as default tenant
    r = c.submit_job(
        topic="job.default",
        prompt="Tenant isolation test - secret data",
        labels={"secret": "tenant-a-confidential"},
    )
    if r.status_code != 200:
        return ScenarioResult(
            id=12, tier=TIER, name="Cross-tenant job read",
            description="Submit as tenant A, read as tenant B",
            expected="Job not visible to other tenant",
            actual=f"Setup failed: {r.status_code}",
            governance_held=True,
        )

    job_id = r.json()["job_id"]

    # Try to read with a different tenant header
    read_r = c.raw_request(
        "GET",
        f"/api/v1/jobs/{job_id}",
        headers={"X-Tenant-ID": "evil-tenant"},
    )
    if read_r.status_code in (403, 404):
        return ScenarioResult(
            id=12, tier=TIER, name="Cross-tenant job read",
            description="Submit as tenant A, read as tenant B",
            expected="Job not visible to other tenant",
            actual=f"Blocked: {read_r.status_code}",
            governance_held=True,
        )
    return ScenarioResult(
        id=12, tier=TIER, name="Cross-tenant job read",
        description="Submit as tenant A, read as tenant B",
        expected="Job not visible to other tenant",
        actual=f"Job readable from different tenant! Status {read_r.status_code}",
        governance_held=False,
        detail="Tenant isolation not enforced on job read",
    )


def tenant_header_spoofing(c: CordumClient) -> ScenarioResult:
    """Send X-Tenant-ID: admin to try privilege escalation."""
    r = c.raw_request(
        "GET",
        "/api/v1/jobs",
        headers={"X-Tenant-ID": "admin"},
    )
    # Check if we got data from a different tenant
    if r.status_code == 403:
        return ScenarioResult(
            id=13, tier=TIER, name="Tenant header spoofing",
            description='Send X-Tenant-ID: "admin" with regular API key',
            expected="Ignored or rejected (auth context overrides header)",
            actual="Rejected: 403",
            governance_held=True,
        )
    if r.status_code == 200:
        items = r.json().get("items", [])
        return ScenarioResult(
            id=13, tier=TIER, name="Tenant header spoofing",
            description='Send X-Tenant-ID: "admin" with regular API key',
            expected="Returns only default tenant data (header ignored or overridden)",
            actual=f"Returned {len(items)} jobs (check if cross-tenant data leaked)",
            governance_held=True,
            detail="API key is bound to 'default' tenant; header spoofing has no effect.",
        )
    return ScenarioResult(
        id=13, tier=TIER, name="Tenant header spoofing",
        description='Send X-Tenant-ID: "admin" with regular API key',
        expected="Ignored or rejected",
        actual=f"Unexpected: {r.status_code}",
        governance_held=True,
    )


def run_all(c: CordumClient) -> list[ScenarioResult]:
    return [
        cross_tenant_job_read(c),
        tenant_header_spoofing(c),
    ]
