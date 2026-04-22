import httpx
import pytest

from cordum_sdk import AsyncCordumClient, CordumClient
from cordum_sdk._generated.models.job_detail import JobDetail
from cordum_sdk._generated.models.list_jobs_response_200 import ListJobsResponse200
from cordum_sdk._generated.models.submit_job_request import SubmitJobRequest
from cordum_sdk._generated.models.submit_job_response import SubmitJobResponse
from cordum_sdk.errors import AuthenticationError, NotFoundError, RateLimitError


def _job_summary_payload(job_id: str) -> dict[str, object]:
    return {
        "id": job_id,
        "state": "queued",
        "topic": "job.default",
        "tenant": "tenant-123",
        "updated_at": "2026-04-20T07:00:00Z",
    }


def _job_detail_payload(job_id: str) -> dict[str, object]:
    payload = _job_summary_payload(job_id)
    payload.update(
        {
            "prompt": "hello",
            "trace_id": "trace-123",
        }
    )
    return payload


def test_client_constructor_shorthand_namespace_dispatch_and_headers() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        assert request.headers["X-API-Key"] == "secret-key"
        assert request.headers["X-Cordum-Tenant"] == "tenant-123"
        assert request.headers["X-Tenant-ID"] == "tenant-123"
        assert request.headers["User-Agent"] == "cordum-sdk-python/0.1.0"

        if request.method == "GET" and request.url.path == "/api/v1/jobs":
            return httpx.Response(
                200,
                json={"items": [_job_summary_payload("job-1")], "next_cursor": None},
            )
        if request.method == "GET" and request.url.path == "/api/v1/jobs/job-1":
            return httpx.Response(200, json=_job_detail_payload("job-1"))
        if request.method == "POST" and request.url.path == "/api/v1/jobs":
            return httpx.Response(200, json={"job_id": "job-1", "trace_id": "trace-123"})
        raise AssertionError("Unexpected request: {!r} {!r}".format(request.method, request.url))

    client = CordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    )

    listed = client.jobs.list_jobs()
    assert isinstance(listed, ListJobsResponse200)
    assert listed.items[0].id == "job-1"

    detail = client.jobs.get_job("job-1")
    assert isinstance(detail, JobDetail)
    assert detail.id == "job-1"

    created = client.jobs.submit_job(body=SubmitJobRequest(prompt="hello"))
    assert isinstance(created, SubmitJobResponse)
    assert created.job_id == "job-1"
    assert len(requests) == 3

    client.close()


def test_sync_context_manager_closes_client() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        del request
        return httpx.Response(200, json={"items": [], "next_cursor": None})

    with CordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    ) as client:
        client.jobs.list_jobs()
        assert client._httpx_client.is_closed is False

    assert client._httpx_client.is_closed is True


def test_client_raises_typed_errors() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/v1/jobs/missing":
            return httpx.Response(
                404,
                headers={"X-Request-Id": "req-404"},
                json={"error": {"code": "not_found", "message": "missing"}},
            )
        if request.url.path == "/api/v1/jobs":
            if request.method == "GET":
                return httpx.Response(
                    401,
                    headers={"X-Request-Id": "req-401"},
                    json={"error": {"code": "unauthorized", "message": "bad key"}},
                )
            return httpx.Response(
                429,
                headers={"Retry-After": "5", "X-Request-Id": "req-429"},
                json={"error": {"code": "rate_limited", "message": "slow down"}},
            )
        raise AssertionError("Unexpected request")

    client = CordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    )

    with pytest.raises(NotFoundError) as not_found:
        client.jobs.get_job("missing")
    assert not_found.value.request_id == "req-404"

    with pytest.raises(AuthenticationError) as auth_error:
        client.jobs.list_jobs()
    assert auth_error.value.request_id == "req-401"

    with pytest.raises(RateLimitError) as rate_limit:
        client.jobs.submit_job(body=SubmitJobRequest(prompt="hello"))
    assert rate_limit.value.retry_after == 5

    client.close()


@pytest.mark.asyncio
async def test_async_client_dispatch_and_context_manager() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.method == "GET" and request.url.path == "/api/v1/jobs":
            return httpx.Response(
                200,
                json={"items": [_job_summary_payload("job-async")], "next_cursor": None},
            )
        raise AssertionError("Unexpected request")

    async with AsyncCordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    ) as client:
        listed = await client.jobs.list_jobs()
        assert isinstance(listed, ListJobsResponse200)
        assert listed.items[0].id == "job-async"
        assert requests[0].headers["X-API-Key"] == "secret-key"
        assert requests[0].headers["User-Agent"] == "cordum-sdk-python/0.1.0"
        assert client._httpx_client.is_closed is False

    assert client._httpx_client.is_closed is True
