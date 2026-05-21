from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.mcp_upstream_list_response import MCPUpstreamListResponse
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union


def _get_kwargs(
    *,
    enabled: Union[Unset, bool] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["enabled"] = enabled

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/mcp/upstreams",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, MCPUpstreamListResponse]]:
    if response.status_code == 200:
        response_200 = MCPUpstreamListResponse.from_dict(response.json())

        return response_200
    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
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
) -> Response[Union[Any, MCPUpstreamListResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    enabled: Union[Unset, bool] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, MCPUpstreamListResponse]]:
    """List approved upstream MCP servers

     Lists tenant-scoped upstream MCP registry entries plus system-wide entries. Secret material is never
    resolved; `auth_secret_ref` is returned only as a redacted `secret://` reference.

    Args:
        enabled (Union[Unset, bool]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, MCPUpstreamListResponse]]
    """

    kwargs = _get_kwargs(
        enabled=enabled,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    enabled: Union[Unset, bool] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, MCPUpstreamListResponse]]:
    """List approved upstream MCP servers

     Lists tenant-scoped upstream MCP registry entries plus system-wide entries. Secret material is never
    resolved; `auth_secret_ref` is returned only as a redacted `secret://` reference.

    Args:
        enabled (Union[Unset, bool]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, MCPUpstreamListResponse]
    """

    return sync_detailed(
        client=client,
        enabled=enabled,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    enabled: Union[Unset, bool] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, MCPUpstreamListResponse]]:
    """List approved upstream MCP servers

     Lists tenant-scoped upstream MCP registry entries plus system-wide entries. Secret material is never
    resolved; `auth_secret_ref` is returned only as a redacted `secret://` reference.

    Args:
        enabled (Union[Unset, bool]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, MCPUpstreamListResponse]]
    """

    kwargs = _get_kwargs(
        enabled=enabled,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    enabled: Union[Unset, bool] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, MCPUpstreamListResponse]]:
    """List approved upstream MCP servers

     Lists tenant-scoped upstream MCP registry entries plus system-wide entries. Secret material is never
    resolved; `auth_secret_ref` is returned only as a redacted `secret://` reference.

    Args:
        enabled (Union[Unset, bool]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, MCPUpstreamListResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            enabled=enabled,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
