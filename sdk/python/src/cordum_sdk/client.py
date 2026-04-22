"""Public Cordum SDK client facade.

The OpenAPI-generated client is vendored in ``cordum_sdk._generated`` and can be
regenerated from the spec at any time. This hand-written facade keeps the
public API ergonomic by grouping raw generated functions into stable resource
namespaces (``client.jobs``, ``client.workflows``, and friends) without editing
machine-generated code directly.
"""

from __future__ import annotations

import asyncio
import inspect
import json
import logging
from dataclasses import dataclass
from types import ModuleType
from typing import Any, Dict, Optional, Union

import httpx

from . import __version__
from ._generated.client import Client as GeneratedClient
from ._namespaces import ResourceNamespace, get_available_namespaces, get_namespace_operations
from ._retry import AsyncRetryTransport, RetryPolicy, RetryTransport
from .auth import ApiKeyAuth, AuthProvider
from .errors import CordumError, _raise_for_status, map_httpx_error

DEFAULT_USER_AGENT = "cordum-sdk-python/{version}".format(version=__version__)


@dataclass(frozen=True)
class RequestResponse:
    status_code: int
    headers: Dict[str, str]
    content: bytes
    data: Any
    text: str


def _normalize_base_url(base_url: str) -> str:
    return base_url.rstrip("/")


def _coerce_auth(auth: Optional[Union[AuthProvider, str]]) -> Optional[AuthProvider]:
    if auth is None:
        return None
    if isinstance(auth, str):
        return ApiKeyAuth(auth)
    return auth


def _requires_tenant_id(operation: Any) -> bool:
    return "x_tenant_id" in inspect.signature(operation).parameters


def _to_httpx_response(response: Any) -> httpx.Response:
    return httpx.Response(
        int(response.status_code),
        headers=response.headers,
        content=response.content,
    )


def _parse_httpx_response(response: httpx.Response) -> RequestResponse:
    content = response.content
    text = response.text
    content_type = response.headers.get("Content-Type", "").lower()

    data: Any = None
    if content:
        if "application/json" in content_type:
            try:
                data = response.json()
            except json.JSONDecodeError:
                data = text
        else:
            data = text

    return RequestResponse(
        status_code=int(response.status_code),
        headers=dict(response.headers),
        content=content,
        data=data,
        text=text,
    )


class _ClientCommon:
    def __init__(
        self,
        *,
        base_url: str,
        auth: Optional[Union[AuthProvider, str]],
        timeout: Union[float, httpx.Timeout],
        retry_policy: Optional[RetryPolicy],
        tenant_id: Optional[str],
        user_agent: Optional[str],
        logger: Optional[logging.Logger],
        async_mode: bool,
    ) -> None:
        self.base_url = _normalize_base_url(base_url)
        self.auth = _coerce_auth(auth)
        self.timeout = timeout
        self.retry_policy = retry_policy or RetryPolicy()
        self.tenant_id = tenant_id
        self.user_agent = user_agent or DEFAULT_USER_AGENT
        self.logger = logger or logging.getLogger(__name__)
        self._async_mode = async_mode
        self._namespaces: Dict[str, ResourceNamespace] = {}

    def __dir__(self) -> list[str]:
        return sorted(set(super().__dir__()) | set(get_available_namespaces()))

    def __getattr__(self, name: str) -> ResourceNamespace:
        if name.startswith("_"):
            raise AttributeError(name)

        existing = self._namespaces.get(name)
        if existing is not None:
            return existing

        try:
            operations = get_namespace_operations(name)
        except KeyError as exc:
            raise AttributeError(
                "{cls} has no attribute {name!r}".format(
                    cls=self.__class__.__name__,
                    name=name,
                )
            ) from exc

        namespace = ResourceNamespace(
            owner=self,
            namespace_name=name,
            operations=operations,
            async_mode=self._async_mode,
        )
        self._namespaces[name] = namespace
        return namespace

    def _prepare_operation_kwargs(self, operation: Any, kwargs: Dict[str, Any]) -> Dict[str, Any]:
        prepared = dict(kwargs)
        if _requires_tenant_id(operation) and "x_tenant_id" not in prepared:
            if not self.tenant_id:
                raise TypeError(
                    "Operation {name} requires x_tenant_id or a client tenant_id".format(
                        name=operation.__module__.rsplit(".", 1)[-1]
                    )
                )
            prepared["x_tenant_id"] = self.tenant_id
        return prepared

    def _refresh_auth_if_needed(self, response: httpx.Response) -> bool:
        handler = getattr(self.auth, "handle_response", None)
        if handler is None:
            return False
        return bool(handler(response))


