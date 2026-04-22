from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.list_agents_response_200 import ListAgentsResponse200
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union



def _get_kwargs(
    *,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    status: Union[Unset, str] = UNSET,
    risk_tier: Union[Unset, str] = UNSET,
    team: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id



    

    params: Dict[str, Any] = {}

    params["cursor"] = cursor

    params["limit"] = limit

    params["status"] = status

    params["risk_tier"] = risk_tier

    params["team"] = team


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/agents",
        "params": params,
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, ListAgentsResponse200]]:
    if response.status_code == 200:
        response_200 = ListAgentsResponse200.from_dict(response.json())



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


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, ListAgentsResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    status: Union[Unset, str] = UNSET,
    risk_tier: Union[Unset, str] = UNSET,
    team: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, ListAgentsResponse200]]:
    """ List agent identities

    Args:
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        status (Union[Unset, str]):
        risk_tier (Union[Unset, str]):
        team (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListAgentsResponse200]]
     """


    kwargs = _get_kwargs(
        cursor=cursor,
limit=limit,
status=status,
risk_tier=risk_tier,
team=team,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    status: Union[Unset, str] = UNSET,
    risk_tier: Union[Unset, str] = UNSET,
    team: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, ListAgentsResponse200]]:
    """ List agent identities

    Args:
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        status (Union[Unset, str]):
        risk_tier (Union[Unset, str]):
        team (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListAgentsResponse200]
     """


    return sync_detailed(
        client=client,
cursor=cursor,
limit=limit,
status=status,
risk_tier=risk_tier,
team=team,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    status: Union[Unset, str] = UNSET,
    risk_tier: Union[Unset, str] = UNSET,
    team: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, ListAgentsResponse200]]:
    """ List agent identities

    Args:
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        status (Union[Unset, str]):
        risk_tier (Union[Unset, str]):
        team (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListAgentsResponse200]]
     """


    kwargs = _get_kwargs(
        cursor=cursor,
limit=limit,
status=status,
risk_tier=risk_tier,
team=team,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 50,
    status: Union[Unset, str] = UNSET,
    risk_tier: Union[Unset, str] = UNSET,
    team: Union[Unset, str] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, ListAgentsResponse200]]:
    """ List agent identities

    Args:
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 50.
        status (Union[Unset, str]):
        risk_tier (Union[Unset, str]):
        team (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListAgentsResponse200]
     """


    return (await asyncio_detailed(
        client=client,
cursor=cursor,
limit=limit,
status=status,
risk_tier=risk_tier,
team=team,
x_tenant_id=x_tenant_id,

    )).parsed
