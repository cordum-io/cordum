from __future__ import annotations

from collections.abc import AsyncIterator, Awaitable, Callable, Iterator, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, Generic, Optional, TypeVar
from urllib.parse import parse_qs, urlparse

T = TypeVar("T")


@dataclass(frozen=True)
class Page(Generic[T]):
    items: list[T]
    next_cursor: Optional[str]
    total: Optional[int] = None


def paginate(
    operation: Callable[..., Any],
    **kwargs: Any,
) -> Iterator[T]:
    page_kwargs = dict(kwargs)
    while True:
        payload, headers = _coerce_page_result(operation(**page_kwargs))
        page = _extract_page(payload, headers)
        for item in page.items:
            yield item
        if not page.next_cursor:
            break
        page_kwargs["cursor"] = page.next_cursor


async def paginate_async(
    operation: Callable[..., Awaitable[Any]],
    **kwargs: Any,
) -> AsyncIterator[T]:
    page_kwargs = dict(kwargs)
    while True:
        payload, headers = _coerce_page_result(await operation(**page_kwargs))
        page = _extract_page(payload, headers)
        for item in page.items:
            yield item
        if not page.next_cursor:
            break
        page_kwargs["cursor"] = page.next_cursor


def _coerce_page_result(result: Any) -> tuple[Any, Mapping[str, str]]:
    if hasattr(result, "parsed") and hasattr(result, "headers"):
        return result.parsed, result.headers
    if isinstance(result, tuple) and len(result) == 2:
        payload, headers = result
        if isinstance(headers, Mapping):
            return payload, headers
    return result, {}


def _extract_page(payload: Any, headers: Mapping[str, str]) -> Page[Any]:
    items = _extract_items(payload)
    next_cursor = _extract_next_cursor(payload, headers)
    total = _extract_total(payload)
    return Page(items=items, next_cursor=next_cursor, total=total)


def _extract_items(payload: Any) -> list[Any]:
    if payload is None or _is_unset(payload):
        return []
    if isinstance(payload, Mapping):
        raw_items = payload.get("items", [])
        return list(raw_items or [])
    if isinstance(payload, Sequence) and not isinstance(payload, (str, bytes, bytearray)):
        return list(payload)
    raw_items = getattr(payload, "items", [])
    if _is_unset(raw_items) or raw_items is None:
        return []
    return list(raw_items)


def _extract_next_cursor(payload: Any, headers: Mapping[str, str]) -> Optional[str]:
    if isinstance(payload, Mapping):
        next_cursor = payload.get("next_cursor") or payload.get("nextCursor")
        if isinstance(next_cursor, str) and next_cursor:
            return next_cursor
        if next_cursor is None:
            link_cursor = _extract_cursor_from_link(headers)
            if link_cursor:
                return link_cursor
        return None

    next_cursor = getattr(payload, "next_cursor", None)
    if _is_unset(next_cursor):
        next_cursor = None
    if isinstance(next_cursor, str) and next_cursor:
        return next_cursor

    camel_cursor = getattr(payload, "nextCursor", None)
    if _is_unset(camel_cursor):
        camel_cursor = None
    if isinstance(camel_cursor, str) and camel_cursor:
        return camel_cursor

    return _extract_cursor_from_link(headers)


def _extract_total(payload: Any) -> Optional[int]:
    if isinstance(payload, Mapping):
        total = payload.get("total")
        return total if isinstance(total, int) else None

    total = getattr(payload, "total", None)
    if isinstance(total, int):
        return total
    return None


def _extract_cursor_from_link(headers: Mapping[str, str]) -> Optional[str]:
    link_header = headers.get("Link") or headers.get("link")
    if not link_header:
        return None

    for part in link_header.split(","):
        part = part.strip()
        if 'rel="next"' not in part and "rel=next" not in part:
            continue
        start = part.find("<")
        end = part.find(">")
        if start == -1 or end == -1 or end <= start + 1:
            continue
        url = part[start + 1 : end]
        query = parse_qs(urlparse(url).query)
        for candidate in ("cursor", "next_cursor", "page[cursor]"):
            values = query.get(candidate)
            if values:
                return values[0]
    return None


def _is_unset(value: Any) -> bool:
    return type(value).__name__ == "Unset"
