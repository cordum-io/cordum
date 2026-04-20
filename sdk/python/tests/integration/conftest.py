from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

import httpx
import pytest
import respx


@dataclass
class GatewayState:
    jobs: dict[str, dict[str, Any]] = field(default_factory=dict)
    workflows: dict[str, dict[str, Any]] = field(default_factory=dict)
    runs: dict[str, dict[str, Any]] = field(default_factory=dict)
    policy_bundles: dict[str, dict[str, Any]] = field(default_factory=dict)
    workers: dict[str, dict[str, Any]] = field(default_factory=dict)
    request_log: list[tuple[str, str]] = field(default_factory=list)
    login_calls: int = 0
    force_session_refresh: bool = False
    session_refresh_triggered: bool = False
    list_jobs_statuses: list[int] = field(default_factory=list)
    submit_job_statuses: list[int] = field(default_factory=list)
    list_workflows_statuses: list[int] = field(default_factory=list)
    validation_error_job_id: str | None = None
    saml_session_token: str = "saml-session-token"

    def pop_status(self, name: str) -> int | None:
        queue = getattr(self, name)
        if queue:
            return queue.pop(0)
        return None

    def log(self, request: httpx.Request) -> None:
        self.request_log.append((request.method, request.url.path))


def _now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _job_record(job_id: str, *, state: str = "queued") -> dict[str, Any]:
    return {
        "id": job_id,
        "state": state,
        "topic": "job.default",
        "tenant": "tenant-123",
        "updated_at": _now(),
        "trace_id": "trace-{job_id}".format(job_id=job_id),
        "prompt": "hello from gateway",
    }


def _workflow_record(workflow_id: str) -> dict[str, Any]:
    return {
        "id": workflow_id,
        "name": "Workflow {workflow_id}".format(workflow_id=workflow_id),
        "steps": {
            "step-1": {
                "id": "step-1",
                "type": "job",
                "name": "Run worker job",
            }
        },
    }


def _run_record(run_id: str, workflow_id: str) -> dict[str, Any]:
    return {
        "id": run_id,
        "workflow_id": workflow_id,
        "status": "running",
        "started_at": _now(),
        "dry_run": False,
    }


def _policy_bundle(bundle_id: str) -> dict[str, Any]:
    return {
        "id": bundle_id,
        "enabled": True,
        "source": "sdk-test",
        "author": "sdk",
        "message": "bundle applied",
        "rule_count": 1,
        "updated_at": _now(),
        "content": "rules: []",
        "rules": [],
    }


def _worker_record(worker_id: str) -> dict[str, Any]:
    return {
        "worker_id": worker_id,
        "pool": "default",
        "active_jobs": 0,
        "max_parallel_jobs": 4,
        "capabilities": ["jobs", "workflows"],
        "region": "us-east-1",
        "type": "agent",
        "last_heartbeat": _now(),
    }


def _error_response(
    status_code: int,
    *,
    request_id: str,
    code: str,
    message: str,
    details: dict[str, Any] | None = None,
    headers: dict[str, str] | None = None,
) -> httpx.Response:
    response_headers = {"X-Request-Id": request_id}
    if headers:
        response_headers.update(headers)
    payload: dict[str, Any] = {"error": {"code": code, "message": message}}
    if details is not None:
        payload["error"]["details"] = details
    return httpx.Response(status_code, headers=response_headers, json=payload)


def _authorize(state: GatewayState, request: httpx.Request) -> httpx.Response | None:
    state.log(request)
    path = request.url.path
    if path.startswith("/api/v1/auth/login") or path.startswith("/api/v1/auth/saml/acs"):
        return None

    api_key = request.headers.get("X-API-Key")
    auth_header = request.headers.get("Authorization")
    allowed = {
        "Bearer bearer-token",
        "Bearer session-token-1",
        "Bearer session-token-2",
        "Bearer {token}".format(token=state.saml_session_token),
    }
    if api_key == "good-api-key" or auth_header in allowed:
        return None

    return _error_response(
        401,
        request_id="req-unauthorized",
        code="unauthorized",
        message="unauthorized",
    )


@pytest.fixture
def gateway_state() -> GatewayState:
    state = GatewayState()
    state.policy_bundles["bundle-1"] = _policy_bundle("bundle-1")
    state.workers["worker-1"] = _worker_record("worker-1")
    return state


