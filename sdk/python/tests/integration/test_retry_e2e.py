from __future__ import annotations

import pytest

import cordum_sdk.client as client_module
from cordum_sdk import CordumClient
from cordum_sdk._generated.models.submit_job_request import SubmitJobRequest
from cordum_sdk._retry import RetryExhaustedError, RetryPolicy
from cordum_sdk.errors import ServerError


def test_retry_after_429_eventually_succeeds(monkeypatch, respx_router, gateway_state) -> None:
    del respx_router
    delays: list[float] = []
    original_transport = client_module.RetryTransport

    def build_transport(*args, **kwargs):
        kwargs["sleep_func"] = lambda delay: delays.append(delay)
        return original_transport(*args, **kwargs)

    monkeypatch.setattr(client_module, "RetryTransport", build_transport)
    gateway_state.list_jobs_statuses = [429]

    client = CordumClient(
        "https://api.example.test",
        auth="good-api-key",
        tenant_id="tenant-123",
        retry_policy=RetryPolicy(max_retries=2, jitter=False, initial_backoff_s=0.1),
    )
    listed = client.jobs.list_jobs()
    client.close()

    assert listed.items == []
    assert delays == [2.0]
    assert gateway_state.request_log.count(("GET", "/api/v1/jobs")) == 2


def test_non_idempotent_post_does_not_retry_without_idempotency_key(
    monkeypatch, respx_router, gateway_state
) -> None:
    del respx_router
    delays: list[float] = []
    original_transport = client_module.RetryTransport

    def build_transport(*args, **kwargs):
        kwargs["sleep_func"] = lambda delay: delays.append(delay)
        return original_transport(*args, **kwargs)

    monkeypatch.setattr(client_module, "RetryTransport", build_transport)
    gateway_state.submit_job_statuses = [503]

    client = CordumClient(
        "https://api.example.test",
        auth="good-api-key",
        tenant_id="tenant-123",
        retry_policy=RetryPolicy(max_retries=3, jitter=False, initial_backoff_s=0.1),
    )

    with pytest.raises(ServerError):
        client.jobs.submit_job(body=SubmitJobRequest(prompt="hello"))
    client.close()

    assert delays == []
    assert gateway_state.request_log.count(("POST", "/api/v1/jobs")) == 1


def test_retry_exhausted_after_max_retries(monkeypatch, respx_router, gateway_state) -> None:
    del respx_router
    delays: list[float] = []
    original_transport = client_module.RetryTransport

    def build_transport(*args, **kwargs):
        kwargs["sleep_func"] = lambda delay: delays.append(delay)
        return original_transport(*args, **kwargs)

    monkeypatch.setattr(client_module, "RetryTransport", build_transport)
    gateway_state.list_workflows_statuses = [503, 503, 503]

    client = CordumClient(
        "https://api.example.test",
        auth="good-api-key",
        tenant_id="tenant-123",
        retry_policy=RetryPolicy(max_retries=2, jitter=False, initial_backoff_s=0.1),
    )

    with pytest.raises(RetryExhaustedError):
        client.workflows.list_workflows()
    client.close()

    assert len(delays) == 2
    assert gateway_state.request_log.count(("GET", "/api/v1/workflows")) == 3


def test_jittered_retry_delay_stays_within_backoff_window(
    monkeypatch, respx_router, gateway_state
) -> None:
    del respx_router
    delays: list[float] = []
    original_transport = client_module.RetryTransport

    def build_transport(*args, **kwargs):
        kwargs["sleep_func"] = lambda delay: delays.append(delay)
        return original_transport(*args, **kwargs)

    monkeypatch.setattr(client_module, "RetryTransport", build_transport)
    gateway_state.list_workflows_statuses = [503]

    client = CordumClient(
        "https://api.example.test",
        auth="good-api-key",
        tenant_id="tenant-123",
        retry_policy=RetryPolicy(
            max_retries=1,
            jitter=True,
            initial_backoff_s=0.25,
            max_backoff_s=0.25,
        ),
    )
    client.workflows.list_workflows()
    client.close()

    assert len(delays) == 1
    assert 0.0 <= delays[0] <= 0.25
