from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.list_shadow_exceptions_response import ListShadowExceptionsResponse
from ...models.list_shadow_exceptions_risk import ListShadowExceptionsRisk
from ...models.list_shadow_exceptions_source_type import ListShadowExceptionsSourceType
from ...models.list_shadow_exceptions_status import ListShadowExceptionsStatus
from ...types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union


def _get_kwargs(
    *,
    status: Union[Unset, ListShadowExceptionsStatus] = UNSET,
    source_type: Union[Unset, ListShadowExceptionsSourceType] = UNSET,
    risk: Union[Unset, ListShadowExceptionsRisk] = UNSET,
    limit: Union[Unset, int] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    json_status: Union[Unset, str] = UNSET
    if not isinstance(status, Unset):
        json_status = status.value

    params["status"] = json_status

    json_source_type: Union[Unset, str] = UNSET
    if not isinstance(source_type, Unset):
        json_source_type = source_type.value

    params["source_type"] = json_source_type

    json_risk: Union[Unset, str] = UNSET
    if not isinstance(risk, Unset):
        json_risk = risk.value

    params["risk"] = json_risk

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/shadow/exceptions",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, ListShadowExceptionsResponse]]:
    if response.status_code == 200:
        response_200 = ListShadowExceptionsResponse.from_dict(response.json())

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
) -> Response[Union[Any, ListShadowExceptionsResponse]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListShadowExceptionsStatus] = UNSET,
    source_type: Union[Unset, ListShadowExceptionsSourceType] = UNSET,
    risk: Union[Unset, ListShadowExceptionsRisk] = UNSET,
    limit: Union[Unset, int] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, ListShadowExceptionsResponse]]:
    """List shadow exceptions for the caller's tenant (EDGE-143.6)

     Returns a bounded, cursor-paginated page of exceptions for the
    requested tenant. Optional filters: status (active|revoked|
    expired), source_type (local|kubernetes|ci|network), risk
    (low|medium|high|critical).

    Args:
        status (Union[Unset, ListShadowExceptionsStatus]):
        source_type (Union[Unset, ListShadowExceptionsSourceType]):
        risk (Union[Unset, ListShadowExceptionsRisk]):
        limit (Union[Unset, int]):
        cursor (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListShadowExceptionsResponse]]
    """

    kwargs = _get_kwargs(
        status=status,
        source_type=source_type,
        risk=risk,
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
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListShadowExceptionsStatus] = UNSET,
    source_type: Union[Unset, ListShadowExceptionsSourceType] = UNSET,
    risk: Union[Unset, ListShadowExceptionsRisk] = UNSET,
    limit: Union[Unset, int] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, ListShadowExceptionsResponse]]:
    """List shadow exceptions for the caller's tenant (EDGE-143.6)

     Returns a bounded, cursor-paginated page of exceptions for the
    requested tenant. Optional filters: status (active|revoked|
    expired), source_type (local|kubernetes|ci|network), risk
    (low|medium|high|critical).

    Args:
        status (Union[Unset, ListShadowExceptionsStatus]):
        source_type (Union[Unset, ListShadowExceptionsSourceType]):
        risk (Union[Unset, ListShadowExceptionsRisk]):
        limit (Union[Unset, int]):
        cursor (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListShadowExceptionsResponse]
    """

    return sync_detailed(
        client=client,
        status=status,
        source_type=source_type,
        risk=risk,
        limit=limit,
        cursor=cursor,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListShadowExceptionsStatus] = UNSET,
    source_type: Union[Unset, ListShadowExceptionsSourceType] = UNSET,
    risk: Union[Unset, ListShadowExceptionsRisk] = UNSET,
    limit: Union[Unset, int] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Union[Any, ListShadowExceptionsResponse]]:
    """List shadow exceptions for the caller's tenant (EDGE-143.6)

     Returns a bounded, cursor-paginated page of exceptions for the
    requested tenant. Optional filters: status (active|revoked|
    expired), source_type (local|kubernetes|ci|network), risk
    (low|medium|high|critical).

    Args:
        status (Union[Unset, ListShadowExceptionsStatus]):
        source_type (Union[Unset, ListShadowExceptionsSourceType]):
        risk (Union[Unset, ListShadowExceptionsRisk]):
        limit (Union[Unset, int]):
        cursor (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, ListShadowExceptionsResponse]]
    """

    kwargs = _get_kwargs(
        status=status,
        source_type=source_type,
        risk=risk,
        limit=limit,
        cursor=cursor,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Union[AuthenticatedClient, Client],
    status: Union[Unset, ListShadowExceptionsStatus] = UNSET,
    source_type: Union[Unset, ListShadowExceptionsSourceType] = UNSET,
    risk: Union[Unset, ListShadowExceptionsRisk] = UNSET,
    limit: Union[Unset, int] = UNSET,
    cursor: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Optional[Union[Any, ListShadowExceptionsResponse]]:
    """List shadow exceptions for the caller's tenant (EDGE-143.6)

     Returns a bounded, cursor-paginated page of exceptions for the
    requested tenant. Optional filters: status (active|revoked|
    expired), source_type (local|kubernetes|ci|network), risk
    (low|medium|high|critical).

    Args:
        status (Union[Unset, ListShadowExceptionsStatus]):
        source_type (Union[Unset, ListShadowExceptionsSourceType]):
        risk (Union[Unset, ListShadowExceptionsRisk]):
        limit (Union[Unset, int]):
        cursor (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, ListShadowExceptionsResponse]
    """

    return (
        await asyncio_detailed(
            client=client,
            status=status,
            source_type=source_type,
            risk=risk,
            limit=limit,
            cursor=cursor,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
