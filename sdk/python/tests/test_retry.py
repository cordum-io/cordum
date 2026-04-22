from __future__ import annotations

from datetime import datetime, timezone
from email.utils import format_datetime

import httpx
import pytest
import respx
from freezegun import freeze_time

from cordum_sdk._retry import AsyncRetryTransport, RetryExhaustedError, RetryPolicy, RetryTransport


def test_exponential_backoff_growth_is_used_for_sync_retries() -> None:
    sleeps: list[float] = []
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    route = router.get("https://api.cordum.example/jobs").mock(
        side_effect=[
            httpx.Response(503),
            httpx.Response(503),
            httpx.Response(200, json={"ok": True}),
        ]
    )

    transport = RetryTransport(
        policy=RetryPolicy(jitter=False),
        transport=httpx.MockTransport(router.handler),
        sleep_func=sleeps.append,
    )
    with httpx.Client(base_url="https://api.cordum.example", transport=transport) as client:
        response = client.get("/jobs")

    assert response.status_code == 200
    assert route.call_count == 3
    assert sleeps == [0.5, 1.0]


@freeze_time("2026-04-20 08:00:00", tz_offset=0)
@pytest.mark.parametrize(
    ("header_value", "expected_delay"),
    [
        ("7", 7.0),
        (format_datetime(datetime(2026, 4, 20, 8, 0, 9, tzinfo=timezone.utc)), 9.0),
    ],
)
def test_retry_after_is_honored(header_value: str, expected_delay: float) -> None:
    sleeps: list[float] = []
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    route = router.get("https://api.cordum.example/jobs").mock(
        side_effect=[
            httpx.Response(503, headers={"Retry-After": header_value}),
            httpx.Response(200, json={"ok": True}),
        ]
    )

    transport = RetryTransport(
        policy=RetryPolicy(jitter=False),
        transport=httpx.MockTransport(router.handler),
        sleep_func=sleeps.append,
    )
    with httpx.Client(base_url="https://api.cordum.example", transport=transport) as client:
        response = client.get("/jobs")

    assert response.status_code == 200
    assert route.call_count == 2
    assert sleeps == [expected_delay]


def test_jitter_produces_non_constant_backoff_samples() -> None:
    policy = RetryPolicy(jitter=True)
    samples = [policy.compute_backoff(0) for _ in range(100)]

    assert all(0.0 <= sample <= 0.5 for sample in samples)
    assert len({round(sample, 6) for sample in samples}) > 10


def test_non_retryable_statuses_bypass_retry() -> None:
    sleeps: list[float] = []
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    route = router.get("https://api.cordum.example/jobs").mock(
        return_value=httpx.Response(400, json={"error": "bad request"})
    )

    transport = RetryTransport(
        policy=RetryPolicy(jitter=False),
        transport=httpx.MockTransport(router.handler),
        sleep_func=sleeps.append,
    )
    with httpx.Client(base_url="https://api.cordum.example", transport=transport) as client:
        response = client.get("/jobs")

    assert response.status_code == 400
    assert route.call_count == 1
    assert sleeps == []


def test_post_without_idempotency_key_does_not_retry() -> None:
    sleeps: list[float] = []
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    route = router.post("https://api.cordum.example/jobs").mock(return_value=httpx.Response(503))

    transport = RetryTransport(
        policy=RetryPolicy(jitter=False),
        transport=httpx.MockTransport(router.handler),
        sleep_func=sleeps.append,
    )
    with httpx.Client(base_url="https://api.cordum.example", transport=transport) as client:
        response = client.post("/jobs", json={"name": "demo"})

    assert response.status_code == 503
    assert route.call_count == 1
    assert sleeps == []


def test_post_with_idempotency_key_retries() -> None:
    sleeps: list[float] = []
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    route = router.post("https://api.cordum.example/jobs").mock(
        side_effect=[httpx.Response(503), httpx.Response(200, json={"ok": True})]
    )

    transport = RetryTransport(
        policy=RetryPolicy(jitter=False),
        transport=httpx.MockTransport(router.handler),
        sleep_func=sleeps.append,
    )
    with httpx.Client(base_url="https://api.cordum.example", transport=transport) as client:
        response = client.post(
            "/jobs",
            json={"name": "demo"},
            headers={"Idempotency-Key": "job-1"},
        )

    assert response.status_code == 200
    assert route.call_count == 2
    assert sleeps == [0.5]


def test_exhaustion_raises_with_last_response_preserved() -> None:
    sleeps: list[float] = []
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    route = router.get("https://api.cordum.example/jobs").mock(
        side_effect=[
            httpx.Response(503),
            httpx.Response(503),
            httpx.Response(503),
        ]
    )

    transport = RetryTransport(
        policy=RetryPolicy(max_retries=2, jitter=False),
        transport=httpx.MockTransport(router.handler),
        sleep_func=sleeps.append,
    )
    with httpx.Client(base_url="https://api.cordum.example", transport=transport) as client:
        with pytest.raises(RetryExhaustedError) as exc_info:
            client.get("/jobs")

    assert route.call_count == 3
    assert sleeps == [0.5, 1.0]
    assert exc_info.value.last_response is not None
    assert exc_info.value.last_response.status_code == 503


@freeze_time("2026-04-20 08:00:00", tz_offset=0)
@pytest.mark.asyncio
async def test_async_transport_uses_async_sleep_and_retries() -> None:
    sleeps: list[float] = []
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    retry_after = format_datetime(datetime(2026, 4, 20, 8, 0, 2, tzinfo=timezone.utc))
    route = router.get("https://api.cordum.example/jobs").mock(
        side_effect=[
            httpx.Response(429, headers={"Retry-After": retry_after}),
            httpx.Response(200, json={"ok": True}),
        ]
    )

    async def capture_sleep(delay: float) -> None:
        sleeps.append(delay)

    transport = AsyncRetryTransport(
        policy=RetryPolicy(jitter=False),
        transport=httpx.MockTransport(router.async_handler),
        sleep_func=capture_sleep,
    )
    async with httpx.AsyncClient(
        base_url="https://api.cordum.example", transport=transport
    ) as client:
        response = await client.get("/jobs")

    assert response.status_code == 200
    assert route.call_count == 2
    assert sleeps == [2.0]
