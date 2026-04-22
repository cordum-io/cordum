from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.delegation_list_response import DelegationListResponse
from ...models.list_delegations_status import ListDelegationsStatus
from ...types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime



def _get_kwargs(
    *,
    status: Union[Unset, ListDelegationsStatus] = UNSET,
    scope: Union[Unset, str] = UNSET,
    before_expiry: Union[Unset, datetime.datetime] = UNSET,
    since_issued: Union[Unset, datetime.datetime] = UNSET,
    until_issued: Union[Unset, datetime.datetime] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id



    

    params: Dict[str, Any] = {}

    json_status: Union[Unset, str] = UNSET
    if not isinstance(status, Unset):
        json_status = status.value

    params["status"] = json_status

    params["scope"] = scope

    json_before_expiry: Union[Unset, str] = UNSET
    if not isinstance(before_expiry, Unset):
        json_before_expiry = before_expiry.isoformat()
    params["before_expiry"] = json_before_expiry

    json_since_issued: Union[Unset, str] = UNSET
    if not isinstance(since_issued, Unset):
        json_since_issued = since_issued.isoformat()
    params["since_issued"] = json_since_issued

    json_until_issued: Union[Unset, str] = UNSET
    if not isinstance(until_issued, Unset):
        json_until_issued = until_issued.isoformat()
    params["until_issued"] = json_until_issued

    params["cursor"] = cursor

    params["limit"] = limit


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/delegations",
        "params": params,
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, DelegationListResponse]]:
    if response.status_code == 200:
        response_200 = DelegationListResponse.from_dict(response.json())



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
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, DelegationListResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListDelegationsStatus] = UNSET,
    scope: Union[Unset, str] = UNSET,
    before_expiry: Union[Unset, datetime.datetime] = UNSET,
    since_issued: Union[Unset, datetime.datetime] = UNSET,
    until_issued: Union[Unset, datetime.datetime] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, DelegationListResponse]]:
    """ List delegation tokens for the current tenant

    Args:
        status (Union[Unset, ListDelegationsStatus]):
        scope (Union[Unset, str]):
        before_expiry (Union[Unset, datetime.datetime]):
        since_issued (Union[Unset, datetime.datetime]):
        until_issued (Union[Unset, datetime.datetime]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, DelegationListResponse]]
     """


    kwargs = _get_kwargs(
        status=status,
scope=scope,
before_expiry=before_expiry,
since_issued=since_issued,
until_issued=until_issued,
cursor=cursor,
limit=limit,
x_tenant_id=x_tenant_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListDelegationsStatus] = UNSET,
    scope: Union[Unset, str] = UNSET,
    before_expiry: Union[Unset, datetime.datetime] = UNSET,
    since_issued: Union[Unset, datetime.datetime] = UNSET,
    until_issued: Union[Unset, datetime.datetime] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, DelegationListResponse]]:
    """ List delegation tokens for the current tenant

    Args:
        status (Union[Unset, ListDelegationsStatus]):
        scope (Union[Unset, str]):
        before_expiry (Union[Unset, datetime.datetime]):
        since_issued (Union[Unset, datetime.datetime]):
        until_issued (Union[Unset, datetime.datetime]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, DelegationListResponse]
     """


    return sync_detailed(
        client=client,
status=status,
scope=scope,
before_expiry=before_expiry,
since_issued=since_issued,
until_issued=until_issued,
cursor=cursor,
limit=limit,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListDelegationsStatus] = UNSET,
    scope: Union[Unset, str] = UNSET,
    before_expiry: Union[Unset, datetime.datetime] = UNSET,
    since_issued: Union[Unset, datetime.datetime] = UNSET,
    until_issued: Union[Unset, datetime.datetime] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, DelegationListResponse]]:
    """ List delegation tokens for the current tenant

    Args:
        status (Union[Unset, ListDelegationsStatus]):
        scope (Union[Unset, str]):
        before_expiry (Union[Unset, datetime.datetime]):
        since_issued (Union[Unset, datetime.datetime]):
        until_issued (Union[Unset, datetime.datetime]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, DelegationListResponse]]
     """


    kwargs = _get_kwargs(
        status=status,
scope=scope,
before_expiry=before_expiry,
since_issued=since_issued,
until_issued=until_issued,
cursor=cursor,
limit=limit,
x_tenant_id=x_tenant_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListDelegationsStatus] = UNSET,
    scope: Union[Unset, str] = UNSET,
    before_expiry: Union[Unset, datetime.datetime] = UNSET,
    since_issued: Union[Unset, datetime.datetime] = UNSET,
    until_issued: Union[Unset, datetime.datetime] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, DelegationListResponse]]:
    """ List delegation tokens for the current tenant

    Args:
        status (Union[Unset, ListDelegationsStatus]):
        scope (Union[Unset, str]):
        before_expiry (Union[Unset, datetime.datetime]):
        since_issued (Union[Unset, datetime.datetime]):
        until_issued (Union[Unset, datetime.datetime]):
        cursor (Union[Unset, str]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, DelegationListResponse]
     """


    return (await asyncio_detailed(
        client=client,
status=status,
scope=scope,
before_expiry=before_expiry,
since_issued=since_issued,
until_issued=until_issued,
cursor=cursor,
limit=limit,
x_tenant_id=x_tenant_id,

    )).parsed
