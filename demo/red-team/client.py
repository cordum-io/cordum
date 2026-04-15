"""Cordum HTTP API client for red-team testing."""

from __future__ import annotations

import os
import time
from dataclasses import dataclass, field
from typing import Any

import httpx

_GATEWAY = os.environ.get("CORDUM_GATEWAY_URL", "https://localhost:8081")
_API_KEY = os.environ.get("CORDUM_API_KEY", "")


@dataclass
class CordumClient:
    base_url: str = _GATEWAY
    api_key: str = _API_KEY
    tenant_id: str = "default"
    _http: httpx.Client = field(init=False, repr=False)

    def __post_init__(self) -> None:
        self._http = httpx.Client(
            base_url=self.base_url,
            headers={
                "X-API-Key": self.api_key,
                "X-Tenant-ID": self.tenant_id,
                "Content-Type": "application/json",
            },
            verify=False,
            timeout=30.0,
        )

    def close(self) -> None:
        self._http.close()

    # -- Health ----------------------------------------------------------------

    def health(self) -> dict[str, Any]:
        return self._http.get("/health").json()

    # -- Jobs ------------------------------------------------------------------

    def submit_job(
        self,
        topic: str,
        prompt: str,
        *,
        payload: dict[str, Any] | None = None,
        risk_tags: list[str] | None = None,
        labels: dict[str, str] | None = None,
        tenant_override: str | None = None,
    ) -> httpx.Response:
        body: dict[str, Any] = {"topic": topic, "prompt": prompt}
        if payload:
            body["context"] = payload
        if risk_tags:
            body["risk_tags"] = risk_tags
        if labels:
            body["labels"] = labels
        headers = {}
        if tenant_override:
            headers["X-Tenant-ID"] = tenant_override
        return self._http.post("/api/v1/jobs", json=body, headers=headers)

    def get_job(self, job_id: str) -> httpx.Response:
        return self._http.get(f"/api/v1/jobs/{job_id}")

    # -- Approvals -------------------------------------------------------------

    def list_approvals(self) -> httpx.Response:
        return self._http.get("/api/v1/approvals")

    def approve_job(self, job_id: str, comment: str = "") -> httpx.Response:
        body: dict[str, Any] = {}
        if comment:
            body["comment"] = comment
        return self._http.post(f"/api/v1/approvals/{job_id}/approve", json=body)

    def reject_job(self, job_id: str, reason: str = "") -> httpx.Response:
        body: dict[str, Any] = {}
        if reason:
            body["reason"] = reason
        return self._http.post(f"/api/v1/approvals/{job_id}/reject", json=body)

    # -- Workflows -------------------------------------------------------------

    def start_workflow(
        self, workflow_id: str, input_data: dict[str, Any]
    ) -> httpx.Response:
        return self._http.post(
            f"/api/v1/workflows/{workflow_id}/runs", json=input_data
        )

    def get_workflow_run(self, run_id: str) -> httpx.Response:
        return self._http.get(f"/api/v1/workflow-runs/{run_id}")

    # -- Helpers ---------------------------------------------------------------

    def wait_for_job(self, job_id: str, timeout: float = 15.0) -> dict[str, Any]:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            r = self.get_job(job_id)
            if r.status_code != 200:
                return {"error": r.text, "status_code": r.status_code}
            data = r.json()
            state = data.get("state", "")
            if state not in ("PENDING", "SCHEDULED", "DISPATCHED", "RUNNING", "APPROVAL_REQUIRED"):
                return data
            time.sleep(0.5)
        return {"error": "timeout", "state": data.get("state", "unknown")}

    def raw_request(
        self,
        method: str,
        path: str,
        *,
        json: Any = None,
        headers: dict[str, str] | None = None,
    ) -> httpx.Response:
        return self._http.request(method, path, json=json, headers=headers or {})
