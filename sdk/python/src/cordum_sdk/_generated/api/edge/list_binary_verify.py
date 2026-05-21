from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.binary_verify_events_envelope import BinaryVerifyEventsEnvelope
from ...models.list_binary_verify_event import ListBinaryVerifyEvent
from ...models.list_binary_verify_sig_scheme import ListBinaryVerifySigScheme
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union


def _get_kwargs(
    *,
    tenant: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event: Union[Unset, ListBinaryVerifyEvent] = UNSET,
    sig_scheme: Union[Unset, ListBinaryVerifySigScheme] = UNSET,
    endpoint: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["tenant"] = tenant

    params["limit"] = limit

    params["cursor"] = cursor

    json_event: Union[Unset, str] = UNSET
    if not isinstance(event, Unset):
        json_event = event.value

    params["event"] = json_event

    json_sig_scheme: Union[Unset, str] = UNSET
    if not isinstance(sig_scheme, Unset):
        json_sig_scheme = sig_scheme.value

    params["sig_scheme"] = json_sig_scheme

    params["endpoint"] = endpoint

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/binary-integrity/events",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, BinaryVerifyEventsEnvelope]]:
    if response.status_code == 200:
        response_200 = BinaryVerifyEventsEnvelope.from_dict(response.json())

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
) -> Response[Union[Any, BinaryVerifyEventsEnvelope]]:
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
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event: Union[Unset, ListBinaryVerifyEvent] = UNSET,
    sig_scheme: Union[Unset, ListBinaryVerifySigScheme] = UNSET,
    endpoint: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, BinaryVerifyEventsEnvelope]]:
    """List binary-verify outcomes for a tenant

     Returns a tenant-scoped reverse-chronological page of
    binary-verify outcomes recovered from the tenant audit
    stream. Filters narrow the page; values that fall outside
    the accepted enums return 400. The response envelope mirrors
    `/api/v1/audit/events` (items / next_cursor / returned) but
    each item carries the original BinaryVerifyEvent shape plus a
    server-side timestamp and endpoint label.

    Auth: requires `audit.read` permission or `admin` role,
    plus tenant access enforced via `X-Tenant-ID`.

    Args:
        tenant (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        event (Union[Unset, ListBinaryVerifyEvent]):
        sig_scheme (Union[Unset, ListBinaryVerifySigScheme]):
        endpoint (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, BinaryVerifyEventsEnvelope]]
    """

    kwargs = _get_kwargs(
        tenant=tenant,
        limit=limit,
        cursor=cursor,
        event=event,
        sig_scheme=sig_scheme,
        endpoint=endpoint,
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
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event: Union[Unset, ListBinaryVerifyEvent] = UNSET,
    sig_scheme: Union[Unset, ListBinaryVerifySigScheme] = UNSET,
    endpoint: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, BinaryVerifyEventsEnvelope]]:
    """List binary-verify outcomes for a tenant

     Returns a tenant-scoped reverse-chronological page of
    binary-verify outcomes recovered from the tenant audit
    stream. Filters narrow the page; values that fall outside
    the accepted enums return 400. The response envelope mirrors
    `/api/v1/audit/events` (items / next_cursor / returned) but
    each item carries the original BinaryVerifyEvent shape plus a
    server-side timestamp and endpoint label.

    Auth: requires `audit.read` permission or `admin` role,
    plus tenant access enforced via `X-Tenant-ID`.

    Args:
        tenant (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        event (Union[Unset, ListBinaryVerifyEvent]):
        sig_scheme (Union[Unset, ListBinaryVerifySigScheme]):
        endpoint (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, BinaryVerifyEventsEnvelope]
    """

    return sync_detailed(
        client=client,
        tenant=tenant,
        limit=limit,
        cursor=cursor,
        event=event,
        sig_scheme=sig_scheme,
        endpoint=endpoint,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    tenant: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event: Union[Unset, ListBinaryVerifyEvent] = UNSET,
    sig_scheme: Union[Unset, ListBinaryVerifySigScheme] = UNSET,
    endpoint: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, BinaryVerifyEventsEnvelope]]:
    """List binary-verify outcomes for a tenant

     Returns a tenant-scoped reverse-chronological page of
    binary-verify outcomes recovered from the tenant audit
    stream. Filters narrow the page; values that fall outside
    the accepted enums return 400. The response envelope mirrors
    `/api/v1/audit/events` (items / next_cursor / returned) but
    each item carries the original BinaryVerifyEvent shape plus a
    server-side timestamp and endpoint label.

    Auth: requires `audit.read` permission or `admin` role,
    plus tenant access enforced via `X-Tenant-ID`.

    Args:
        tenant (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        event (Union[Unset, ListBinaryVerifyEvent]):
        sig_scheme (Union[Unset, ListBinaryVerifySigScheme]):
        endpoint (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, BinaryVerifyEventsEnvelope]]
    """

    kwargs = _get_kwargs(
        tenant=tenant,
        limit=limit,
        cursor=cursor,
        event=event,
        sig_scheme=sig_scheme,
        endpoint=endpoint,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    tenant: Union[Unset, str] = UNSET,
    limit: Union[Unset, int] = 100,
    cursor: Union[Unset, str] = UNSET,
    event: Union[Unset, ListBinaryVerifyEvent] = UNSET,
    sig_scheme: Union[Unset, ListBinaryVerifySigScheme] = UNSET,
    endpoint: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, BinaryVerifyEventsEnvelope]]:
    """List binary-verify outcomes for a tenant

     Returns a tenant-scoped reverse-chronological page of
    binary-verify outcomes recovered from the tenant audit
    stream. Filters narrow the page; values that fall outside
    the accepted enums return 400. The response envelope mirrors
    `/api/v1/audit/events` (items / next_cursor / returned) but
    each item carries the original BinaryVerifyEvent shape plus a
    server-side timestamp and endpoint label.

    Auth: requires `audit.read` permission or `admin` role,
    plus tenant access enforced via `X-Tenant-ID`.

    Args:
        tenant (Union[Unset, str]):
        limit (Union[Unset, int]):  Default: 100.
        cursor (Union[Unset, str]):
        event (Union[Unset, ListBinaryVerifyEvent]):
        sig_scheme (Union[Unset, ListBinaryVerifySigScheme]):
        endpoint (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, BinaryVerifyEventsEnvelope]
    """

    return (
        await asyncio_detailed(
            client=client,
            tenant=tenant,
            limit=limit,
            cursor=cursor,
            event=event,
            sig_scheme=sig_scheme,
            endpoint=endpoint,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