class CordumClient(_ClientCommon):
    def __init__(
        self,
        base_url: str,
        auth: Optional[Union[AuthProvider, str]] = None,
        *,
        timeout: Union[float, httpx.Timeout] = 30.0,
        retry_policy: Optional[RetryPolicy] = None,
        tenant_id: Optional[str] = None,
        user_agent: Optional[str] = None,
        transport: Optional[httpx.BaseTransport] = None,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        super().__init__(
            base_url=base_url,
            auth=auth,
            timeout=timeout,
            retry_policy=retry_policy,
            tenant_id=tenant_id,
            user_agent=user_agent,
            logger=logger,
            async_mode=False,
        )
        self._transport = RetryTransport(
            policy=self.retry_policy,
            transport=transport or httpx.HTTPTransport(),
        )
        self._httpx_client = httpx.Client(
            base_url=self.base_url,
            timeout=self.timeout,
            headers={"User-Agent": self.user_agent},
            event_hooks={"request": [self._on_request]},
            transport=self._transport,
        )
        self._generated_client = GeneratedClient(
            base_url=self.base_url,
            raise_on_unexpected_status=False,
        ).set_httpx_client(self._httpx_client)

    def _on_request(self, request: httpx.Request) -> None:
        if self.tenant_id and "X-Cordum-Tenant" not in request.headers:
            request.headers["X-Cordum-Tenant"] = self.tenant_id

        if self.auth is None:
            return

        result = self.auth.apply(request.headers, request)
        if inspect.isawaitable(result):
            raise TypeError(
                "Sync client auth provider returned an awaitable; "
                "use AsyncCordumClient"
            )

    def _invoke_detailed(self, module: ModuleType, *args: Any, **kwargs: Any) -> Any:
        operation = module.sync_detailed
        prepared_kwargs = self._prepare_operation_kwargs(operation, kwargs)

        try:
            response = operation(*args, client=self._generated_client, **prepared_kwargs)
        except CordumError:
            raise
        except httpx.HTTPError as exc:
            raise map_httpx_error(exc) from exc

        raw_response = _to_httpx_response(response)
        if response.status_code == 401 and self._refresh_auth_if_needed(raw_response):
            try:
                response = operation(*args, client=self._generated_client, **prepared_kwargs)
            except CordumError:
                raise
            except httpx.HTTPError as exc:
                raise map_httpx_error(exc) from exc
            raw_response = _to_httpx_response(response)

        _raise_for_status(raw_response)
        return response

    def _invoke_operation(self, module: ModuleType, *args: Any, **kwargs: Any) -> Any:
        return self._invoke_detailed(module, *args, **kwargs).parsed

    def _invoke_paginated(self, module: ModuleType, *args: Any, **kwargs: Any) -> Any:
        response = self._invoke_detailed(module, *args, **kwargs)
        return response.parsed, response.headers

    def _stream(self, *args: Any, **kwargs: Any) -> Any:
        from .streaming import stream_events

        return stream_events(self._httpx_client, *args, **kwargs)

    def request_detailed(
        self,
        method: str,
        path: str,
        *,
        query: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
        json_body: Any = None,
        content: Any = None,
    ) -> RequestResponse:
        try:
            response = self._httpx_client.request(
                method,
                path,
                params=query,
                headers=headers,
                json=json_body,
                content=content,
            )
        except CordumError:
            raise
        except httpx.HTTPError as exc:
            raise map_httpx_error(exc) from exc

        if response.status_code == 401 and self._refresh_auth_if_needed(response):
            try:
                response = self._httpx_client.request(
                    method,
                    path,
                    params=query,
                    headers=headers,
                    json=json_body,
                    content=content,
                )
            except CordumError:
                raise
            except httpx.HTTPError as exc:
                raise map_httpx_error(exc) from exc

        _raise_for_status(response)
        return _parse_httpx_response(response)

    def request(
        self,
        method: str,
        path: str,
        *,
        query: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
        json_body: Any = None,
        content: Any = None,
    ) -> Any:
        return self.request_detailed(
            method,
            path,
            query=query,
            headers=headers,
            json_body=json_body,
            content=content,
        ).data

    def stream(
        self,
        *,
        path: str = "/api/v1/stream",
        method: str = "GET",
        headers: Optional[Dict[str, str]] = None,
        query: Optional[Dict[str, Any]] = None,
        json_body: Any = None,
        content: Any = None,
    ) -> Any:
        return self._stream(
            path=path,
            method=method,
            headers=headers,
            params=query,
            json_body=json_body,
            content=content,
        )

    def close(self) -> None:
        self._httpx_client.close()
        close = getattr(self.auth, "close", None)
        if close is not None:
            close()

    async def aclose(self) -> None:
        self.close()

    def __enter__(self) -> "CordumClient":
        return self

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        del exc_type, exc, tb
        self.close()


class AsyncCordumClient(_ClientCommon):
    def __init__(
        self,
        base_url: str,
        auth: Optional[Union[AuthProvider, str]] = None,
        *,
        timeout: Union[float, httpx.Timeout] = 30.0,
        retry_policy: Optional[RetryPolicy] = None,
        tenant_id: Optional[str] = None,
        user_agent: Optional[str] = None,
        transport: Optional[httpx.AsyncBaseTransport] = None,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        super().__init__(
            base_url=base_url,
            auth=auth,
            timeout=timeout,
            retry_policy=retry_policy,
            tenant_id=tenant_id,
            user_agent=user_agent,
            logger=logger,
            async_mode=True,
        )
        self._transport = AsyncRetryTransport(
            policy=self.retry_policy,
            transport=transport or httpx.AsyncHTTPTransport(),
        )
        self._httpx_client = httpx.AsyncClient(
            base_url=self.base_url,
            timeout=self.timeout,
            headers={"User-Agent": self.user_agent},
            event_hooks={"request": [self._on_request]},
            transport=self._transport,
        )
        self._generated_client = GeneratedClient(
            base_url=self.base_url,
            raise_on_unexpected_status=False,
        ).set_async_httpx_client(self._httpx_client)

    async def _on_request(self, request: httpx.Request) -> None:
        if self.tenant_id and "X-Cordum-Tenant" not in request.headers:
            request.headers["X-Cordum-Tenant"] = self.tenant_id

        if self.auth is None:
            return

        result = self.auth.apply(request.headers, request)
        if inspect.isawaitable(result):
            await result

    async def _invoke_detailed(self, module: ModuleType, *args: Any, **kwargs: Any) -> Any:
        operation = module.asyncio_detailed
        prepared_kwargs = self._prepare_operation_kwargs(operation, kwargs)

        try:
            response = await operation(*args, client=self._generated_client, **prepared_kwargs)
        except CordumError:
            raise
        except httpx.HTTPError as exc:
            raise map_httpx_error(exc) from exc

        raw_response = _to_httpx_response(response)
        if response.status_code == 401 and self._refresh_auth_if_needed(raw_response):
            try:
                response = await operation(*args, client=self._generated_client, **prepared_kwargs)
            except CordumError:
                raise
            except httpx.HTTPError as exc:
                raise map_httpx_error(exc) from exc
            raw_response = _to_httpx_response(response)

        _raise_for_status(raw_response)
        return response

    async def _invoke_operation(self, module: ModuleType, *args: Any, **kwargs: Any) -> Any:
        response = await self._invoke_detailed(module, *args, **kwargs)
        return response.parsed

    async def _invoke_paginated(self, module: ModuleType, *args: Any, **kwargs: Any) -> Any:
        response = await self._invoke_detailed(module, *args, **kwargs)
        return response.parsed, response.headers

    def _stream(self, *args: Any, **kwargs: Any) -> Any:
        from .streaming import stream_events_async

        return stream_events_async(self._httpx_client, *args, **kwargs)

    async def request_detailed(
        self,
        method: str,
        path: str,
        *,
        query: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
        json_body: Any = None,
        content: Any = None,
    ) -> RequestResponse:
        try:
            response = await self._httpx_client.request(
                method,
                path,
                params=query,
                headers=headers,
                json=json_body,
                content=content,
            )
        except CordumError:
            raise
        except httpx.HTTPError as exc:
            raise map_httpx_error(exc) from exc

        if response.status_code == 401 and self._refresh_auth_if_needed(response):
            try:
                response = await self._httpx_client.request(
                    method,
                    path,
                    params=query,
                    headers=headers,
                    json=json_body,
                    content=content,
                )
            except CordumError:
                raise
            except httpx.HTTPError as exc:
                raise map_httpx_error(exc) from exc

        _raise_for_status(response)
        return _parse_httpx_response(response)

    async def request(
        self,
        method: str,
        path: str,
        *,
        query: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
        json_body: Any = None,
        content: Any = None,
    ) -> Any:
        response = await self.request_detailed(
            method,
            path,
            query=query,
            headers=headers,
            json_body=json_body,
            content=content,
        )
        return response.data

    def stream(
        self,
        *,
        path: str = "/api/v1/stream",
        method: str = "GET",
        headers: Optional[Dict[str, str]] = None,
        query: Optional[Dict[str, Any]] = None,
        json_body: Any = None,
        content: Any = None,
    ) -> Any:
        return self._stream(
            path=path,
            method=method,
            headers=headers,
            params=query,
            json_body=json_body,
            content=content,
        )

    def close(self) -> None:
        try:
            asyncio.get_running_loop()
        except RuntimeError:
            asyncio.run(self.aclose())
            return
        raise RuntimeError("close() cannot be used while an event loop is running; use aclose()")

    async def aclose(self) -> None:
        await self._httpx_client.aclose()
        aclose = getattr(self.auth, "aclose", None)
        if aclose is not None:
            await aclose()
            return
        close = getattr(self.auth, "close", None)
        if close is not None:
            close()

    async def __aenter__(self) -> "AsyncCordumClient":
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        del exc_type, exc, tb
        await self.aclose()
