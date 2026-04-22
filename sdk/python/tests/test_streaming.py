from __future__ import annotations

from typing import AsyncIterator, Iterator

import httpx
import pytest

from cordum_sdk import AsyncCordumClient, CordumClient
from cordum_sdk.errors import NetworkError


class TrackingSyncStream(httpx.SyncByteStream):
    def __init__(self, chunks: list[bytes], *, error: Exception | None = None) -> None:
        self._chunks = list(chunks)
        self._error = error
        self.closed = False

    def __iter__(self) -> Iterator[bytes]:
        for index, chunk in enumerate(self._chunks):
            yield chunk
            if self._error is not None and index == len(self._chunks) - 1:
                raise self._error

    def close(self) -> None:
        self.closed = True


class TrackingAsyncStream(httpx.AsyncByteStream):
    def __init__(self, chunks: list[bytes]) -> None:
        self._chunks = list(chunks)
        self.closed = False

    async def __aiter__(self) -> AsyncIterator[bytes]:
        for chunk in self._chunks:
            yield chunk

    async def aclose(self) -> None:
        self.closed = True


def test_streaming_parses_sse_frames_and_closes_on_break() -> None:
    stream = TrackingSyncStream(
        [
            b"event: status\n",
            b"data: {\"job_id\":\"job-1\"}\n\n",
            b"data: plain text\n\n",
        ]
    )

    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["Accept"] == "text/event-stream"
        return httpx.Response(
            200,
            headers={"Content-Type": "text/event-stream"},
            stream=stream,
        )

    client = CordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    )

    first_event = None
    for event in client.mcp.stream():
        first_event = event
        break

    assert first_event is not None
    assert first_event.event == "status"
    assert first_event.data == {"job_id": "job-1"}
    assert stream.closed is True
    client.close()


def test_streaming_raises_network_error_on_disconnect() -> None:
    stream = TrackingSyncStream(
        [
            b"data: {\"step\":1}\n\n",
        ],
        error=httpx.ReadError("boom"),
    )

    def handler(request: httpx.Request) -> httpx.Response:
        del request
        return httpx.Response(
            200,
            headers={"Content-Type": "text/event-stream"},
            stream=stream,
        )

    client = CordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    )

    iterator = client.mcp.stream()
    first = next(iterator)
    assert first.data == {"step": 1}
    with pytest.raises(NetworkError):
        next(iterator)
    client.close()


@pytest.mark.asyncio
async def test_async_streaming_parses_sse_frames() -> None:
    stream = TrackingAsyncStream(
        [
            b"event: heartbeat\n",
            b"data: {\"ok\":true}\n\n",
        ]
    )

    def handler(request: httpx.Request) -> httpx.Response:
        del request
        return httpx.Response(
            200,
            headers={"Content-Type": "text/event-stream"},
            stream=stream,
        )

    async with AsyncCordumClient(
        "https://api.example.test",
        auth="secret-key",
        tenant_id="tenant-123",
        transport=httpx.MockTransport(handler),
    ) as client:
        events = []
        async for event in client.mcp.stream():
            events.append(event)

    assert len(events) == 1
    assert events[0].event == "heartbeat"
    assert events[0].data == {"ok": True}
    assert stream.closed is True
