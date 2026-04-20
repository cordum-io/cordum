from __future__ import annotations

import httpx
import pytest

from cordum_sdk import AsyncCordumClient, CordumClient
from cordum_sdk.auth import BearerTokenAuth, SamlAuth, SessionAuth
from cordum_sdk.errors import AuthenticationError


def test_api_key_auth_success_and_wrong_key_401(respx_router, gateway_state) -> None:
    del gateway_state
    good_client = CordumClient(
        "https://api.example.test",
        auth="good-api-key",
        tenant_id="tenant-123",
    )
    assert good_client.jobs.list_jobs().items == []
    good_client.close()

    bad_client = CordumClient(
        "https://api.example.test",
        auth="wrong-api-key",
        tenant_id="tenant-123",
    )
    with pytest.raises(AuthenticationError):
        bad_client.jobs.list_jobs()
    bad_client.close()

    assert respx_router.calls.last.request.headers["X-API-Key"] == "wrong-api-key"


def test_bearer_token_pass_through(respx_router, gateway_state) -> None:
    del gateway_state
    client = CordumClient(
        "https://api.example.test",
        auth=BearerTokenAuth("bearer-token"),
        tenant_id="tenant-123",
    )

    client.jobs.list_jobs()
    client.close()

    assert respx_router.calls.last.request.headers["Authorization"] == "Bearer bearer-token"


@pytest.mark.asyncio
async def test_session_auth_login_and_refresh_on_401(respx_router, gateway_state) -> None:
    del respx_router
    gateway_state.force_session_refresh = True

    auth = SessionAuth(
        "https://api.example.test",
        email="sdk@example.com",
        password="correct-password",
    )
    async with AsyncCordumClient(
        "https://api.example.test",
        auth=auth,
        tenant_id="tenant-123",
    ) as client:
        listed = await client.jobs.list_jobs()
        assert listed.items == []

    assert gateway_state.login_calls == 2
    assert gateway_state.request_log.count(("GET", "/api/v1/jobs")) == 2


def test_saml_auth_initiate_complete_and_use_session(respx_router, gateway_state) -> None:
    del respx_router

    def acs_callback(relay_state: str) -> dict[str, str]:
        with httpx.Client(base_url="https://api.example.test") as http_client:
            response = http_client.post("/api/v1/auth/saml/acs", json={"relay_state": relay_state})
            response.raise_for_status()
            return response.json()

    auth = SamlAuth("https://idp.example.test/start", acs_callback)
    assert auth.initiate() == "https://idp.example.test/start"
    token = auth.complete("relay-123")
    assert token == gateway_state.saml_session_token

    client = CordumClient(
        "https://api.example.test",
        auth=auth,
        tenant_id="tenant-123",
    )
    assert client.jobs.list_jobs().items == []
    client.close()
