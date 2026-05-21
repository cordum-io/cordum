from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.mcp_upstream_server import MCPUpstreamServer
from ...models.mcp_upstream_server_write_request import MCPUpstreamServerWriteRequest
from ...models.mcp_upstream_validation_response import MCPUpstreamValidationResponse
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union


def _get_kwargs(
    *,
    body: MCPUpstreamServerWriteRequest,
    validate_only: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["validate_only"] = validate_only

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "post",
        "url": "/api/v1/edge/mcp/upstreams",
        "params": params,
    }

    _body = body.to_dict()

    _kwargs["json"] = _body
    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]]:
    if response.status_code == 200:
        response_200 = MCPUpstreamValidationResponse.from_dict(response.json())

        return response_200
    if response.status_code == 201:
        response_201 = MCPUpstreamServer.from_dict(response.json())

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
    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409
    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413
    if response.status_code == 429:
        response_429 = cast(Any, None)
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
) -> Response[Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: MCPUpstreamServerWriteRequest,
    validate_only: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Response[Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]]:
    """Create or validate an upstream MCP server entry

     Creates a tenant-scoped upstream MCP registry entry. With `validate_only=true`, validates the entry
    without storing it and returns a validation verdict. Raw secrets are rejected; use `secret://` auth
    references.

    Args:
        validate_only (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):
        body (MCPUpstreamServerWriteRequest): Write payload for an upstream MCP entry. `tenant_id`
            may be omitted and is resolved from X-Tenant-ID; `created_at` and `updated_at` are server-
            managed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]]
    """

    kwargs = _get_kwargs(
        body=body,
        validate_only=validate_only,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    body: MCPUpstreamServerWriteRequest,
    validate_only: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Optional[Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]]:
    """Create or validate an upstream MCP server entry

     Creates a tenant-scoped upstream MCP registry entry. With `validate_only=true`, validates the entry
    without storing it and returns a validation verdict. Raw secrets are rejected; use `secret://` auth
    references.

    Args:
        validate_only (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):
        body (MCPUpstreamServerWriteRequest): Write payload for an upstream MCP entry. `tenant_id`
            may be omitted and is resolved from X-Tenant-ID; `created_at` and `updated_at` are server-
            managed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]
    """

    return sync_detailed(
        client=client,
        body=body,
        validate_only=validate_only,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    body: MCPUpstreamServerWriteRequest,
    validate_only: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Response[Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]]:
    """Create or validate an upstream MCP server entry

     Creates a tenant-scoped upstream MCP registry entry. With `validate_only=true`, validates the entry
    without storing it and returns a validation verdict. Raw secrets are rejected; use `secret://` auth
    references.

    Args:
        validate_only (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):
        body (MCPUpstreamServerWriteRequest): Write payload for an upstream MCP entry. `tenant_id`
            may be omitted and is resolved from X-Tenant-ID; `created_at` and `updated_at` are server-
            managed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]]
    """

    kwargs = _get_kwargs(
        body=body,
        validate_only=validate_only,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    body: MCPUpstreamServerWriteRequest,
    validate_only: Union[Unset, bool] = False,
    x_tenant_id: str,
) -> Optional[Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]]:
    """Create or validate an upstream MCP server entry

     Creates a tenant-scoped upstream MCP registry entry. With `validate_only=true`, validates the entry
    without storing it and returns a validation verdict. Raw secrets are rejected; use `secret://` auth
    references.

    Args:
        validate_only (Union[Unset, bool]):  Default: False.
        x_tenant_id (str):
        body (MCPUpstreamServerWriteRequest): Write payload for an upstream MCP entry. `tenant_id`
            may be omitted and is resolved from X-Tenant-ID; `created_at` and `updated_at` are server-
            managed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, MCPUpstreamServer, MCPUpstreamValidationResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            validate_only=validate_only,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
