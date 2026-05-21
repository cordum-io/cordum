from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.shadow_agent_finding import ShadowAgentFinding
from typing import cast
from typing import Dict


def _get_kwargs(
    finding_id: str,
    *,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/shadow-agents/{finding_id}".format(
            finding_id=finding_id,
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, ShadowAgentFinding]]:
    if response.status_code == 200:
        response_200 = ShadowAgentFinding.from_dict(response.json())

        return response_200
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404
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
) -> Response[Union[Any, ShadowAgentFinding]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    finding_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, ShadowAgentFinding]]:
    """Get a shadow-agent finding by id

     Returns one ShadowAgentFinding scoped to the caller's tenant. Cross-tenant lookups return 404 (not
    403) to prevent tuple-existence probing.

    Args:
        finding_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ShadowAgentFinding]]
    """

    kwargs = _get_kwargs(
        finding_id=finding_id,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    finding_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, ShadowAgentFinding]]:
    """Get a shadow-agent finding by id

     Returns one ShadowAgentFinding scoped to the caller's tenant. Cross-tenant lookups return 404 (not
    403) to prevent tuple-existence probing.

    Args:
        finding_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ShadowAgentFinding]
    """

    return sync_detailed(
        finding_id=finding_id,
        client=client,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    finding_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, ShadowAgentFinding]]:
    """Get a shadow-agent finding by id

     Returns one ShadowAgentFinding scoped to the caller's tenant. Cross-tenant lookups return 404 (not
    403) to prevent tuple-existence probing.

    Args:
        finding_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ShadowAgentFinding]]
    """

    kwargs = _get_kwargs(
        finding_id=finding_id,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    finding_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, ShadowAgentFinding]]:
    """Get a shadow-agent finding by id

     Returns one ShadowAgentFinding scoped to the caller's tenant. Cross-tenant lookups return 404 (not
    403) to prevent tuple-existence probing.

    Args:
        finding_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ShadowAgentFinding]
    """

    return (
        await asyncio_detailed(
            finding_id=finding_id,
            client=client,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
