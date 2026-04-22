from __future__ import annotations

import asyncio
import base64
import inspect
import json
import threading
from datetime import datetime, timedelta, timezone
from typing import Any, Awaitable, Callable, MutableMapping, Optional, Protocol, Union

import httpx

from .errors import AuthenticationError

AuthApplyResult = Optional[Awaitable[None]]
SamlCallbackValue = Union[str, MutableMapping[str, Any]]
SamlCallbackResult = Union[SamlCallbackValue, Awaitable[SamlCallbackValue]]


class AuthProvider(Protocol):
    def apply(
        self, headers: MutableMapping[str, str], request: httpx.Request
    ) -> AuthApplyResult: ...


def _redact_secret(value: Optional[str]) -> str:
    return "<redacted>" if value else "<unset>"


def _in_async_context() -> bool:
    try:
        asyncio.get_running_loop()
    except RuntimeError:
        return False
    return True


def _origin_base_url(request: httpx.Request) -> str:
    if request.url.port is None:
        return "{scheme}://{host}".format(scheme=request.url.scheme, host=request.url.host)
    return "{scheme}://{host}:{port}".format(
        scheme=request.url.scheme,
        host=request.url.host,
        port=request.url.port,
    )


def _jwt_expiration(token: str) -> Optional[datetime]:
    parts = token.split(".")
    if len(parts) != 3:
        return None

    payload = parts[1]
    payload += "=" * (-len(payload) % 4)
    try:
        decoded = base64.urlsafe_b64decode(payload.encode("utf-8"))
        data = json.loads(decoded.decode("utf-8"))
    except (ValueError, json.JSONDecodeError, UnicodeDecodeError):
        return None

    exp = data.get("exp")
    if not isinstance(exp, (int, float)):
        return None
    return datetime.fromtimestamp(float(exp), tz=timezone.utc)


class ApiKeyAuth:
    def __init__(self, key: str, header: str = "X-API-Key") -> None:
        self.key = key
        self.header = header

    def apply(self, headers: MutableMapping[str, str], request: httpx.Request) -> AuthApplyResult:
        del request
        headers[self.header] = self.key
        return None

    def __repr__(self) -> str:
        return "ApiKeyAuth(header={!r}, key={!r})".format(self.header, _redact_secret(self.key))


class BearerTokenAuth:
    def __init__(self, token: str) -> None:
        self.token = token

    def apply(self, headers: MutableMapping[str, str], request: httpx.Request) -> AuthApplyResult:
        del request
        headers["Authorization"] = "Bearer {token}".format(token=self.token)
        return None

    def __repr__(self) -> str:
        return "BearerTokenAuth(token={!r})".format(_redact_secret(self.token))


