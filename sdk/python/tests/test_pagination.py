import httpx
import pytest

from cordum_sdk import AsyncCordumClient, CordumClient
from cordum_sdk.pagination import paginate


def _job(job_id: str) -> dict[str, object]:
    return {
        "id": job_id,
        "state": "queued",
        "topic": "job.default",
        "tenant": "tenant-123",
        "updated_at": "2026-04-20T07:00:00Z",
    }


def test_paginate_handles_mapping_next_cursor() -> None:
    seen: list[str | None] = []

    def fetch_page(*, cursor: str | None = None) -> tuple[dict[str, object], dict[str, str]]:
        seen.append(cursor)
        if cursor is None:
            return {
                "items": [_job("job-1"), _job("job-2")],
                "nextCursor": "page-2",
                "total": 3,
            }, {}
        return {"items": [_job("job-3")], "nextCursor": None, "total": 3}, {}

    items = list(paginate(fetch_page))
    assert [item["id"] for item in items] == ["job-1", "job-2", "job-3"]
    assert seen == [None, "page-2"]


def test_paginate_follows_link_header_cursor() -> None:
    seen: list[str | None] = []

    def fetch_page(*, cursor: str | None = None) -> tuple[list[str], dict[str, str]]:
        seen.append(cursor)
        if cursor is None:
            return ["one", "two"], {
                "Link": '<https://api.example.test/resources?cursor=page-2>; rel="next"'
            }
        return ["three"], {}

    assert list(paginate(fetch_page)) == ["one", "two", "three"]
    assert seen == [None, "page-2"]


def test_client_namespace_paginate_yields_each_item_once() -> None:
    requests: list[str | None] = []

    def handler(request: httpx.Request) -> httpx.Response:
        cursor = request.url.params.get("cursor")
        requests.append(cursor)
        if cursor is None:
            return httpx.Response(
                200,
                json={"items": [_job("job-1"), _job("job-2")], "next_cursor": "page-2"},
            )
        return httpx.Response(
            200,
            json={"items": [_job("job-3"), _job("job-4")], "next_cursor": None},
        )

    client = CordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    )

    items = list(client.jobs.paginate("list_jobs"))
    assert [item.id for item in items] == ["job-1", "job-2", "job-3", "job-4"]
    assert requests == [None, "page-2"]
    client.close()


@pytest.mark.asyncio
async def test_async_namespace_paginate_yields_each_item_once() -> None:
    requests: list[str | None] = []

    def handler(request: httpx.Request) -> httpx.Response:
        cursor = request.url.params.get("cursor")
        requests.append(cursor)
        if cursor is None:
            return httpx.Response(
                200,
                json={"items": [_job("job-a"), _job("job-b")], "next_cursor": "page-2"},
            )
        return httpx.Response(200, json={"items": [_job("job-c")], "next_cursor": None})

    async with AsyncCordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    ) as client:
        items = []
        async for item in client.jobs.paginate("list_jobs"):
            items.append(item.id)

    assert items == ["job-a", "job-b", "job-c"]
    assert requests == [None, "page-2"]
