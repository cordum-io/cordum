from __future__ import annotations

import json

import httpx
import pytest

from cordum_sdk.errors import (
    AuthenticationError,
    AuthorizationError,
    ConflictError,
    CordumError,
    NotFoundError,
    RateLimitError,
    ServerError,
    ValidationError,
    _raise_for_status,
)


def make_response(
    status_code: int,
    *,
    body: object | None = None,
    headers: dict[str, str] | None = None,
) -> httpx.Response:
    request = httpx.Request("GET", "https://api.cordum.example/jobs")
    content = b""
    if body is not None:
        if isinstance(body, str):
            content = body.encode("utf-8")
        else:
            content = json.dumps(body).encode("utf-8")
    return httpx.Response(
        status_code=status_code,
        content=content,
        headers=headers,
        request=request,
    )


@pytest.mark.parametrize(
    ("status_code", "expected_type"),
    [
        (400, ValidationError),
        (401, AuthenticationError),
        (403, AuthorizationError),
        (404, NotFoundError),
        (409, ConflictError),
        (422, ValidationError),
        (429, RateLimitError),
        (500, ServerError),
        (503, ServerError),
    ],
)
def test_status_codes_map_to_typed_exceptions(
    status_code: int, expected_type: type[CordumError]
) -> None:
    response = make_response(
        status_code,
        body={"error": {"code": "bad_thing", "message": "Nope", "details": {"field": "broken"}}},
        headers={"X-Request-Id": "req-123", "Retry-After": "12"},
    )

    with pytest.raises(expected_type) as exc_info:
        _raise_for_status(response, None)

    error = exc_info.value
    assert error.status_code == status_code
    assert error.request_id == "req-123"
    assert error.payload == {
        "error": {"code": "bad_thing", "message": "Nope", "details": {"field": "broken"}}
    }
    if isinstance(error, ValidationError):
        assert error.field_errors == {"field": "broken"}
    if isinstance(error, RateLimitError):
        assert error.retry_after == 12


def test_validation_error_extracts_violations_from_gateway_payload() -> None:
    response = make_response(
        400,
        body={
            "error": "schema_validation_failed",
            "error_code": "schema_validation_failed",
            "violations": [
                {"path": "$.context.message", "message": "must be a string"},
                {"path": "$.topic", "message": "is required"},
            ],
        },
    )

    with pytest.raises(ValidationError) as exc_info:
        _raise_for_status(response, "req-violations")

    assert exc_info.value.field_errors == {
        "$.context.message": "must be a string",
        "$.topic": "is required",
    }


def test_raise_for_status_is_noop_for_success_responses() -> None:
    response = make_response(204)
    assert _raise_for_status(response, "req-ok") is None


def test_malformed_json_falls_back_to_raw_text_payload() -> None:
    response = make_response(500, body="<<<not json>>>")

    with pytest.raises(ServerError) as exc_info:
        _raise_for_status(response, "req-bad-json")

    assert exc_info.value.payload == "<<<not json>>>"
    assert exc_info.value.message == "<<<not json>>>"


def test_error_string_is_deterministic() -> None:
    response = make_response(
        401,
        body={"error": {"code": "invalid_credentials", "message": "invalid credentials"}},
        headers={"X-Request-Id": "req-auth"},
    )

    with pytest.raises(AuthenticationError) as exc_info:
        _raise_for_status(response, None)

    expected = (
        "AuthenticationError("
        "message='invalid credentials', "
        "status_code=401, "
        "request_id='req-auth', "
        "code='invalid_credentials')"
    )
    assert str(exc_info.value) == expected
