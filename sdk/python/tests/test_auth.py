from __future__ import annotations

import inspect
from typing import Any, MutableMapping

import httpx
import pytest
import respx

from cordum_sdk.auth import ApiKeyAuth, BearerTokenAuth, SamlAuth, SessionAuth


async def _apply_auth(
    provider: Any,
    headers: MutableMapping[str, str],
    request: httpx.Request,
) -> None:
    result = provider.apply(headers, request)
    if inspect.isawaitable(result):
        await result


def test_api_key_and_bearer_auth_inject_headers_sync() -> None:
    request = httpx.Request("GET", "https://api.cordum.example/jobs")
    api_key_headers: dict[str, str] = {}
    bearer_headers: dict[str, str] = {}

    ApiKeyAuth("secret-key").apply(api_key_headers, request)
    BearerTokenAuth("secret-token").apply(bearer_headers, request)

    assert api_key_headers == {"X-API-Key": "secret-key"}
    assert bearer_headers == {"Authorization": "Bearer secret-token"}


@pytest.mark.asyncio
async def test_api_key_auth_header_injection_async() -> None:
    request = httpx.Request("GET", "https://api.cordum.example/jobs")
    headers: dict[str, str] = {}

    await _apply_auth(ApiKeyAuth("async-key"), headers, request)

    assert headers == {"X-API-Key": "async-key"}


def test_session_auth_lazy_login_cache_and_refresh_on_401() -> None:
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    login_route = router.post("https://api.cordum.example/api/v1/auth/login").mock(
        side_effect=[
            httpx.Response(200, json={"token": "token-1"}),
            httpx.Response(200, json={"token": "token-2"}),
        ]
    )
    base_client = httpx.Client(
        base_url="https://api.cordum.example",
        transport=httpx.MockTransport(router.handler),
    )
    auth = SessionAuth(base_client, "user@example.com", "super-secret")
    request = httpx.Request("GET", "https://api.cordum.example/api/v1/jobs")

    headers_one: dict[str, str] = {}
    auth.apply(headers_one, request)
    headers_two: dict[str, str] = {}
    auth.apply(headers_two, request)

    assert headers_one["Authorization"] == "Bearer token-1"
    assert headers_two["Authorization"] == "Bearer token-1"
    assert login_route.call_count == 1

    unauthorized = httpx.Response(401, request=request)
    assert auth.handle_response(unauthorized) is True

    headers_three: dict[str, str] = {}
    auth.apply(headers_three, request)

    assert headers_three["Authorization"] == "Bearer token-2"
    assert login_route.call_count == 2
    base_client.close()


@pytest.mark.asyncio
async def test_session_auth_async_login_path_injects_header() -> None:
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    login_route = router.post("https://api.cordum.example/api/v1/auth/login").mock(
        return_value=httpx.Response(200, json={"token": "async-session-token"})
    )
    async_client = httpx.AsyncClient(
        base_url="https://api.cordum.example",
        transport=httpx.MockTransport(router.async_handler),
    )
    auth = SessionAuth(async_client, "user@example.com", "super-secret", totp="123456")
    request = httpx.Request("GET", "https://api.cordum.example/api/v1/jobs")
    headers: dict[str, str] = {}

    await _apply_auth(auth, headers, request)

    assert headers["Authorization"] == "Bearer async-session-token"
    assert login_route.call_count == 1
    await async_client.aclose()


def test_saml_auth_round_trip_with_acs_callback() -> None:
    router = respx.MockRouter(assert_all_called=True, assert_all_mocked=True)
    acs_route = router.post("https://api.cordum.example/api/v1/auth/saml/acs").mock(
        return_value=httpx.Response(200, json={"session_token": "saml-session-token"})
    )
    callback_client = httpx.Client(
        base_url="https://api.cordum.example",
        transport=httpx.MockTransport(router.handler),
    )

    def acs_callback(relay_state: str) -> MutableMapping[str, Any]:
        response = callback_client.post("/api/v1/auth/saml/acs", json={"relay_state": relay_state})
        response.raise_for_status()
        return response.json()

    auth = SamlAuth("https://idp.example/sso/start", acs_callback)
    request = httpx.Request("GET", "https://api.cordum.example/api/v1/jobs")
    headers: dict[str, str] = {}

    assert auth.initiate() == "https://idp.example/sso/start"
    token = auth.complete("relay-state-123")
    auth.apply(headers, request)

    assert token == "saml-session-token"
    assert headers["Authorization"] == "Bearer saml-session-token"
    assert acs_route.call_count == 1
    callback_client.close()


def test_auth_repr_redacts_secrets() -> None:
    api_key_auth = ApiKeyAuth("secret-key")
    bearer_auth = BearerTokenAuth("secret-token")
    session_auth = SessionAuth(
        "https://api.cordum.example",
        "user@example.com",
        "super-secret",
        totp="123456",
    )
    saml_auth = SamlAuth("https://idp.example/sso/start", lambda relay: "session-token")
    saml_auth.complete("relay-state")

    for rendered in (
        repr(api_key_auth),
        repr(bearer_auth),
        repr(session_auth),
        repr(saml_auth),
    ):
        assert "secret-key" not in rendered
        assert "secret-token" not in rendered
        assert "super-secret" not in rendered
        assert "123456" not in rendered
        assert "session-token" not in rendered
        assert "<redacted>" in rendered
