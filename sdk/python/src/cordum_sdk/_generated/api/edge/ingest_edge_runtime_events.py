from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_error import EdgeError
from ...models.edge_runtime_ingest_request import EdgeRuntimeIngestRequest
from ...models.edge_runtime_ingest_response import EdgeRuntimeIngestResponse
from typing import cast
from typing import Dict


def _get_kwargs(
    *,
    body: EdgeRuntimeIngestRequest,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/runtime/events",
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeError, EdgeRuntimeIngestResponse]]:
    if response.status_code == 201:
        response_201 = EdgeRuntimeIngestResponse.from_dict(response.json())

        return response_201
    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404
    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413
    if response.status_code == 429:
        response_429 = EdgeError.from_dict(response.json())

        return response_429
    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, EdgeError, EdgeRuntimeIngestResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeRuntimeIngestRequest,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeError, EdgeRuntimeIngestResponse]]:
    """Ingest bounded runtime telemetry from a trusted sidecar

     Disabled by default. When `CORDUM_EDGE_RUNTIME_INGEST_ENABLED` is unset (or set to a non-truthy
    value), the route returns 503 `service_unavailable` and persists nothing. When enabled, the endpoint
    accepts a bounded, redacted runtime event batch (process exec, file read/write, network connect, DNS
    query) from an authenticated runtime collector holding `edge.runtime.ingest`; generic job writers
    are forbidden. The request `source.source_id` must match the authenticated collector principal and
    the referenced session/execution must be bound to that collector before mapped `AgentActionEvent`
    records with `layer=runtime` and `decision=RECORDED` are persisted through the existing Edge store.
    Raw argv, file contents, packet payloads, DNS response bodies, request bodies, headers, secrets, and
    tokens are rejected at the strict-schema decode boundary. All-or-nothing batch acceptance — a single
    invalid envelope aborts the whole batch. See `docs/edge/runtime-ingestion.md` for the full contract.

    Args:
        x_tenant_id (str):
        body (EdgeRuntimeIngestRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeError, EdgeRuntimeIngestResponse]]
    """

    kwargs = _get_kwargs(
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeRuntimeIngestRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeError, EdgeRuntimeIngestResponse]]:
    """Ingest bounded runtime telemetry from a trusted sidecar

     Disabled by default. When `CORDUM_EDGE_RUNTIME_INGEST_ENABLED` is unset (or set to a non-truthy
    value), the route returns 503 `service_unavailable` and persists nothing. When enabled, the endpoint
    accepts a bounded, redacted runtime event batch (process exec, file read/write, network connect, DNS
    query) from an authenticated runtime collector holding `edge.runtime.ingest`; generic job writers
    are forbidden. The request `source.source_id` must match the authenticated collector principal and
    the referenced session/execution must be bound to that collector before mapped `AgentActionEvent`
    records with `layer=runtime` and `decision=RECORDED` are persisted through the existing Edge store.
    Raw argv, file contents, packet payloads, DNS response bodies, request bodies, headers, secrets, and
    tokens are rejected at the strict-schema decode boundary. All-or-nothing batch acceptance — a single
    invalid envelope aborts the whole batch. See `docs/edge/runtime-ingestion.md` for the full contract.

    Args:
        x_tenant_id (str):
        body (EdgeRuntimeIngestRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeError, EdgeRuntimeIngestResponse]
    """

    return sync_detailed(
        client=client,
        body=body,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeRuntimeIngestRequest,
    x_tenant_id: str,
) -> Response[Union[Any, EdgeError, EdgeRuntimeIngestResponse]]:
    """Ingest bounded runtime telemetry from a trusted sidecar

     Disabled by default. When `CORDUM_EDGE_RUNTIME_INGEST_ENABLED` is unset (or set to a non-truthy
    value), the route returns 503 `service_unavailable` and persists nothing. When enabled, the endpoint
    accepts a bounded, redacted runtime event batch (process exec, file read/write, network connect, DNS
    query) from an authenticated runtime collector holding `edge.runtime.ingest`; generic job writers
    are forbidden. The request `source.source_id` must match the authenticated collector principal and
    the referenced session/execution must be bound to that collector before mapped `AgentActionEvent`
    records with `layer=runtime` and `decision=RECORDED` are persisted through the existing Edge store.
    Raw argv, file contents, packet payloads, DNS response bodies, request bodies, headers, secrets, and
    tokens are rejected at the strict-schema decode boundary. All-or-nothing batch acceptance — a single
    invalid envelope aborts the whole batch. See `docs/edge/runtime-ingestion.md` for the full contract.

    Args:
        x_tenant_id (str):
        body (EdgeRuntimeIngestRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeError, EdgeRuntimeIngestResponse]]
    """

    kwargs = _get_kwargs(
        body=body,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    body: EdgeRuntimeIngestRequest,
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeError, EdgeRuntimeIngestResponse]]:
    """Ingest bounded runtime telemetry from a trusted sidecar

     Disabled by default. When `CORDUM_EDGE_RUNTIME_INGEST_ENABLED` is unset (or set to a non-truthy
    value), the route returns 503 `service_unavailable` and persists nothing. When enabled, the endpoint
    accepts a bounded, redacted runtime event batch (process exec, file read/write, network connect, DNS
    query) from an authenticated runtime collector holding `edge.runtime.ingest`; generic job writers
    are forbidden. The request `source.source_id` must match the authenticated collector principal and
    the referenced session/execution must be bound to that collector before mapped `AgentActionEvent`
    records with `layer=runtime` and `decision=RECORDED` are persisted through the existing Edge store.
    Raw argv, file contents, packet payloads, DNS response bodies, request bodies, headers, secrets, and
    tokens are rejected at the strict-schema decode boundary. All-or-nothing batch acceptance — a single
    invalid envelope aborts the whole batch. See `docs/edge/runtime-ingestion.md` for the full contract.

    Args:
        x_tenant_id (str):
        body (EdgeRuntimeIngestRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeError, EdgeRuntimeIngestResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