class SessionAuth:
    def __init__(
        self,
        client: Union[str, httpx.Client, httpx.AsyncClient, None],
        email: str,
        password: str,
        totp: Optional[str] = None,
        *,
        login_path: str = "/api/v1/auth/login",
        refresh_leeway_s: int = 30,
    ) -> None:
        self._client_source = client
        self.email = email
        self.password = password
        self.totp = totp
        self.login_path = login_path
        self.refresh_leeway_s = refresh_leeway_s

        self._token: Optional[str] = None
        self._expires_at: Optional[datetime] = None
        self._sync_lock = threading.Lock()
        self._async_lock: Optional[asyncio.Lock] = None
        self._owned_sync_client: Optional[httpx.Client] = None
        self._owned_async_client: Optional[httpx.AsyncClient] = None

    def __repr__(self) -> str:
        return "SessionAuth(email={!r}, password={!r}, totp={!r}, token={!r})".format(
            self.email,
            _redact_secret(self.password),
            _redact_secret(self.totp),
            _redact_secret(self._token),
        )

    def apply(self, headers: MutableMapping[str, str], request: httpx.Request) -> AuthApplyResult:
        if _in_async_context():
            return self._apply_async(headers, request)
        self._apply_sync(headers, request)
        return None

    def invalidate(self) -> None:
        self._token = None
        self._expires_at = None

    def handle_response(self, response: httpx.Response) -> bool:
        if response.status_code != 401:
            return False
        self.invalidate()
        return True

    def close(self) -> None:
        if self._owned_sync_client is not None:
            self._owned_sync_client.close()
            self._owned_sync_client = None

    async def aclose(self) -> None:
        if self._owned_async_client is not None:
            await self._owned_async_client.aclose()
            self._owned_async_client = None

    def _apply_sync(self, headers: MutableMapping[str, str], request: httpx.Request) -> None:
        with self._sync_lock:
            if self._needs_login():
                self._login_sync(request)
            if self._token is None:
                raise AuthenticationError("session login did not yield a token")
            headers["Authorization"] = "Bearer {token}".format(token=self._token)

    async def _apply_async(self, headers: MutableMapping[str, str], request: httpx.Request) -> None:
        lock = self._get_async_lock()
        async with lock:
            if self._needs_login():
                await self._login_async(request)
            if self._token is None:
                raise AuthenticationError("session login did not yield a token")
            headers["Authorization"] = "Bearer {token}".format(token=self._token)

    def _get_async_lock(self) -> asyncio.Lock:
        if self._async_lock is None:
            self._async_lock = asyncio.Lock()
        return self._async_lock

    def _needs_login(self) -> bool:
        if self._token is None:
            return True
        expires_at = self._expires_at or _jwt_expiration(self._token)
        if expires_at is None:
            return False
        refresh_at = datetime.now(timezone.utc) + timedelta(seconds=self.refresh_leeway_s)
        return expires_at <= refresh_at

    def _login_payload(self, request: httpx.Request) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "username": self.email,
            "password": self.password,
        }
        tenant = request.headers.get("X-Tenant-ID")
        if tenant:
            payload["tenant"] = tenant
        if self.totp:
            payload["totp"] = self.totp
        return payload

    def _update_token(self, data: MutableMapping[str, Any]) -> None:
        token = data.get("token") or data.get("session_token") or data.get("access_token")
        if not isinstance(token, str) or not token:
            raise AuthenticationError("session login response did not include a token")

        expires_at = None
        raw_expires_at = data.get("expires_at") or data.get("expiresAt")
        if isinstance(raw_expires_at, str):
            try:
                expires_at = datetime.fromisoformat(raw_expires_at.replace("Z", "+00:00"))
            except ValueError:
                expires_at = None
        if expires_at is None:
            expires_at = _jwt_expiration(token)

        self._token = token
        self._expires_at = expires_at

    def _login_sync(self, request: httpx.Request) -> None:
        client = self._get_sync_client(request)
        response = client.post(self.login_path, json=self._login_payload(request))
        response.raise_for_status()
        self._update_token(response.json())

    async def _login_async(self, request: httpx.Request) -> None:
        client = await self._get_async_client(request)
        response = await client.post(self.login_path, json=self._login_payload(request))
        response.raise_for_status()
        self._update_token(response.json())

    def _base_url(self, request: httpx.Request) -> str:
        source = self._client_source
        if isinstance(source, str):
            return source
        if isinstance(source, httpx.Client):
            return str(source.base_url)
        if isinstance(source, httpx.AsyncClient):
            return str(source.base_url)
        return _origin_base_url(request)

    def _get_sync_client(self, request: httpx.Request) -> httpx.Client:
        source = self._client_source
        if isinstance(source, httpx.Client):
            return source
        if self._owned_sync_client is None:
            self._owned_sync_client = httpx.Client(base_url=self._base_url(request))
        return self._owned_sync_client

    async def _get_async_client(self, request: httpx.Request) -> httpx.AsyncClient:
        source = self._client_source
        if isinstance(source, httpx.AsyncClient):
            return source
        if self._owned_async_client is None:
            self._owned_async_client = httpx.AsyncClient(base_url=self._base_url(request))
        return self._owned_async_client


class SamlAuth:
    def __init__(
        self,
        sso_start_url: str,
        acs_callback: Callable[[str], SamlCallbackResult],
    ) -> None:
        self.sso_start_url = sso_start_url
        self._acs_callback = acs_callback
        self._session_token: Optional[str] = None

    def __repr__(self) -> str:
        return "SamlAuth(sso_start_url={!r}, session_token={!r})".format(
            self.sso_start_url,
            _redact_secret(self._session_token),
        )

    def initiate(self) -> str:
        return self.sso_start_url

    def complete(self, session_token_or_relay: str) -> str:
        result = self._acs_callback(session_token_or_relay)
        if inspect.isawaitable(result):
            raise TypeError("acs_callback returned an awaitable; use acomplete() instead")
        return self._store_completion_result(result)

    async def acomplete(self, session_token_or_relay: str) -> str:
        result = self._acs_callback(session_token_or_relay)
        if inspect.isawaitable(result):
            result = await result
        return self._store_completion_result(result)

    def apply(self, headers: MutableMapping[str, str], request: httpx.Request) -> AuthApplyResult:
        del request
        if not self._session_token:
            raise AuthenticationError("SAML session not established")
        headers["Authorization"] = "Bearer {token}".format(token=self._session_token)
        return None

    def _store_completion_result(self, result: Union[str, MutableMapping[str, Any]]) -> str:
        token: Optional[str]
        if isinstance(result, str):
            token = result
        else:
            token = result.get("session_token") or result.get("token")
            if not isinstance(token, str):
                raise AuthenticationError("SAML ACS callback did not return a session token")
        self._session_token = token
        return token