@pytest.fixture
def respx_router(gateway_state: GatewayState) -> respx.Router:
    with respx.mock(
        base_url="https://api.example.test",
        assert_all_mocked=True,
        assert_all_called=False,
    ) as router:
        router.state = gateway_state  # type: ignore[attr-defined]

        def login_handler(request: httpx.Request) -> httpx.Response:
            gateway_state.log(request)
            payload = request.content.decode("utf-8")
            if "correct-password" not in payload:
                return _error_response(
                    401,
                    request_id="req-login-failed",
                    code="invalid_credentials",
                    message="invalid credentials",
                )
            gateway_state.login_calls += 1
            token = "session-token-1" if gateway_state.login_calls == 1 else "session-token-2"
            return httpx.Response(200, json={"token": token})

        def saml_acs_handler(request: httpx.Request) -> httpx.Response:
            gateway_state.log(request)
            return httpx.Response(200, json={"session_token": gateway_state.saml_session_token})

        def list_jobs_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            status = gateway_state.pop_status("list_jobs_statuses")
            if (
                gateway_state.force_session_refresh
                and request.headers.get("Authorization") == "Bearer session-token-1"
                and not gateway_state.session_refresh_triggered
            ):
                gateway_state.session_refresh_triggered = True
                return _error_response(
                    401,
                    request_id="req-session-expired",
                    code="session_expired",
                    message="session expired",
                )

            if status == 429:
                return _error_response(
                    429,
                    request_id="req-rate-limit",
                    code="rate_limited",
                    message="too many requests",
                    headers={"Retry-After": "2"},
                )
            if status == 503:
                return _error_response(
                    503,
                    request_id="req-service-unavailable",
                    code="unavailable",
                    message="service unavailable",
                )

            return httpx.Response(
                200,
                json={"items": list(gateway_state.jobs.values()), "next_cursor": None},
            )

        def submit_job_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            status = gateway_state.pop_status("submit_job_statuses")
            if status == 429:
                return _error_response(
                    429,
                    request_id="req-submit-rate-limit",
                    code="rate_limited",
                    message="slow down",
                    headers={"Retry-After": "2"},
                )
            if status == 503:
                return _error_response(
                    503,
                    request_id="req-submit-unavailable",
                    code="unavailable",
                    message="gateway unavailable",
                )

            job_id = "job-{count}".format(count=len(gateway_state.jobs) + 1)
            gateway_state.jobs[job_id] = _job_record(job_id)
            return httpx.Response(
                200,
                json={
                    "job_id": job_id,
                    "trace_id": "trace-{id}".format(id=job_id),
                },
            )

        def get_job_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            job_id = request.url.path.rsplit("/", 1)[-1]
            if gateway_state.validation_error_job_id == job_id:
                return _error_response(
                    422,
                    request_id="req-validation",
                    code="validation_failed",
                    message="validation failed",
                    details={"field_errors": {"prompt": "prompt is required"}},
                )
            job = gateway_state.jobs.get(job_id)
            if job is None:
                return _error_response(
                    404,
                    request_id="req-job-missing",
                    code="not_found",
                    message="job not found",
                )
            return httpx.Response(200, json=job)

        def cancel_job_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            job_id = request.url.path.split("/")[-2]
            job = gateway_state.jobs.get(job_id)
            if job is None:
                return _error_response(
                    404,
                    request_id="req-cancel-missing",
                    code="not_found",
                    message="job not found",
                )
            job["state"] = "canceled"
            return httpx.Response(200, json={"ok": True, "state": "canceled"})

        def create_workflow_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            body = request.read().decode("utf-8")
            if '"id":"wf-1"' in body or '"id": "wf-1"' in body:
                workflow_id = "wf-1"
            else:
                workflow_id = "wf-{count}".format(count=len(gateway_state.workflows) + 1)
            gateway_state.workflows[workflow_id] = _workflow_record(workflow_id)
            return httpx.Response(201, json={"id": workflow_id})

        def list_workflows_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            status = gateway_state.pop_status("list_workflows_statuses")
            if status == 503:
                return _error_response(
                    503,
                    request_id="req-workflows-unavailable",
                    code="unavailable",
                    message="workflows unavailable",
                )

            return httpx.Response(200, json=list(gateway_state.workflows.values()))

        def get_workflow_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            workflow_id = request.url.path.rsplit("/", 1)[-1]
            workflow = gateway_state.workflows.get(workflow_id)
            if workflow is None:
                return _error_response(
                    404,
                    request_id="req-workflow-missing",
                    code="not_found",
                    message="workflow not found",
                )
            return httpx.Response(200, json=workflow)

        def delete_workflow_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            workflow_id = request.url.path.rsplit("/", 1)[-1]
            gateway_state.workflows.pop(workflow_id, None)
            return httpx.Response(204)

        def start_run_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            workflow_id = request.url.path.split("/")[-2]
            run_id = "run-{count}".format(count=len(gateway_state.runs) + 1)
            gateway_state.runs[run_id] = _run_record(run_id, workflow_id)
            return httpx.Response(200, json={"run_id": run_id})

        def get_run_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            run_id = request.url.path.rsplit("/", 1)[-1]
            run = gateway_state.runs.get(run_id)
            if run is None:
                return _error_response(
                    404,
                    request_id="req-run-missing",
                    code="not_found",
                    message="run not found",
                )
            return httpx.Response(200, json=run)

        def update_bundle_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            bundle_id = request.url.path.rsplit("/", 1)[-1]
            bundle = gateway_state.policy_bundles.get(bundle_id, _policy_bundle(bundle_id))
            bundle["content"] = request.content.decode("utf-8")
            gateway_state.policy_bundles[bundle_id] = bundle
            return httpx.Response(200, json=bundle)

        def get_bundle_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            bundle_id = request.url.path.rsplit("/", 1)[-1]
            bundle = gateway_state.policy_bundles.get(bundle_id)
            if bundle is None:
                return _error_response(
                    404,
                    request_id="req-bundle-missing",
                    code="not_found",
                    message="bundle not found",
                )
            return httpx.Response(200, json=bundle)

        def list_bundles_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            bundles = list(gateway_state.policy_bundles.values())
            return httpx.Response(
                200,
                json={
                    "items": bundles,
                    "bundles": bundles,
                    "updated_at": _now(),
                },
            )

        def delete_bundle_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error

            bundle_id = request.url.path.rsplit("/", 1)[-1]
            gateway_state.policy_bundles.pop(bundle_id, None)
            return httpx.Response(204)

        def list_workers_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error
            return httpx.Response(200, json={"items": list(gateway_state.workers.values())})

        def get_worker_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error
            worker_id = request.url.path.rsplit("/", 1)[-1]
            worker = gateway_state.workers.get(worker_id)
            if worker is None:
                return _error_response(
                    404,
                    request_id="req-worker-missing",
                    code="not_found",
                    message="worker not found",
                )
            return httpx.Response(200, json=worker)

        def get_worker_jobs_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error
            return httpx.Response(200, json={"items": list(gateway_state.jobs.values())})

        def mcp_status_handler(request: httpx.Request) -> httpx.Response:
            auth_error = _authorize(gateway_state, request)
            if auth_error is not None:
                return auth_error
            return httpx.Response(200, json={"connected": True, "tenant": "tenant-123"})

        router.post("/api/v1/auth/login").mock(side_effect=login_handler)
        router.post("/api/v1/auth/saml/acs").mock(side_effect=saml_acs_handler)
        router.post(path__regex=r"^/api/v1/jobs/[^/]+/cancel$").mock(side_effect=cancel_job_handler)
        router.get(path__regex=r"^/api/v1/jobs/[^/]+$").mock(side_effect=get_job_handler)
        router.get("/api/v1/jobs").mock(side_effect=list_jobs_handler)
        router.post("/api/v1/jobs").mock(side_effect=submit_job_handler)
        router.post(path__regex=r"^/api/v1/workflows/[^/]+/runs$").mock(side_effect=start_run_handler)
        router.get(path__regex=r"^/api/v1/workflow-runs/[^/]+$").mock(side_effect=get_run_handler)
        router.delete(path__regex=r"^/api/v1/workflows/[^/]+$").mock(side_effect=delete_workflow_handler)
        router.get(path__regex=r"^/api/v1/workflows/[^/]+$").mock(side_effect=get_workflow_handler)
        router.get("/api/v1/workflows").mock(side_effect=list_workflows_handler)
        router.post("/api/v1/workflows").mock(side_effect=create_workflow_handler)
        router.delete(path__regex=r"^/api/v1/policy/bundles/[^/]+$").mock(side_effect=delete_bundle_handler)
        router.get(path__regex=r"^/api/v1/policy/bundles/[^/]+$").mock(side_effect=get_bundle_handler)
        router.put(path__regex=r"^/api/v1/policy/bundles/[^/]+$").mock(side_effect=update_bundle_handler)
        router.get("/api/v1/policy/bundles").mock(side_effect=list_bundles_handler)
        router.get(path__regex=r"^/api/v1/workers/[^/]+/jobs$").mock(side_effect=get_worker_jobs_handler)
        router.get(path__regex=r"^/api/v1/workers/[^/]+$").mock(side_effect=get_worker_handler)
        router.get("/api/v1/workers").mock(side_effect=list_workers_handler)
        router.get("/mcp/status").mock(side_effect=mcp_status_handler)

        yield router
