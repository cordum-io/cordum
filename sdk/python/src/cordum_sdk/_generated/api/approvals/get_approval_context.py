from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.get_approval_context_response_200 import GetApprovalContextResponse200
from typing import cast
from typing import Dict


def _get_kwargs(
    job_id: str,
    *,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/approvals/{job_id}/context".format(
            job_id=job_id,
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Union[Any, GetApprovalContextResponse200]]:
    if response.status_code == 200:
        response_200 = GetApprovalContextResponse200.from_dict(response.json())

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
    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Union[Any, GetApprovalContextResponse200]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    job_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, GetApprovalContextResponse200]]:
    """Get enriched approval context for a single job

     Returns all enriched context for a single approval in one call: blast radius, prior approvals,
    rollback hints, policy snapshot summary, time remaining, and parsed constraints. Used by the
    approval detail page.

    Args:
        job_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, GetApprovalContextResponse200]]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    job_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, GetApprovalContextResponse200]]:
    """Get enriched approval context for a single job

     Returns all enriched context for a single approval in one call: blast radius, prior approvals,
    rollback hints, policy snapshot summary, time remaining, and parsed constraints. Used by the
    approval detail page.

    Args:
        job_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, GetApprovalContextResponse200]
    """

    return sync_detailed(
        job_id=job_id,
        client=client,
        x_tenant_id=x_tenant_id,
    ).parsed


async def asyncio_detailed(
    job_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Response[Union[Any, GetApprovalContextResponse200]]:
    """Get enriched approval context for a single job

     Returns all enriched context for a single approval in one call: blast radius, prior approvals,
    rollback hints, policy snapshot summary, time remaining, and parsed constraints. Used by the
    approval detail page.

    Args:
        job_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Union[Any, GetApprovalContextResponse200]]
    """

    kwargs = _get_kwargs(
        job_id=job_id,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    job_id: str,
    *,
    client: Union[AuthenticatedClient, Client],
    x_tenant_id: str,
) -> Optional[Union[Any, GetApprovalContextResponse200]]:
    """Get enriched approval context for a single job

     Returns all enriched context for a single approval in one call: blast radius, prior approvals,
    rollback hints, policy snapshot summary, time remaining, and parsed constraints. Used by the
    approval detail page.

    Args:
        job_id (str):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Union[Any, GetApprovalContextResponse200]
    """

    return (
        await asyncio_detailed(
            job_id=job_id,
            client=client,
            x_tenant_id=x_tenant_id,
        )
    ).parsed
