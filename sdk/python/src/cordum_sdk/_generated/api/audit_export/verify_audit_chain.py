from http import HTTPStatus
from typing import Any, Dict, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.error import Error
from ...models.generic_object import GenericObject
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union



def _get_kwargs(
    *,
    tenant: Union[Unset, str] = UNSET,
    since: Union[Unset, int] = UNSET,
    until: Union[Unset, int] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id





    params: Dict[str, Any] = {}

    params["tenant"] = tenant

    params["since"] = since

    params["until"] = until

    params["limit"] = limit


    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}


    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/audit/verify",
        "params": params,
    }


    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Optional[Union[Any, Error, GenericObject]]:
    if response.status_code == 200:
        response_200 = GenericObject.from_dict(response.json())



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
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if response.status_code == 503:
        response_503 = Error.from_dict(response.json())



        return response_503
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: Union[AuthenticatedClient, Client], response: httpx.Response) -> Response[Union[Any, Error, GenericObject]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    tenant: Union[Unset, str] = UNSET,
    since: Union[Unset, int] = UNSET,
    until: Union[Unset, int] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, Error, GenericObject]]:
    """ Verify audit chain integrity

     Walks the tenant's audit stream and attests integrity of the hash chain. Reports
    `status=ok` on a contiguous chain, `status=gap` with classification when seq numbers are
    missing (retention-trimmed vs suspected tampering), and `status=tamper` on a broken hash
    link. Admin-only and entitlement-gated; NEVER returns raw event bodies — this is an
    integrity report surface, not event retrieval.

    Args:
        tenant (Union[Unset, str]):
        since (Union[Unset, int]):
        until (Union[Unset, int]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, Error, GenericObject]]
     """


    kwargs = _get_kwargs(
        tenant=tenant,
since=since,
until=until,
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
    tenant: Union[Unset, str] = UNSET,
    since: Union[Unset, int] = UNSET,
    until: Union[Unset, int] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, Error, GenericObject]]:
    """ Verify audit chain integrity

     Walks the tenant's audit stream and attests integrity of the hash chain. Reports
    `status=ok` on a contiguous chain, `status=gap` with classification when seq numbers are
    missing (retention-trimmed vs suspected tampering), and `status=tamper` on a broken hash
    link. Admin-only and entitlement-gated; NEVER returns raw event bodies — this is an
    integrity report surface, not event retrieval.

    Args:
        tenant (Union[Unset, str]):
        since (Union[Unset, int]):
        until (Union[Unset, int]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, Error, GenericObject]
     """


    return sync_detailed(
        client=client,
tenant=tenant,
since=since,
until=until,
limit=limit,
x_tenant_id=x_tenant_id,

    ).parsed

async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    tenant: Union[Unset, str] = UNSET,
    since: Union[Unset, int] = UNSET,
    until: Union[Unset, int] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Response[Union[Any, Error, GenericObject]]:
    """ Verify audit chain integrity

     Walks the tenant's audit stream and attests integrity of the hash chain. Reports
    `status=ok` on a contiguous chain, `status=gap` with classification when seq numbers are
    missing (retention-trimmed vs suspected tampering), and `status=tamper` on a broken hash
    link. Admin-only and entitlement-gated; NEVER returns raw event bodies — this is an
    integrity report surface, not event retrieval.

    Args:
        tenant (Union[Unset, str]):
        since (Union[Unset, int]):
        until (Union[Unset, int]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, Error, GenericObject]]
     """


    kwargs = _get_kwargs(
        tenant=tenant,
since=since,
until=until,
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
    tenant: Union[Unset, str] = UNSET,
    since: Union[Unset, int] = UNSET,
    until: Union[Unset, int] = UNSET,
    limit: Union[Unset, int] = UNSET,
    x_tenant_id: str,

) -> Optional[Union[Any, Error, GenericObject]]:
    """ Verify audit chain integrity

     Walks the tenant's audit stream and attests integrity of the hash chain. Reports
    `status=ok` on a contiguous chain, `status=gap` with classification when seq numbers are
    missing (retention-trimmed vs suspected tampering), and `status=tamper` on a broken hash
    link. Admin-only and entitlement-gated; NEVER returns raw event bodies — this is an
    integrity report surface, not event retrieval.

    Args:
        tenant (Union[Unset, str]):
        since (Union[Unset, int]):
        until (Union[Unset, int]):
        limit (Union[Unset, int]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, Error, GenericObject]
     """


    return (await asyncio_detailed(
        client=client,
tenant=tenant,
since=since,
until=until,
limit=limit,
x_tenant_id=x_tenant_id,

    )).parsed
