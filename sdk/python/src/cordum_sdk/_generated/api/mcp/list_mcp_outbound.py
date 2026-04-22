from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.list_mcp_outbound_response_200 import ListMcpOutboundResponse200
from ...types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime



def _get_kwargs(
    *,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id



    

    params: Dict[str, Any] = {}

    json_since: Union[Unset, str] = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: Union[Unset, str] = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params["limit"] = limit

    params["cursor"] = cursor


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/mcp/outbound",
        "params": params,
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, ListMcpOutboundResponse200]]:
    if response.status_code == 200:
        response_200 = ListMcpOutboundResponse200.from_dict(response.json())



        return response_200
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, ListMcpOutboundResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, ListMcpOutboundResponse200]]:
    """ List outbound MCP signed-call events

     Walks the tenant's audit chain stream and returns observed outbound MCP calls, filtered by time
    range + subject. Admin-only.

    Args:
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListMcpOutboundResponse200]]
     """


    kwargs = _get_kwargs(
        since=since,
until=until,
limit=limit,
cursor=cursor,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: AuthenticatedClient,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, ListMcpOutboundResponse200]]:
    """ List outbound MCP signed-call events

     Walks the tenant's audit chain stream and returns observed outbound MCP calls, filtered by time
    range + subject. Admin-only.

    Args:
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListMcpOutboundResponse200]
     """


    return sync_detailed(
        client=client,
since=since,
until=until,
limit=limit,
cursor=cursor,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, ListMcpOutboundResponse200]]:
    """ List outbound MCP signed-call events

     Walks the tenant's audit chain stream and returns observed outbound MCP calls, filtered by time
    range + subject. Admin-only.

    Args:
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListMcpOutboundResponse200]]
     """


    kwargs = _get_kwargs(
        since=since,
until=until,
limit=limit,
cursor=cursor,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: AuthenticatedClient,
    since: Union[Unset, datetime.datetime] = UNSET,
    until: Union[Unset, datetime.datetime] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, ListMcpOutboundResponse200]]:
    """ List outbound MCP signed-call events

     Walks the tenant's audit chain stream and returns observed outbound MCP calls, filtered by time
    range + subject. Admin-only.

    Args:
        since (Union[Unset, datetime.datetime]):
        until (Union[Unset, datetime.datetime]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListMcpOutboundResponse200]
     """


    return (await asyncio_detailed(
        client=client,
since=since,
until=until,
limit=limit,
cursor=cursor,
x_tenant_id=x_tenant_id,

    )).parsed
