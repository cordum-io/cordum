from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from typing import Any, Dict, Optional, Tuple

import httpx


def _stringify_message(value: Any, *, fallback: str) -> str:
    if isinstance(value, str):
        value = value.strip()
        return value or fallback
    return fallback


def _coerce_mapping(value: Any) -> Optional[Dict[str, Any]]:
    if isinstance(value, Mapping):
        return {str(key): item for key, item in value.items()}
    return None


def _parse_response_payload(response: httpx.Response) -> Any:
    try:
        return response.json()
    except ValueError:
        return response.text


def _extract_error_parts(
    response: httpx.Response,
) -> Tuple[str, Optional[str], Optional[Dict[str, Any]], Any]:
    payload = _parse_response_payload(response)
    fallback = response.reason_phrase or f"HTTP {response.status_code}"

    code: Optional[str] = None
    details: Optional[Dict[str, Any]] = None
    message = fallback

    if isinstance(payload, Mapping):
        raw_error = payload.get("error")
        root_details = _coerce_mapping(payload.get("details"))
        if isinstance(raw_error, Mapping):
            error_mapping = _coerce_mapping(raw_error) or {}
            code = (
                _stringify_message(
                    error_mapping.get("code") or payload.get("code") or payload.get("error_code"),
                    fallback="",
                )
                or None
            )
            details = _coerce_mapping(error_mapping.get("details")) or root_details
            message = _stringify_message(
                error_mapping.get("message") or payload.get("message") or payload.get("error"),
                fallback=fallback,
            )
        else:
            code = (
                _stringify_message(
                    payload.get("code") or payload.get("error_code"),
                    fallback="",
                )
                or None
            )
            details = root_details
            message = _stringify_message(payload.get("message") or raw_error, fallback=fallback)
    elif isinstance(payload, str):
        message = payload.strip() or fallback

    return message, code, details, payload


def _extract_field_errors(payload: Any, details: Optional[Dict[str, Any]]) -> Dict[str, str]:
    def _from_mapping(mapping: Mapping[str, Any]) -> Dict[str, str]:
        result: Dict[str, str] = {}
        for key, value in mapping.items():
            if isinstance(value, str):
                result[str(key)] = value
            elif isinstance(value, Mapping):
                nested = value.get("message")
                if isinstance(nested, str):
                    result[str(key)] = nested
                else:
                    result[str(key)] = str(value)
            else:
                result[str(key)] = str(value)
        return result

    for candidate in (
        details.get("field_errors") if details else None,
        details.get("fields") if details else None,
        payload.get("field_errors") if isinstance(payload, Mapping) else None,
        payload.get("fields") if isinstance(payload, Mapping) else None,
    ):
        if isinstance(candidate, Mapping):
            return _from_mapping(candidate)

    if isinstance(details, Mapping) and details:
        return _from_mapping(details)

    violations: Any = None
    if details and isinstance(details.get("violations"), list):
        violations = details.get("violations")
    elif isinstance(payload, Mapping) and isinstance(payload.get("violations"), list):
        violations = payload.get("violations")

    if isinstance(violations, list):
        result: Dict[str, str] = {}
        for item in violations:
            if not isinstance(item, Mapping):
                continue
            path = item.get("path") or item.get("field") or item.get("name")
            message = item.get("message") or item.get("detail") or item.get("error")
            if path is None or message is None:
                continue
            result[str(path)] = str(message)
        return result

    return {}


def _parse_retry_after(response: httpx.Response) -> Optional[int]:
    raw = response.headers.get("Retry-After")
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


class CordumError(Exception):
    def __init__(
        self,
        message: str,
        *,
        request_id: Optional[str] = None,
        status_code: Optional[int] = None,
        payload: Any = None,
        code: Optional[str] = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.request_id = request_id
        self.status_code = status_code
        self.payload = payload
        self.code = code

    def __str__(self) -> str:
        parts = [f"{self.__class__.__name__}(message={self.message!r}"]
        if self.status_code is not None:
            parts.append(f"status_code={self.status_code}")
        if self.request_id:
            parts.append(f"request_id={self.request_id!r}")
        if self.code:
            parts.append(f"code={self.code!r}")
        parts.append(")")
        return ", ".join(parts[:-1]) + parts[-1]


class AuthenticationError(CordumError):
    pass


class AuthorizationError(CordumError):
    pass


class NotFoundError(CordumError):
    pass


class ConflictError(CordumError):
    pass


class ServerError(CordumError):
    pass


class NetworkError(CordumError):
    def __init__(self, message: str, *, cause: Optional[Exception] = None) -> None:
        super().__init__(message)
        self.__cause__ = cause


class TimeoutError(NetworkError):
    pass


class ValidationError(CordumError):
    def __init__(
        self,
        message: str,
        *,
        request_id: Optional[str] = None,
        status_code: Optional[int] = None,
        payload: Any = None,
        code: Optional[str] = None,
        field_errors: Optional[Dict[str, str]] = None,
    ) -> None:
        super().__init__(
            message,
            request_id=request_id,
            status_code=status_code,
            payload=payload,
            code=code,
        )
        self.field_errors = field_errors or {}


class RateLimitError(CordumError):
    def __init__(
        self,
        message: str,
        *,
        request_id: Optional[str] = None,
        status_code: Optional[int] = None,
        payload: Any = None,
        code: Optional[str] = None,
        retry_after: Optional[int] = None,
    ) -> None:
        super().__init__(
            message,
            request_id=request_id,
            status_code=status_code,
            payload=payload,
            code=code,
        )
        self.retry_after = retry_after


_STATUS_TO_ERROR: Dict[int, type[CordumError]] = {
    400: ValidationError,
    401: AuthenticationError,
    403: AuthorizationError,
    404: NotFoundError,
    409: ConflictError,
    422: ValidationError,
    429: RateLimitError,
}


def map_httpx_error(exc: Exception) -> CordumError:
    if isinstance(exc, httpx.TimeoutException):
        return TimeoutError(str(exc), cause=exc)
    if isinstance(exc, httpx.TransportError):
        return NetworkError(str(exc), cause=exc)
    if isinstance(exc, CordumError):
        return exc
    return CordumError(str(exc))


def _raise_for_status(response: httpx.Response, request_id: Optional[str] = None) -> None:
    if response.status_code < 400:
        return

    resolved_request_id = (
        request_id or response.headers.get("X-Request-Id") or response.headers.get("X-Request-ID")
    )
    message, code, details, payload = _extract_error_parts(response)

    error_cls = _STATUS_TO_ERROR.get(response.status_code)
    if error_cls is None and response.status_code >= 500:
        error_cls = ServerError
    elif error_cls is None:
        error_cls = CordumError

    kwargs: Dict[str, Any] = {
        "request_id": resolved_request_id,
        "status_code": response.status_code,
        "payload": payload,
        "code": code,
    }
    if issubclass(error_cls, ValidationError):
        kwargs["field_errors"] = _extract_field_errors(payload, details)
    if issubclass(error_cls, RateLimitError):
        kwargs["retry_after"] = _parse_retry_after(response)

    raise error_cls(message, **kwargs)


@dataclass(frozen=True)
class ErrorEnvelope:
    message: str
    code: Optional[str]
    details: Optional[Dict[str, Any]]
    payload: Any
