from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.edge_approval import EdgeApproval
from ...models.edge_error import EdgeError
from typing import cast
from typing import Dict


def _get_kwargs(
    approval_ref: str,
    *,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/edge/approvals/{approval_ref}".format(
            approval_ref=approval_ref,
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, EdgeApproval, EdgeError]]:
    if response.status_code == 200:
        response_200 = EdgeApproval.from_dict(response.json())

        return response_200
    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401
    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403
    if response.status_code == 404:
        response_404 = EdgeError.from_dict(response.json())

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
) -> Response[Union[Any, EdgeApproval, EdgeError]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, EdgeApproval, EdgeError]]:
    """Get an Edge action approval

     Returns one tenant-scoped Edge approval record. Detail reads are principal-bound: the caller must be
    the original requester principal (`auth.PrincipalID` matches the approval `principal_id`) or hold an
    admin/operator role. Cross-tenant requests and same-tenant callers that fail this binding are
    reported as not found and no raw tool/action payload is exposed.

    Args:
        approval_ref (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeApproval, EdgeError]]
    """

    kwargs = _get_kwargs(
        approval_ref=approval_ref,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeApproval, EdgeError]]:
    """Get an Edge action approval

     Returns one tenant-scoped Edge approval record. Detail reads are principal-bound: the caller must be
    the original requester principal (`auth.PrincipalID` matches the approval `principal_id`) or hold an
    admin/operator role. Cross-tenant requests and same-tenant callers that fail this binding are
    reported as not found and no raw tool/action payload is exposed.

    Args:
        approval_ref (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeApproval, EdgeError]
    """

    return sync_detailed(
        approval_ref=approval_ref,
        client=client,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, EdgeApproval, EdgeError]]:
    """Get an Edge action approval

     Returns one tenant-scoped Edge approval record. Detail reads are principal-bound: the caller must be
    the original requester principal (`auth.PrincipalID` matches the approval `principal_id`) or hold an
    admin/operator role. Cross-tenant requests and same-tenant callers that fail this binding are
    reported as not found and no raw tool/action payload is exposed.

    Args:
        approval_ref (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, EdgeApproval, EdgeError]]
    """

    kwargs = _get_kwargs(
        approval_ref=approval_ref,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    approval_ref: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, EdgeApproval, EdgeError]]:
    """Get an Edge action approval

     Returns one tenant-scoped Edge approval record. Detail reads are principal-bound: the caller must be
    the original requester principal (`auth.PrincipalID` matches the approval `principal_id`) or hold an
    admin/operator role. Cross-tenant requests and same-tenant callers that fail this binding are
    reported as not found and no raw tool/action payload is exposed.

    Args:
        approval_ref (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, EdgeApproval, EdgeError]
    """

    return (
        await asyncio_detailed(
            approval_ref=approval_ref,
            client=client,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
