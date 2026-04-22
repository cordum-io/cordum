from __future__ import annotations

import secrets
import time
from collections.abc import Awaitable
from dataclasses import dataclass, field
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from typing import Any, Callable, Optional

import anyio
import httpx

from .errors import CordumError, map_httpx_error

_DEFAULT_RETRYABLE_STATUSES = frozenset({408, 425, 429, 500, 502, 503, 504})
_DEFAULT_RETRYABLE_METHODS = frozenset({"GET", "HEAD", "PUT", "DELETE", "OPTIONS"})


@dataclass(frozen=True)
class RetryPolicy:
    max_retries: int = 3
    initial_backoff_s: float = 0.5
    max_backoff_s: float = 30.0
    backoff_multiplier: float = 2.0
    jitter: bool = True
    retryable_statuses: frozenset[int] = field(
        default_factory=lambda: _DEFAULT_RETRYABLE_STATUSES
    )
    retryable_methods: frozenset[str] = field(
        default_factory=lambda: _DEFAULT_RETRYABLE_METHODS
    )
    _rng: secrets.SystemRandom = field(
        default_factory=secrets.SystemRandom, repr=False, compare=False
    )

    def is_retryable_method(self, request: httpx.Request) -> bool:
        method = request.method.upper()
        if method in self.retryable_methods:
            return True
        return bool(
            request.headers.get("Idempotency-Key") or request.headers.get("X-Idempotency-Key")
        )

    def is_retryable_response(self, request: httpx.Request, response: httpx.Response) -> bool:
        return (
            self.is_retryable_method(request)
            and response.status_code in self.retryable_statuses
        )

    def compute_backoff(self, retry_number: int) -> float:
        delay = min(
            self.initial_backoff_s * (self.backoff_multiplier**retry_number),
            self.max_backoff_s,
        )
        if self.jitter:
            delay = self._rng.uniform(0.0, delay)
        return max(delay, 0.0)

    def compute_delay(
        self, request: httpx.Request, response: httpx.Response, retry_number: int
    ) -> float:
        if response.status_code in {429, 503}:
            retry_after = _parse_retry_after(response.headers)
            if retry_after is not None:
                return min(float(retry_after), self.max_backoff_s)
        return self.compute_backoff(retry_number)


class RetryExhaustedError(CordumError):
    def __init__(
        self,
        message: str,
        *,
        attempts: int,
        last_response: Optional[httpx.Response] = None,
        last_exception: Optional[Exception] = None,
    ) -> None:
        request_id = None
        status_code = None
        payload: Any = None
        if last_response is not None:
            request_id = last_response.headers.get("X-Request-Id") or last_response.headers.get(
                "X-Request-ID"
            )
            status_code = last_response.status_code
            try:
                payload = last_response.json()
            except ValueError:
                payload = last_response.text
        super().__init__(
            message,
            request_id=request_id,
            status_code=status_code,
            payload=payload,
        )
        self.attempts = attempts
        self.last_response = last_response
        self.last_exception = last_exception
        if last_exception is not None:
            self.__cause__ = last_exception


def _parse_retry_after(headers: httpx.Headers) -> Optional[int]:
    raw = headers.get("Retry-After")
    if raw is None:
        return None

    value = raw.strip()
    if not value:
        return None
    if value.isdigit():
        return max(int(value), 0)

    try:
        retry_time = parsedate_to_datetime(value)
    except (TypeError, ValueError, IndexError, OverflowError):
        return None

    if retry_time.tzinfo is None:
        retry_time = retry_time.replace(tzinfo=timezone.utc)
    delta = retry_time - datetime.now(timezone.utc)
    return max(int(delta.total_seconds()), 0)


def _clone_request(request: httpx.Request, body: bytes) -> httpx.Request:
    return httpx.Request(
        method=request.method,
        url=request.url,
        headers=request.headers,
        content=body,
        extensions=dict(request.extensions),
    )


class RetryTransport(httpx.BaseTransport):
    def __init__(
        self,
        *,
        policy: Optional[RetryPolicy] = None,
        transport: Optional[httpx.BaseTransport] = None,
        sleep_func: Callable[[float], None] = time.sleep,
    ) -> None:
        self.policy = policy or RetryPolicy()
        self.transport = transport or httpx.HTTPTransport()
        self._sleep_func = sleep_func

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        request_body = request.read()
        attempts = 0

        while True:
            current_request = _clone_request(request, request_body)
            try:
                response = self.transport.handle_request(current_request)
            except httpx.TransportError as exc:
                if attempts >= self.policy.max_retries or not self.policy.is_retryable_method(
                    request
                ):
                    if attempts == 0:
                        raise map_httpx_error(exc) from exc
                    raise RetryExhaustedError(
                        f"Retry policy exhausted after {attempts + 1} attempts",
                        attempts=attempts + 1,
                        last_exception=exc,
                    ) from exc

                self._sleep_func(self.policy.compute_backoff(attempts))
                attempts += 1
                continue

            if not self.policy.is_retryable_response(request, response):
                return response

            if attempts >= self.policy.max_retries:
                response.read()
                raise RetryExhaustedError(
                    f"Retry policy exhausted after {attempts + 1} attempts",
                    attempts=attempts + 1,
                    last_response=response,
                )

            delay = self.policy.compute_delay(request, response, attempts)
            response.close()
            self._sleep_func(delay)
            attempts += 1

    def close(self) -> None:
        self.transport.close()


class AsyncRetryTransport(httpx.AsyncBaseTransport):
    def __init__(
        self,
        *,
        policy: Optional[RetryPolicy] = None,
        transport: Optional[httpx.AsyncBaseTransport] = None,
        sleep_func: Callable[[float], Awaitable[None]] = anyio.sleep,
    ) -> None:
        self.policy = policy or RetryPolicy()
        self.transport = transport or httpx.AsyncHTTPTransport()
        self._sleep_func = sleep_func

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        request_body = await request.aread()
        attempts = 0

        while True:
            current_request = _clone_request(request, request_body)
            try:
                response = await self.transport.handle_async_request(current_request)
            except httpx.TransportError as exc:
                if attempts >= self.policy.max_retries or not self.policy.is_retryable_method(
                    request
                ):
                    if attempts == 0:
                        raise map_httpx_error(exc) from exc
                    raise RetryExhaustedError(
                        f"Retry policy exhausted after {attempts + 1} attempts",
                        attempts=attempts + 1,
                        last_exception=exc,
                    ) from exc

                await self._sleep_func(self.policy.compute_backoff(attempts))
                attempts += 1
                continue

            if not self.policy.is_retryable_response(request, response):
                return response

            if attempts >= self.policy.max_retries:
                await response.aread()
                raise RetryExhaustedError(
                    f"Retry policy exhausted after {attempts + 1} attempts",
                    attempts=attempts + 1,
                    last_response=response,
                )

            delay = self.policy.compute_delay(request, response, attempts)
            await response.aclose()
            await self._sleep_func(delay)
            attempts += 1

    async def aclose(self) -> None:
        await self.transport.aclose()
