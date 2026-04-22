from __future__ import annotations

import json
from collections.abc import AsyncIterator, Iterator, Mapping
from typing import Any, Optional

import httpx
from pydantic import BaseModel, ConfigDict

from .errors import _raise_for_status, map_httpx_error


class StreamEvent(BaseModel):
    model_config = ConfigDict(extra="allow")

    event: str = "message"
    data: Any = None
    raw_data: str = ""
    id: Optional[str] = None
    retry: Optional[int] = None


def stream_events(
    client: httpx.Client,
    *,
    path: str = "/api/v1/stream",
    method: str = "GET",
    headers: Optional[Mapping[str, str]] = None,
    params: Optional[Mapping[str, Any]] = None,
    data: Any = None,
    json_body: Any = None,
    content: Any = None,
) -> Iterator[StreamEvent]:
    request_headers = _prepare_headers(headers)
    try:
        with client.stream(
            method,
            path,
            headers=request_headers,
            params=params,
            data=data,
            json=json_body,
            content=content,
        ) as response:
            _raise_for_status(response)
            yield from _iter_sse_events(response.iter_lines())
    except httpx.HTTPError as exc:
        raise map_httpx_error(exc) from exc


async def stream_events_async(
    client: httpx.AsyncClient,
    *,
    path: str = "/api/v1/stream",
    method: str = "GET",
    headers: Optional[Mapping[str, str]] = None,
    params: Optional[Mapping[str, Any]] = None,
    data: Any = None,
    json_body: Any = None,
    content: Any = None,
) -> AsyncIterator[StreamEvent]:
    request_headers = _prepare_headers(headers)
    try:
        async with client.stream(
            method,
            path,
            headers=request_headers,
            params=params,
            data=data,
            json=json_body,
            content=content,
        ) as response:
            _raise_for_status(response)
            async for event in _aiter_sse_events(response.aiter_lines()):
                yield event
    except httpx.HTTPError as exc:
        raise map_httpx_error(exc) from exc


def _prepare_headers(headers: Optional[Mapping[str, str]]) -> dict[str, str]:
    request_headers = dict(headers or {})
    request_headers.setdefault("Accept", "text/event-stream")
    request_headers.setdefault("Cache-Control", "no-cache")
    return request_headers


def _iter_sse_events(lines: Iterator[str]) -> Iterator[StreamEvent]:
    buffer: list[str] = []
    for line in lines:
        if line == "":
            event = _build_event(buffer)
            if event is not None:
                yield event
            buffer = []
            continue
        buffer.append(line)

    event = _build_event(buffer)
    if event is not None:
        yield event


async def _aiter_sse_events(lines: AsyncIterator[str]) -> AsyncIterator[StreamEvent]:
    buffer: list[str] = []
    async for line in lines:
        if line == "":
            event = _build_event(buffer)
            if event is not None:
                yield event
            buffer = []
            continue
        buffer.append(line)

    event = _build_event(buffer)
    if event is not None:
        yield event


def _build_event(lines: list[str]) -> Optional[StreamEvent]:
    if not lines:
        return None

    event_name = "message"
    event_id: Optional[str] = None
    retry: Optional[int] = None
    data_lines: list[str] = []

    for line in lines:
        if line.startswith(":"):
            continue

        field, separator, value = line.partition(":")
        if separator and value.startswith(" "):
            value = value[1:]

        if field == "event":
            event_name = value or "message"
        elif field == "data":
            data_lines.append(value)
        elif field == "id":
            event_id = value or None
        elif field == "retry":
            try:
                retry = int(value)
            except ValueError:
                retry = None

    raw_data = "\n".join(data_lines)
    data = _parse_event_data(raw_data)
    return StreamEvent(
        event=event_name,
        data=data,
        raw_data=raw_data,
        id=event_id,
        retry=retry,
    )


def _parse_event_data(raw_data: str) -> Any:
    if not raw_data:
        return ""
    try:
        return json.loads(raw_data)
    except json.JSONDecodeError:
        return raw_data
