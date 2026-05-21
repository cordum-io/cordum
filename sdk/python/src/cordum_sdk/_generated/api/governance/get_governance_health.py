from http import HTTPStatus
from typing import Any, Dict, List, Optional, Union, cast

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...types import UNSET, Unset
from typing import Union


def _get_kwargs(
    *,
    tenant: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Dict[str, Any]:
    headers: Dict[str, Any] = {}
    headers["X-Tenant-ID"] = x_tenant_id

    params: Dict[str, Any] = {}

    params["tenant"] = tenant

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: Dict[str, Any] = {
        "method": "get",
        "url": "/api/v1/governance/health",
        "params": params,
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Optional[Any]:
    if response.status_code == 200:
        return None
    if response.status_code == 401:
        return None
    if response.status_code == 403:
        return None
    if response.status_code == 500:
        return None
    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: Union[AuthenticatedClient, Client], response: httpx.Response
) -> Response[Any]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    tenant: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Any]:
    """Composite governance health score per tenant

     Returns the Command Center governance-health aggregate for the tenant:
    denial-rate, approval-latency p95, policy coverage, and audit-chain integrity
    rolled up into a 0-100 score plus A-F grade.

    Admin-only. When governance-health caching is enabled, results are cached
    per tenant for 60 seconds so dashboards can poll without forcing a full
    recompute on every refresh. If caching is disabled, the handler falls
    back to a fresh per-request cache and the endpoint behaves as uncached.

    Args:
        tenant (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any]
    """

    kwargs = _get_kwargs(
        tenant=tenant,
        x_tenant_id=x_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    tenant: Union[Unset, str] = UNSET,
    x_tenant_id: str,
) -> Response[Any]:
    """Composite governance health score per tenant

     Returns the Command Center governance-health aggregate for the tenant:
    denial-rate, approval-latency p95, policy coverage, and audit-chain integrity
    rolled up into a 0-100 score plus A-F grade.

    Admin-only. When governance-health caching is enabled, results are cached
    per tenant for 60 seconds so dashboards can poll without forcing a full
    recompute on every refresh. If caching is disabled, the handler falls
    back to a fresh per-request cache and the endpoint behaves as uncached.

    Args:
        tenant (Union[Unset, str]):
        x_tenant_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any]
    """

    kwargs = _get_kwargs(
        tenant=tenant,
        x_tenant_id=x_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)
